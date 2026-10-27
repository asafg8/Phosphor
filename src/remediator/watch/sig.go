package watch

import (
	"regexp"
	"strings"
	"time"
)

// Line classification of PHP / fpm / web-server error log lines.
type LogEvent struct {
	Sig    string // level|masked message|file
	Fatal  bool   // PHP Fatal error / Uncaught
	Segv   bool   // child exited on signal 11|6, segmentation fault
	MaxChd bool   // server reached pm.max_children
	Exit   bool   // a worker exited (fpm "child N exited …", apache "child pid N exit signal")
	Sig9   bool   // the exit was SIGKILL (a memcap OOM kill when the unit's oom counter moved)
	// Regression is a known-regression signature (gh-24488 zend_mm_heap corrupted).
	Regression string
	T          time.Time // fpm/apache line timestamp when parseable
}

var (
	phpMsgRe  = regexp.MustCompile(`PHP (Fatal error|Parse error|Warning|Notice|Deprecated|Recoverable fatal error|Catchable fatal error|Core error|Compile error):\s+(.*)`)
	fileRe    = regexp.MustCompile(`\s+in\s+(/[^\s:"]+?)(?::(\d+)| on line (\d+))`)
	quotedRe  = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	hexRe     = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	digitsRe  = regexp.MustCompile(`\d+`)
	segvRe    = regexp.MustCompile(`exited on signal (11|6)|[Ss]egmentation fault|exit signal Segmentation|exit signal Aborted|signal 11 \(SIGSEGV|core dumped`)
	maxChildR = regexp.MustCompile(`reached pm\.max_children|reached MaxRequestWorkers`)
	exitRe    = regexp.MustCompile(`child \d+ exited (with code|on signal) (\d+)|child pid \d+ exit signal`)
	fpmTSRe   = regexp.MustCompile(`^\[(\d\d-\w{3}-\d{4} \d\d:\d\d:\d\d)`)
	regressRe = regexp.MustCompile(`zend_mm_heap corrupted`)
)

// Mask replaces digits, hex and quoted strings so the same error on another line/request has one signature.
func Mask(s string) string {
	s = quotedRe.ReplaceAllString(s, "'S'")
	s = hexRe.ReplaceAllString(s, "\x00")
	s = digitsRe.ReplaceAllString(s, "N")
	s = strings.ReplaceAll(s, "\x00", "0xH")
	return strings.Join(strings.Fields(s), " ")
}

// Classify parses one error-log line. Lines that are not PHP errors / crashes return a zero LogEvent.
func Classify(line string) LogEvent {
	var ev LogEvent
	if segvRe.MatchString(line) {
		ev.Segv = true
	}
	if maxChildR.MatchString(line) {
		ev.MaxChd = true
	}
	if m := exitRe.FindStringSubmatch(line); m != nil {
		ev.Exit = true
		ev.Sig9 = m[1] == "on signal" && m[2] == "9"
	}
	if regressRe.MatchString(line) {
		ev.Regression = "gh-24488"
	}
	if m := fpmTSRe.FindStringSubmatch(line); m != nil {
		ev.T, _ = time.ParseInLocation("02-Jan-2006 15:04:05", m[1], time.Local)
	}
	m := phpMsgRe.FindStringSubmatch(line)
	if m == nil {
		return ev
	}
	level := "PHP " + m[1]
	msg := m[2]
	// nginx wraps FastCGI stderr: cut at the stack trace / nginx suffix
	for _, cut := range []string{`\nStack trace`, "\\n", `" while reading`, `" while `, "Stack trace:"} {
		if i := strings.Index(msg, cut); i >= 0 {
			msg = msg[:i]
		}
	}
	file := ""
	if fm := fileRe.FindStringSubmatchIndex(msg); fm != nil {
		file = msg[fm[2]:fm[3]]
		msg = msg[:fm[0]]
	}
	ev.Sig = level + "|" + Mask(msg) + "|" + file
	ev.Fatal = level == "PHP Fatal error" || strings.Contains(msg, "Uncaught") || level == "PHP Parse error"
	return ev
}
