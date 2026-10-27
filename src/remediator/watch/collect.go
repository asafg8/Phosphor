package watch

import (
	"math"
	"regexp"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// Collector gathers the per-sample signal counts the watch decision reads: it never decides.
//   - worker_exits_137: exits with code 137 or SIGKILL that the memcap OOM counter does not explain (a signal
//     distinct from oom-kill, never a trigger alone);
//   - Apache child churn (mod_php): children that disappeared since the last look, minus the recycling that
//     MaxConnectionsPerChild explains for the requests served; Apache logs nothing when a child dies of a
//     PHP crash it survives, so `ps` is the only source.
type Collector struct {
	apache   bool
	maxConn  int
	every    time.Duration
	lastLook time.Time
	children map[int]bool
	reqSince int
	kills    int // code-137 / SIGKILL exits seen in the log since the last Tick
}

// churnEvery is how often Apache's children are listed (every 15 s).
const churnEvery = 15 * time.Second

var exit137Re = regexp.MustCompile(`exited (?:with code 137|on signal 9)`)

// NewCollector prepares the collectors a runtime needs: Apache churn only for mod_php, net of maxConnPerChild
// (inspect's service.max_connections_per_child; 0 = children never recycle).
func NewCollector(sapi string, maxConnPerChild int) *Collector {
	col := &Collector{apache: sapi == "mod_php", maxConn: maxConnPerChild, every: churnEvery}
	if col.apache {
		col.children = workerSet()
		col.lastLook = time.Now()
	}
	return col
}

// Line takes one error-log line the engine has classified.
func (col *Collector) Line(l string, ev LogEvent) {
	if ev.Exit && exit137Re.MatchString(l) {
		col.kills++
	}
}

// Tick completes one sample: call it after the sample's req and oom_kills are known, before it is appended.
func (col *Collector) Tick(s *c.Sample) {
	s.WorkerExits137 = max(col.kills-s.OOMKills, 0)
	col.kills = 0
	if !col.apache {
		return
	}
	col.reqSince += s.Req
	if time.Since(col.lastLook) < col.every {
		return
	}
	now := workerSet()
	gone := 0
	for pid := range col.children {
		if !now[pid] {
			gone++
		}
	}
	churn := max(gone-ExpectedRecycles(col.reqSince, col.maxConn), 0)
	s.WorkerExits += churn
	col.children, col.lastLook, col.reqSince = now, time.Now(), 0
}

// ExpectedRecycles is how many Apache children MaxConnectionsPerChild retires for req requests (0 = never).
// Rounded up: churn is only counted beyond what the configuration explains.
func ExpectedRecycles(req, maxConn int) int {
	if maxConn <= 0 || req <= 0 {
		return 0
	}
	return int(math.Ceil(float64(req) / float64(maxConn)))
}

// ApacheWorkers are the pids of Apache workers (an apache2/httpd process whose parent is one too), in process order.
func ApacheWorkers() []int {
	procs := sys.Procs()
	isApache := map[int]bool{}
	for _, p := range procs {
		if p.Comm == "apache2" || p.Comm == "httpd" {
			isApache[p.PID] = true
		}
	}
	var out []int
	for _, p := range procs {
		if isApache[p.PID] && isApache[p.PPID] {
			out = append(out, p.PID)
		}
	}
	return out
}

func workerSet() map[int]bool {
	out := map[int]bool{}
	for _, pid := range ApacheWorkers() {
		out[pid] = true
	}
	return out
}
