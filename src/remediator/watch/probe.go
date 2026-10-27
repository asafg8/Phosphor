package watch

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Probe witness budget.
const (
	ProbeTimeout   = 5 * time.Second
	ProbeRetries   = 2
	ProbeMaxURLs   = 5
	ProbeRoundEach = 30 * time.Second
)

// ProbeBackoff is the wait before retry 1 and 2.
var ProbeBackoff = []time.Duration{time.Second, 2 * time.Second}

// bodyFatalRE matches PHP fatal output in a response body (body-fatal).
var bodyFatalRE = regexp.MustCompile(`(?i)(PHP )?(Fatal error|Parse error)(</b>)?:|Uncaught (Error|Exception)`)

// sideEffectRE are paths never replayed by the probe (state-changing or auth endpoints).
var sideEffectRE = regexp.MustCompile(`(?i)log-?out|log-?in|wp-login|sign-?(in|out|up)|/admin|wp-admin|wp-cron|cron|delete|remove|destroy|` +
	`cart|checkout|order|pay|purchase|subscribe|unsubscribe|reset|token|confirm|activate|install|upgrade|update|xmlrpc|[?&](action|do|op|cmd)=`)

// ProbeResult is the outcome of one URL after retries.
type ProbeResult struct {
	URL       string `json:"url"`
	Status    int    `json:"status"`
	Timeout   bool   `json:"timeout,omitempty"`
	BodyFatal bool   `json:"body_fatal,omitempty"`
	Err       string `json:"err,omitempty"`
	Attempts  int    `json:"attempts"`
}

// Failed: timeout, refused, 5xx / not 2xx, or a fatal in the body.
func (r ProbeResult) Failed() bool {
	return r.Timeout || r.Status < 200 || r.Status >= 300 || r.BodyFatal
}

// Answered failures are what counts under saturation: the server replied (or refused) wrongly; timeouts never.
func (r ProbeResult) Answered() bool { return r.Failed() && !r.Timeout }

// Prober replays URLs with the probe budget. Do is replaceable in tests.
type Prober struct {
	Client *http.Client
	Sleep  func(time.Duration)
	// BaseFatal lists URLs whose body already showed a fatal at baseline (body-fatal does not count for them).
	BaseFatal map[string]bool
}

func NewProber() *Prober {
	return &Prober{Client: &http.Client{Timeout: ProbeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}, Sleep: time.Sleep}
}

func (p *Prober) once(u string) ProbeResult {
	r := ProbeResult{URL: u}
	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		r.Err = err.Error()
		return r
	}
	req.Header.Set("User-Agent", "phosphor-watch-probe")
	resp, err := p.Client.Do(req)
	if err != nil {
		r.Err = err.Error()
		r.Timeout = ctx.Err() != nil || strings.Contains(err.Error(), "Timeout") || strings.Contains(err.Error(), "deadline")
		return r
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	r.Status = resp.StatusCode
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		r.Status = 200 // a redirect is an answer the app gave on purpose (login walls, trailing slash)
	}
	r.BodyFatal = bodyFatalRE.Match(b) && !p.BaseFatal[u]
	return r
}

// One probes u with up to ProbeRetries retries (1 s / 2 s backoff); the first success wins.
func (p *Prober) One(u string) ProbeResult {
	var r ProbeResult
	for i := 0; i <= ProbeRetries; i++ {
		if i > 0 {
			p.Sleep(ProbeBackoff[i-1])
		}
		r = p.once(u)
		r.Attempts = i + 1
		if !r.Failed() {
			return r
		}
	}
	return r
}

// Round probes every URL concurrently (≤ ProbeMaxURLs).
func (p *Prober) Round(urls []string) []ProbeResult {
	if len(urls) > ProbeMaxURLs {
		urls = urls[:ProbeMaxURLs]
	}
	out := make([]ProbeResult, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			out[i] = p.One(u)
		}(i, u)
	}
	wg.Wait()
	return out
}

// Judge turns a round's results into a ProbeRound: a round fails when ≥ 50% of the URLs failed; under saturation
// only answered failures count (timeouts never).
func Judge(t time.Time, res []ProbeResult, saturation bool) c.ProbeRound {
	pr := c.ProbeRound{T: t, Total: len(res), Counted: true}
	for _, r := range res {
		if r.Failed() {
			pr.Failed++
		}
		if r.Answered() {
			pr.Answered++
		}
	}
	n := pr.Failed
	if saturation {
		n = pr.Answered
		pr.Counted = pr.Answered == pr.Failed
	}
	pr.OK = pr.Total == 0 || 2*n < pr.Total
	return pr
}

// Witness reports whether 2 of the last 3 rounds failed.
func Witness(rounds []c.ProbeRound) bool {
	if len(rounds) < 2 {
		return false
	}
	from := max(0, len(rounds)-3)
	bad := 0
	for _, r := range rounds[from:] {
		if !r.OK {
			bad++
		}
	}
	return bad >= 2
}

// ExitRateThreshold is max(3× baseline, baseline + 2) exits per minute.
func ExitRateThreshold(basePM float64) float64 { return math.Max(3*basePM, basePM+2) }

// ExitRateBreach: the last 3 consecutive minutes are all over the threshold.
func ExitRateBreach(perMin []int, basePM float64) bool {
	if len(perMin) < 3 {
		return false
	}
	th := ExitRateThreshold(basePM)
	for _, n := range perMin[len(perMin)-3:] {
		if float64(n) <= th {
			return false
		}
	}
	return true
}

// Evidence is the per-minute hold evidence.
type Evidence struct {
	Attack     bool // front-proxy large/multipart 502s, rpm > 5× baseline, exits on non-probe requests
	Saturation bool // pm.max_children / MaxRequestWorkers / an active exit-rate alert
	General    bool // new-fatal, segv, body-fatal or 5xx-rate on general traffic
	ExitRate   bool // exit-rate breach
}

// HeldReason applies the held precedence: under-attack > saturation > general-traffic-signal > exit-rate.
func HeldReason(e Evidence) c.Reason {
	switch {
	case e.Attack:
		return "under-attack"
	case e.Saturation:
		return "saturation"
	case e.General:
		return "general-traffic-signal"
	case e.ExitRate:
		return "exit-rate"
	}
	return ""
}

// ProbeURLs picks the most frequent baseline 2xx GET paths (side-effect denylist applied) as loopback URLs on base.
func ProbeURLs(acc []Access, base string, n int) []string {
	count := map[string]int{}
	for _, a := range acc {
		if a.Probe || a.Method != "GET" || a.Status < 200 || a.Status >= 300 || a.Path == "" || sideEffectRE.MatchString(a.Path) {
			continue
		}
		count[a.Path]++
	}
	paths := make([]string, 0, len(count))
	for p := range count {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, k int) bool {
		if count[paths[i]] != count[paths[k]] {
			return count[paths[i]] > count[paths[k]]
		}
		return paths[i] < paths[k]
	})
	if len(paths) > n {
		paths = paths[:n]
	}
	bu, err := url.Parse(base)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range paths {
		out = append(out, bu.Scheme+"://"+bu.Host+p)
	}
	return out
}
