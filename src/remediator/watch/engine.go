package watch

import (
	"context"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// Config is watch-config.json, written by the swap right before it changes anything, and
// read by the watch (contract.WatchConfig).
type Config = c.WatchConfig

// Prepare computes the baseline from the last 24 h of logs and records tail offsets.
func Prepare(cfg *Config) {
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	var first, last time.Time
	req, s5 := 0, 0
	var acc []Access
	for _, p := range cfg.Access {
		for _, f := range []string{p + ".1", p} {
			lines, _ := readFrom(f, 0)
			for _, l := range lines {
				a, ok := ParseAccess(l)
				if !ok || a.Probe || a.T.Before(from) {
					continue
				}
				req++
				acc = append(acc, a)
				if a.Status >= 500 {
					s5++
				}
				if first.IsZero() || a.T.Before(first) {
					first = a.T
				}
				if a.T.After(last) {
					last = a.T
				}
			}
		}
	}
	sigs := map[string]int{}
	exits := 0
	for _, p := range cfg.ErrorLogs {
		for _, f := range []string{p + ".1", p} {
			lines, _ := readFrom(f, 0)
			for _, l := range lines {
				ev := Classify(l)
				if ev.Sig != "" {
					sigs[ev.Sig]++
				}
				if ev.Exit && !ev.T.IsZero() && !ev.T.Before(from) {
					exits++
				}
			}
		}
	}
	b := c.Baseline{From: first.UTC(), To: now, Requests: req, Signatures: []c.SigCount{}}
	if first.IsZero() {
		b.From = now
	}
	mins := last.Sub(first).Minutes()
	if mins < 1 {
		mins = 1
	}
	b.RPM = float64(req) / mins
	b.ExitsPM = float64(exits) / mins
	if req > 0 {
		b.Rate5xx = float64(s5) / float64(req)
	}
	for s, n := range sigs {
		b.Signatures = append(b.Signatures, c.SigCount{Sig: s, Count: n})
	}
	sort.Slice(b.Signatures, func(i, j int) bool { return b.Signatures[i].Sig < b.Signatures[j].Sig })
	b.Sufficient = Sufficient(req, b.RPM)
	cfg.Baseline = b
	cfg.Offsets = map[string]Pos{}
	for _, p := range append(append([]string{}, cfg.Access...), cfg.ErrorLogs...) {
		cfg.Offsets[p] = Current(p)
	}
	if cfg.ProbeURL != "" {
		cfg.ProbeOK = Probe(cfg.ProbeURL) == 200
		// replay the most frequent baseline 2xx GETs; keep only those that answer cleanly right now (a URL that
		// fails before the swap cannot witness a regression), remember baseline body-fatal URLs
		pr := NewProber()
		cfg.BaseFatal = map[string]bool{}
		for _, r := range pr.Round(ProbeURLs(acc, cfg.ProbeURL, ProbeMaxURLs)) {
			if r.BodyFatal {
				cfg.BaseFatal[r.URL] = true
			}
			if r.Status >= 200 && r.Status < 300 {
				cfg.ProbeURLs = append(cfg.ProbeURLs, r.URL)
			}
		}
		if len(cfg.ProbeURLs) == 0 && cfg.ProbeOK {
			cfg.ProbeURLs = []string{cfg.ProbeURL} // no baseline traffic: the health route alone
		}
	}
	if cfg.SampleS == 0 {
		cfg.SampleS = 15
	}
	cfg.Start = now
}

// Rebase starts the window now: tails continue from the current end of every log (the baseline is kept).
func Rebase(cfg *Config) {
	for p := range cfg.Offsets {
		cfg.Offsets[p] = Current(p)
	}
	cfg.Start = time.Now().UTC()
}

var probeClient = &http.Client{Timeout: 5 * time.Second}

// Probe GETs the probe URL with a marked User-Agent (excluded from traffic counts); returns status or 0.
func Probe(u string) int {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "phosphor-watch-probe")
	resp, err := probeClient.Do(req)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

// ProcUp checks the service is alive (systemd unit active, supervisord program RUNNING, or a matching process).
func ProcUp(cfg *Config) bool {
	switch cfg.Manager {
	case "systemd":
		out, _ := sys.Combined(10*time.Second, nil, "systemctl", "is-active", cfg.Unit)
		return strings.TrimSpace(out) == "active" || strings.TrimSpace(out) == "reloading"
	case "supervisord":
		out, _ := sys.Combined(10*time.Second, nil, "supervisorctl", "status", cfg.Program)
		return strings.Contains(out, "RUNNING")
	}
	if cfg.ProcMatch == "" {
		return true
	}
	for _, p := range sys.Procs() {
		if strings.Contains(p.Exe, cfg.ProcMatch) || strings.Contains(p.Comm, cfg.ProcMatch) {
			return true
		}
	}
	return false
}

// Hooks connect the engine to the agent (persistence, events, rollback).
type Hooks struct {
	Save     func(w *c.Watch)
	Event    func(typ c.EventType, step, code string, data map[string]any)
	Rollback func(trigger c.Trigger, evidence map[string]any) error
}

// Run samples for the window, host-local only. Rollback only on clean-traffic evidence: the probe witness (2 of
// 3 replay rounds failing), a fatal in a probe response body, a known-regression signature, the swap being reverted
// under us, or — only when there is no witness URL — the service down for 30 s.
// Everything else that looks wrong — exit rate, new-fatal / segv / 5xx on general traffic, attack or saturation —
// ends the watch `held` with the precedence reason; the patched build stays live. ctx cancellation (abort / undo /
// operator) ends it `aborted` with no rollback of its own.
func Run(ctx context.Context, cfg *Config, w *c.Watch, h Hooks) {
	// with the window log drop-in, logging was off before the swap, so the baseline holds no error-log
	// signatures and every pre-existing fatal would read "new". Such fatals are recorded, never a hold; the probe
	// witness and body-fatal (probe responses compared with their own baseline) still decide.
	blindBaseline := cfg.LogPath != ""
	seenBlind := map[string]bool{}
	window := time.Duration(cfg.WindowS) * time.Second
	sample := time.Duration(cfg.SampleS) * time.Second
	known := map[string]bool{}
	for _, s := range cfg.Baseline.Signatures {
		known[s.Sig] = true
	}
	offsets := cfg.Offsets
	var buckets []Bucket
	var exitsPerMin []int
	cur := Bucket{}
	curExits, curLarge, curSat := 0, 0, false
	inBucket := 0
	downSince := time.Time{}
	deadline := cfg.Start.Add(window)
	oomBase := oomKills(cfg.Unit)
	prober := NewProber()
	col := NewCollector(cfg.SAPI, cfg.MaxConnPerChild)
	prober.BaseFatal = cfg.BaseFatal
	lastRound := time.Time{}
	var lastRes []ProbeResult
	roundFresh := false
	w.ProbeURLs = cfg.ProbeURLs
	var general, attack, saturation bool
	genEv := map[string]any{}
	tick := time.NewTicker(sample)
	defer tick.Stop()
	fire := func(rule c.Trigger, ev map[string]any) {
		th := c.TriggerHit{Rule: rule, At: time.Now().UTC(), Evidence: ev}
		if r, _ := ev["regression"].(string); r != "" {
			th.MatchedRegression = r
		}
		w.Triggers = append(w.Triggers, th)
		w.Decision = "rollback"
		h.Event(c.EvTrigger, "watch", string(rule), ev)
		h.Save(w)
		if err := h.Rollback(rule, ev); err != nil {
			h.Event(c.EvError, "rollback", string(c.ERollbackFailed), map[string]any{"trigger": string(rule)})
		}
		h.Save(w)
	}
	hold := func(e Evidence, ev map[string]any) {
		reason := HeldReason(e)
		w.Decision, w.HeldReason = "held", &reason
		ev["reason"] = string(reason)
		// the signal that fired is recorded as a trigger hit; held is the decision, never a rollback
		if rule := c.Trigger(str(ev["signal"])); e.ExitRate || rule != "" {
			if e.ExitRate {
				rule = "worker-exit-rate"
			}
			w.Triggers = append(w.Triggers, c.TriggerHit{Rule: rule, At: time.Now().UTC(), Evidence: ev})
		}
		if e.ExitRate {
			h.Event(c.EvTrigger, "watch", "worker-exit-rate", ev)
		}
		h.Event(c.EvStatus, "watch", "held", ev)
		h.Save(w)
	}
	for {
		select {
		case <-ctx.Done():
			w.Decision = "aborted"
			h.Save(w)
			return
		case <-tick.C:
		}
		now := time.Now().UTC()
		s := c.Sample{T: now}
		var segvLine, fatalLine, fatalSig, regression, regLine string
		for _, p := range cfg.Access {
			lines, np := ReadNew(p, offsets[p])
			offsets[p] = np
			for _, l := range lines {
				if a, ok := ParseAccess(l); ok && !a.Probe {
					s.Req++
					if a.Status >= 500 {
						s.S5xx++
					}
					if (a.Status == 502 || a.Status == 413 || a.Status == 499) && (a.Multipart || a.ReqLen > 1<<20) {
						s.Large502++
					}
				}
			}
		}
		sig9 := 0
		for _, p := range cfg.ErrorLogs {
			if slices.Contains(cfg.Access, p) {
				continue
			}
			lines, np := ReadNew(p, offsets[p])
			offsets[p] = np
			for _, l := range lines {
				ev := Classify(l)
				col.Line(l, ev)
				if ev.Segv {
					s.Segv++
					segvLine = l
				}
				if ev.MaxChd {
					s.FpmMaxChildren++
				}
				if ev.Exit {
					s.WorkerExits++
					if ev.Sig9 {
						sig9++
					}
				}
				if ev.Regression != "" {
					regression, regLine = ev.Regression, l
				}
				if ev.Sig != "" && ev.Fatal && !known[ev.Sig] {
					s.NewSigs++
					if fatalSig == "" {
						fatalSig, fatalLine = ev.Sig, l
					}
					if blindBaseline && !seenBlind[ev.Sig] {
						seenBlind[ev.Sig] = true
						h.Event(c.EvInfo, "watch", "fatal-without-baseline", map[string]any{"sig": ev.Sig, "line": trim(l)})
					}
				}
			}
		}
		// an OOM kill under the MemoryMax cap is recorded, never a trigger, and not an exit for the rate
		if n := oomKills(cfg.Unit); n > oomBase {
			s.OOMKills = n - oomBase
			oomBase = n
			s.WorkerExits -= min(s.OOMKills, sig9)
			h.Event(c.EvStep, "watch", "oom-kill", map[string]any{"count": s.OOMKills, "unit": cfg.Unit})
		}
		col.Tick(&s)
		if cfg.LogPath != "" {
			CapLog(cfg.LogPath, LogCap)
		}
		s.ProcUp = ProcUp(cfg)
		if cfg.ProbeURL != "" {
			s.ProbeStatus = Probe(cfg.ProbeURL)
		}
		w.Samples = append(w.Samples, s)
		cur.Req += s.Req
		cur.S5xx += s.S5xx
		curExits += s.WorkerExits
		curLarge += s.Large502
		curSat = curSat || s.FpmMaxChildren > 0
		inBucket++
		if inBucket*cfg.SampleS >= 60 {
			buckets = append(buckets, cur)
			exitsPerMin = append(exitsPerMin, curExits)
			rpmSpike := cfg.Baseline.Sufficient && float64(cur.Req) > 5*cfg.Baseline.RPM
			if rpmSpike || curLarge >= 3 {
				attack = true
				w.Markers = append(w.Markers, c.WatchMarker{T: now, Kind: "attack", Evidence: map[string]any{"rpm": cur.Req,
					"base_rpm": cfg.Baseline.RPM, "large_502": curLarge}})
			}
			if curSat {
				w.Markers = append(w.Markers, c.WatchMarker{T: now, Kind: "saturation"})
			}
			h.Event(c.EvStep, "watch-minute", "", map[string]any{"minute": len(buckets), "req": cur.Req, "s5xx": cur.S5xx,
				"rate_5xx": Bucket(cur).Rate(), "base_5xx": cfg.Baseline.Rate5xx, "worker_exits": curExits,
				"exit_threshold": ExitRateThreshold(cfg.Baseline.ExitsPM)})
			cur, curExits, curLarge, inBucket = Bucket{}, 0, 0, 0
			saturation, curSat = curSat, false
		}
		exitRate := ExitRateBreach(exitsPerMin, cfg.Baseline.ExitsPM)
		// an active exit-rate alert counts as saturation for the probe (timeouts stop counting); the held
		// reason stays exit-rate unless real saturation markers (max_children / MaxRequestWorkers) were seen
		probeSat := saturation || exitRate

		// probe witness round every 30 s
		if len(cfg.ProbeURLs) > 0 && (lastRound.IsZero() || now.Sub(lastRound) >= ProbeRoundEach-time.Second) {
			lastRound = now
			res := prober.Round(cfg.ProbeURLs)
			lastRes, roundFresh = res, true
			pr := Judge(now, res, probeSat || s.FpmMaxChildren > 0)
			w.ProbeRounds = append(w.ProbeRounds, pr)
			if !pr.OK {
				h.Event(c.EvStep, "watch", "probe-round-failed", map[string]any{"results": res, "saturation": probeSat})
			}
		}
		h.Save(w)

		switch {
		case regression != "":
			fire("known-regression", map[string]any{"regression": regression, "line": trim(regLine)})
			return
		case len(cfg.VerifyCheck) > 0:
			if _, code := sys.Combined(15*time.Second, nil, cfg.VerifyCheck[0], cfg.VerifyCheck[1:]...); code != 0 {
				fire("swap-reverted", map[string]any{"check": strings.Join(cfg.VerifyCheck, " ")})
				return
			}
		}
		witness := Witness(w.ProbeRounds)
		// a fatal in a probe response body (absent at baseline) is a regression on the probe request itself
		bodyFatal := []string{}
		if roundFresh {
			for _, r := range lastRes {
				if r.BodyFatal {
					bodyFatal = append(bodyFatal, r.URL)
				}
			}
		}
		roundFresh = false
		if len(bodyFatal) > 0 && !attack {
			fire("body-fatal", map[string]any{"urls": bodyFatal, "sig": fatalSig, "line": trim(fatalLine)})
			return
		}
		if !s.ProcUp {
			if downSince.IsZero() {
				downSince = now
			}
		} else {
			downSince = time.Time{}
		}
		if witness { // the probe witness is the only probe rollback (no separate health-route trigger)
			ev := map[string]any{"rounds": lastN(w.ProbeRounds, 3), "exit_rate": exitRate,
				"worker_exits": exitsPerMin}
			if !downSince.IsZero() {
				ev["proc_down_since"] = downSince
			}
			if attack { // under-attack wins even when probes fail
				hold(Evidence{Attack: true, Saturation: saturation, ExitRate: exitRate}, ev)
				return
			}
			rule := c.Trigger("probe-witness")
			fire(rule, ev)
			return
		}
		if len(cfg.ProbeURLs) == 0 && !downSince.IsZero() && now.Sub(downSince) > 30*time.Second {
			fire("proc-down", map[string]any{"since": downSince, "unit": cfg.Unit, "program": cfg.Program})
			return // no witness available: a dead service is the only clean signal left
		}

		// general-traffic signals and rates → held (never a rollback)
		if s.Segv > 0 {
			general, genEv = true, map[string]any{"signal": "segv", "line": trim(segvLine)}
		} else if s.NewSigs > 0 && !blindBaseline {
			general, genEv = true, map[string]any{"signal": "new-fatal", "sig": fatalSig, "line": trim(fatalLine)}
		} else if FiveXXTrigger(buckets, cfg.Baseline.Rate5xx) {
			last := buckets[len(buckets)-1]
			general, genEv = true, map[string]any{"signal": "5xx-rate", "rate": last.Rate(), "threshold": Threshold5xx(cfg.Baseline.Rate5xx)}
		}
		satProbeTimeouts := probeSat && len(w.ProbeRounds) > 0 && !w.ProbeRounds[len(w.ProbeRounds)-1].Counted
		// the witness decides first: while the latest round failed, a general signal waits for the next rounds
		// (the same fatal on probe requests is a rollback, not a hold)
		witnessPending := len(w.ProbeRounds) > 0 && !w.ProbeRounds[len(w.ProbeRounds)-1].OK && !attack
		if !witnessPending && (general || exitRate || satProbeTimeouts || (attack && (s.WorkerExits > 0 || s.S5xx > 0))) {
			ev := genEv
			ev["worker_exits"], ev["exit_threshold"] = exitsPerMin, ExitRateThreshold(cfg.Baseline.ExitsPM)
			ev["s5xx"], ev["oom_kills"] = s.S5xx, s.OOMKills
			if !general {
				ev["signal"] = ""
			}
			// an exit spike together with 5xx is attack evidence (attacker-reachable deaths), not a
			// general-traffic regression
			polluted := exitRate && general && str(genEv["signal"]) == "5xx-rate"
			hold(Evidence{Attack: attack || polluted, Saturation: saturation || (satProbeTimeouts && !exitRate), General: general && !polluted, ExitRate: exitRate}, ev)
			return
		}
		if !now.Before(deadline) {
			w.Decision = "pass"
			h.Save(w)
			return
		}
	}
}

func lastN(r []c.ProbeRound, n int) []c.ProbeRound {
	if len(r) > n {
		return r[len(r)-n:]
	}
	return r
}

func str(v any) string { s, _ := v.(string); return s }

func trim(s string) string {
	if len(s) > 600 {
		return s[:600]
	}
	return s
}

// oomKills reads the unit cgroup's memory.events oom_kill counter (cgroup v2); 0 when unavailable.
func oomKills(unit string) int {
	if unit == "" {
		return 0
	}
	paths := []string{"/sys/fs/cgroup/system.slice/" + unit}
	if out, err := sys.Run(5*time.Second, nil, "systemctl", "show", "-p", "ControlGroup", "--value", unit); err == nil && strings.TrimSpace(out) != "" {
		paths = append([]string{"/sys/fs/cgroup" + strings.TrimSpace(out)}, paths...)
	}
	for _, base := range paths {
		b, err := os.ReadFile(base + "/memory.events")
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Fields(l); len(f) == 2 && f[0] == "oom_kill" {
				n, _ := strconv.Atoi(f[1])
				return n
			}
		}
	}
	return 0
}
