// loadgen sends steady traffic to every lab fixture so watch baselines are real: default 5 rps per
// fixture over its URL list, /_phosphor_lab/legacy-fatal once a minute where configured, never the health probe.
// Control API (used by `lab load`): GET /state, POST /fx/<fx>?state=on|off&rps=N&add=<path>&del=<path>&post=<path>&urls=<p1,p2,…>.
// Flood API (used by `lab flood`): POST /flood/<fx>?kind=multipart|get&path=<p>&mb=N&rate=R&conc=C&dur=S,
// POST /flood/<fx>?stop=1, GET /flood/<fx> (stats). Flood requests carry User-Agent phosphor-lab-flood.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Target struct {
	Name        string   `json:"name"`
	Base        string   `json:"base"`
	URLs        []string `json:"urls"`
	LegacyFatal bool     `json:"legacy_fatal"`
	On          bool     `json:"on"`
	RPS         float64  `json:"rps"`
	Post        []string `json:"post"` // multipart POST targets (upload tests)
	Sent        int64    `json:"sent"`
	Errors      int64    `json:"errors"`
	Flood       *Flood   `json:"flood,omitempty"`
	stop        chan struct{}
}

var (
	mu      sync.Mutex
	targets = map[string]*Target{}
	client  = &http.Client{Timeout: 10 * time.Second}
)

func main() {
	cfg := os.Getenv("LOADGEN_CONFIG")
	if cfg == "" {
		cfg = "/etc/loadgen/targets.json"
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		log.Fatal(err)
	}
	var list []*Target
	if err := json.Unmarshal(b, &list); err != nil {
		log.Fatal(err)
	}
	for _, t := range list {
		if t.RPS == 0 {
			t.RPS = 5
		}
		targets[t.Name] = t
		if t.On {
			start(t)
		}
	}
	http.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(targets)
	})
	http.HandleFunc("/fx/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/fx/")
		mu.Lock()
		t, ok := targets[name]
		mu.Unlock()
		if !ok {
			http.Error(w, "unknown fixture", 404)
			return
		}
		q := r.URL.Query()
		mu.Lock()
		if v := q.Get("rps"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				t.RPS = f
			}
		}
		if v := q.Get("add"); v != "" {
			t.URLs = append(t.URLs, v)
		}
		if v := q.Get("del"); v != "" {
			var keep []string
			for _, u := range t.URLs {
				if u != v {
					keep = append(keep, u)
				}
			}
			t.URLs = keep
		}
		if q.Has("urls") {
			t.URLs = nil
			for _, u := range strings.Split(q.Get("urls"), ",") {
				if u = strings.TrimSpace(u); u != "" {
					t.URLs = append(t.URLs, u)
				}
			}
		}
		if v := q.Get("post"); v != "" {
			t.Post = append(t.Post, v)
		}
		if q.Has("nopost") {
			t.Post = nil
		}
		restart := q.Get("rps") != ""
		mu.Unlock()
		switch q.Get("state") {
		case "on":
			stopT(t)
			start(t)
		case "off":
			stopT(t)
		default:
			if restart && t.On {
				stopT(t)
				start(t)
			}
		}
		fmt.Fprintln(w, "ok")
	})
	http.HandleFunc("/flood/", floodHandler)
	log.Fatal(http.ListenAndServe(":8099", nil))
}

func stopT(t *Target) {
	mu.Lock()
	defer mu.Unlock()
	if t.stop != nil {
		close(t.stop)
		t.stop = nil
	}
	t.On = false
}

func start(t *Target) {
	mu.Lock()
	t.On = true
	stop := make(chan struct{})
	t.stop = stop
	rps := t.RPS
	mu.Unlock()
	go func() {
		tick := time.NewTicker(time.Duration(float64(time.Second) / rps))
		fatal := time.NewTicker(time.Minute)
		defer tick.Stop()
		defer fatal.Stop()
		i := 0
		for {
			select {
			case <-stop:
				return
			case <-fatal.C:
				if t.LegacyFatal {
					go hit(t, "GET", "/_phosphor_lab/legacy-fatal")
				}
			case <-tick.C:
				mu.Lock()
				urls := append([]string(nil), t.URLs...)
				posts := append([]string(nil), t.Post...)
				mu.Unlock()
				all := len(urls) + len(posts)
				if all == 0 {
					continue
				}
				k := i % all
				i++
				if k < len(urls) {
					go hit(t, "GET", urls[k])
				} else {
					go hit(t, "POST", posts[k-len(urls)])
				}
			}
		}
	}()
}

func hit(t *Target, method, path string) {
	var req *http.Request
	if method == "POST" {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("f", "phosphor.txt")
		fw.Write([]byte("phosphor lab upload\n"))
		mw.Close()
		req, _ = http.NewRequest("POST", t.Base+path, &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
	} else {
		req, _ = http.NewRequest("GET", t.Base+path, nil)
	}
	req.Header.Set("User-Agent", "phosphor-lab-loadgen")
	resp, err := client.Do(req)
	mu.Lock()
	defer mu.Unlock()
	t.Sent++
	if err != nil {
		t.Errors++
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// Flood is one running (or finished) flood against a target.
type Flood struct {
	Kind     string           `json:"kind"`
	Path     string           `json:"path"`
	MB       float64          `json:"mb"`
	Rate     float64          `json:"rate"`
	Conc     int              `json:"conc"`
	Dur      int              `json:"dur"`
	Running  bool             `json:"running"`
	Sent     int64            `json:"sent"`
	Skipped  int64            `json:"skipped"`  // ticks dropped because conc requests were in flight
	InFlight int64            `json:"inflight"` // requests sent and not answered yet
	Codes    map[string]int64 `json:"codes"`    // status code, "err" or "cancelled" (flood ended first) → count
	stop     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
}

var floodClient = &http.Client{Timeout: 120 * time.Second}

func floodHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/flood/")
	mu.Lock()
	t, ok := targets[name]
	mu.Unlock()
	if !ok {
		http.Error(w, "unknown fixture", 404)
		return
	}
	q := r.URL.Query()
	if r.Method == "GET" {
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(t.Flood)
		return
	}
	mu.Lock()
	if t.Flood != nil && t.Flood.Running {
		close(t.Flood.stop)
		t.Flood.Running = false
	}
	mu.Unlock()
	if q.Has("stop") {
		fmt.Fprintln(w, "stopped")
		return
	}
	f := &Flood{Kind: q.Get("kind"), Path: q.Get("path"), MB: 8, Rate: 20, Conc: 50, Dur: 60, Running: true,
		Codes: map[string]int64{}, stop: make(chan struct{})}
	if f.Kind == "" {
		f.Kind = "multipart"
	}
	if f.Path == "" {
		f.Path = "/_phosphor_lab/upload"
	}
	if v, err := strconv.ParseFloat(q.Get("mb"), 64); err == nil && v >= 0 {
		f.MB = v
	}
	if v, err := strconv.ParseFloat(q.Get("rate"), 64); err == nil && v > 0 {
		f.Rate = v
	}
	if v, err := strconv.Atoi(q.Get("conc")); err == nil && v > 0 {
		f.Conc = v
	}
	if v, err := strconv.Atoi(q.Get("dur")); err == nil && v > 0 {
		f.Dur = v
	}
	var body []byte
	var ctype string
	if f.Kind == "multipart" {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		fw, _ := mw.CreateFormFile("f", "flood.bin")
		fw.Write(bytes.Repeat([]byte("A"), int(f.MB*1024*1024)))
		mw.Close()
		body, ctype = b.Bytes(), mw.FormDataContentType()
	}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	mu.Lock()
	t.Flood = f
	mu.Unlock()
	go runFlood(t, f, body, ctype)
	fmt.Fprintln(w, "ok")
}

func runFlood(t *Target, f *Flood, body []byte, ctype string) {
	sem := make(chan struct{}, f.Conc)
	tick := time.NewTicker(time.Duration(float64(time.Second) / f.Rate))
	end := time.After(time.Duration(f.Dur) * time.Second)
	defer tick.Stop()
	defer f.cancel() // a stopped/finished flood aborts its in-flight requests
	for {
		select {
		case <-f.stop:
			return
		case <-end:
			mu.Lock()
			f.Running = false
			mu.Unlock()
			return
		case <-tick.C:
			select {
			case sem <- struct{}{}:
			default:
				mu.Lock()
				f.Skipped++
				mu.Unlock()
				continue
			}
			mu.Lock()
			f.InFlight++
			mu.Unlock()
			go func() {
				defer func() { <-sem }()
				var req *http.Request
				if f.Kind == "multipart" {
					req, _ = http.NewRequest("POST", t.Base+f.Path, bytes.NewReader(body))
					req.Header.Set("Content-Type", ctype)
				} else {
					req, _ = http.NewRequest("GET", t.Base+f.Path, nil)
				}
				req = req.WithContext(f.ctx)
				req.Header.Set("User-Agent", "phosphor-lab-flood")
				code := "err"
				resp, err := floodClient.Do(req)
				if err != nil && f.ctx.Err() != nil {
					code = "cancelled"
				}
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					code = strconv.Itoa(resp.StatusCode)
				}
				mu.Lock()
				f.Sent++
				f.InFlight--
				f.Codes[code]++
				mu.Unlock()
			}()
		}
	}
}
