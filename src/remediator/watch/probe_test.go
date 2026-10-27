package watch

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Probe budget, round and trigger math, saturation veto, held precedence.
func TestU24_ProbeRetriesAndBodyFatal(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flaky": // fails twice, then answers: retries rescue it
			if atomic.AddInt32(&hits, 1) <= 2 {
				w.WriteHeader(500)
				return
			}
			w.Write([]byte("ok"))
		case "/fatal":
			w.Write([]byte("<b>Fatal error</b>: Uncaught Error: Call to undefined function imagecreatetruecolor()"))
		default:
			w.Write([]byte("ok"))
		}
	}))
	defer srv.Close()
	var slept []time.Duration
	p := NewProber()
	p.Sleep = func(d time.Duration) { slept = append(slept, d) }
	if r := p.One(srv.URL + "/flaky"); r.Failed() || r.Attempts != 3 {
		t.Fatalf("flaky: %+v", r)
	}
	if len(slept) != 2 || slept[0] != time.Second || slept[1] != 2*time.Second {
		t.Fatalf("backoff %v", slept)
	}
	if r := p.One(srv.URL + "/fatal"); !r.Failed() || !r.BodyFatal || !r.Answered() {
		t.Fatalf("fatal: %+v", r)
	}
	p.BaseFatal = map[string]bool{srv.URL + "/fatal": true}
	if r := p.One(srv.URL + "/fatal"); r.Failed() {
		t.Fatalf("baseline-fatal URL must not count: %+v", r)
	}
	if ProbeTimeout != 5*time.Second || ProbeRetries != 2 || ProbeMaxURLs != 5 || ProbeRoundEach != 30*time.Second {
		t.Fatal("budget constants changed")
	}
}

func TestU24_RoundWitnessSaturation(t *testing.T) {
	now := time.Now()
	ok := ProbeResult{Status: 200}
	bad := ProbeResult{Status: 500}
	to := ProbeResult{Timeout: true}
	if r := Judge(now, []ProbeResult{ok, ok, ok, bad}, false); !r.OK {
		t.Fatal("25% failed must pass")
	}
	if r := Judge(now, []ProbeResult{ok, ok, bad, bad}, false); r.OK {
		t.Fatal("50% failed must fail")
	}
	// saturation: timeouts never count, answered failures do
	if r := Judge(now, []ProbeResult{to, to, to, ok}, true); !r.OK || r.Counted {
		t.Fatalf("saturation timeouts: %+v", r)
	}
	if r := Judge(now, []ProbeResult{bad, bad, to, ok}, true); r.OK {
		t.Fatal("answered failures count under saturation")
	}
	R := func(oks ...bool) []c.ProbeRound {
		var out []c.ProbeRound
		for _, o := range oks {
			out = append(out, c.ProbeRound{OK: o})
		}
		return out
	}
	for _, tc := range []struct {
		r    []c.ProbeRound
		want bool
	}{{R(false), false}, {R(true, false), false}, {R(false, false), true}, {R(false, true, false), true},
		{R(false, true, true, false), false}, {R(true, false, true, false), true}} {
		if got := Witness(tc.r); got != tc.want {
			t.Errorf("Witness(%v)=%v", tc.r, got)
		}
	}
}

func TestU24_ExitRateAndHeldPrecedence(t *testing.T) {
	if ExitRateThreshold(0) != 2 || ExitRateThreshold(2) != 6 {
		t.Fatal("threshold")
	}
	if ExitRateBreach([]int{3, 3}, 0) || !ExitRateBreach([]int{0, 3, 3, 3}, 0) || ExitRateBreach([]int{3, 2, 3}, 0) {
		t.Fatal("3 consecutive minutes over threshold")
	}
	all := Evidence{Attack: true, Saturation: true, General: true, ExitRate: true}
	if HeldReason(all) != "under-attack" {
		t.Fatal("attack first")
	}
	all.Attack = false
	if HeldReason(all) != "saturation" {
		t.Fatal("saturation second")
	}
	all.Saturation = false
	if HeldReason(all) != "general-traffic-signal" {
		t.Fatal("general third")
	}
	if HeldReason(Evidence{ExitRate: true}) != "exit-rate" {
		t.Fatal("exit-rate last")
	}
}

func TestProbeURLsDenylistAndOrder(t *testing.T) {
	acc := []Access{}
	add := func(m, p string, st, n int) {
		for i := 0; i < n; i++ {
			acc = append(acc, Access{Method: m, Path: p, Status: st})
		}
	}
	add("GET", "/", 200, 50)
	add("GET", "/blog/", 200, 40)
	add("GET", "/wp-login.php", 200, 100)
	add("GET", "/cart/?action=add", 200, 100)
	add("POST", "/form", 200, 100)
	add("GET", "/missing", 404, 100)
	add("GET", "/_phosphor_lab/gd", 200, 30)
	got := ProbeURLs(acc, "http://127.0.0.1/_phosphor_lab/health", 5)
	want := []string{"http://127.0.0.1/", "http://127.0.0.1/blog/", "http://127.0.0.1/_phosphor_lab/gd"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
}

func TestParseAccessRequestLength(t *testing.T) {
	a, ok := ParseAccess(`10.0.0.1 - - [24/Oct/2026:20:42:50 +0000] "POST /upload HTTP/1.1" 502 157 "-" "curl/8" rl=5242880 ct="multipart/form-data; boundary=x"`)
	if !ok || a.Method != "POST" || a.Path != "/upload" || a.Status != 502 || a.ReqLen != 5242880 || !a.Multipart {
		t.Fatalf("%+v", a)
	}
	if ev := Classify(`[24-Oct-2026 20:42:50] WARNING: [pool www] child 1234 exited on signal 9 (SIGKILL) after 12.3 seconds from start`); !ev.Exit || !ev.Sig9 || ev.T.IsZero() {
		t.Fatalf("%+v", ev)
	}
	if ev := Classify(`[24-Oct-2026 20:42:51] ERROR: zend_mm_heap corrupted`); ev.Regression != "gh-24488" {
		t.Fatalf("%+v", ev)
	}
}
