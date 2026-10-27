package scan

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/asafg8/Phosphor/src/checker/inspect"
	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

func init() { stage.Register("readiness", RunReadiness) }

const (
	defaultBudget = 600 * time.Second // per runtime
	batchFiles    = 200               // files per phpcs process: bounds its in-memory report and the work lost to a cut
	firstPartyMax = 30 * time.Minute  // first-party is never cut by the budget; this only stops a hung phpcs
)

// rootWork is one root's first-party and third-party PHP files and its merged phpcs report.
type rootWork struct {
	r            Root
	sr           *c.ScanRoot
	first, third []string
	rep          phpcsReport
}

// RunReadiness is the opt-in migration-readiness scan: PHPCompatibility at the migration
// testVersion over the roots, first-party files first and always in full, then third-party files while the
// per-runtime budget lasts. Files the budget cut are counted, never silently dropped (readiness: partial).
func RunReadiness(x *rt.Ctx) int {
	doc := newDoc(x, c.SchemaReadiness)
	doc.UploadsUsage = "unknown" // readiness does not classify uploads; "" fails the schema
	var roots []Root
	x.ArgInto("roots", &roots)
	budget := defaultBudget
	if s, err := strconv.Atoi(x.Arg("budget_s")); err == nil && s > 0 {
		budget = time.Duration(s) * time.Second
	}
	firstOnly := x.ArgBool("first_party_only")
	dir := filepath.Join(rt.Root, "readiness")
	finish := func(err *c.Error, code int) int {
		doc.Envelope.Finish(err)
		_ = x.WriteDoc("readiness.json", doc)
		_ = os.RemoveAll(dir)
		return code
	}
	if doc.TargetVersion == "" {
		// 8.2+ has no migration target; never guess one here
		return finish(c.Errf(c.EScanFailed, "step", "no-target-version"), 1)
	}
	start := time.Now()
	if e := unpackBundle(x, dir); e != nil {
		return finish(e, 1)
	}
	var meta map[string]string
	if b, err := os.ReadFile(filepath.Join(dir, "bundle.json")); err == nil {
		_ = json.Unmarshal(b, &meta)
	}
	doc.Tool = c.ScanTools{PHP: meta["php"], PHPCS: meta["phpcs"], PHPCompatRef: meta["phpcompat_ref"], PHPCSUtils: meta["phpcsutils"]}
	limits, wrap := limiter(x.ArgBool("lab_no_systemd_run"))
	quota := 0.0
	if limits.Via == "systemd-run" {
		quota = 0.5 // our own CPUQuota=50% scope
	}
	par, memLimit := workers(quota)
	doc.Limits = &limits
	x.Step("readiness", map[string]any{"limits": limits, "roots": len(roots), "parallel": par, "memory_limit": memLimit,
		"budget_s": int(budget.Seconds()), "first_party_only": firstOnly})

	work, rootErr := collectWork(roots)
	tv := testVersion(doc.TargetVersion)
	run := func(w *rootWork, files []string, timeout time.Duration) bool {
		rep, err := phpcsFiles(dir, wrap, files, tv, par, memLimit, timeout)
		if err != nil {
			return false
		}
		w.rep.merge(rep)
		w.sr.Files += len(files)
		return true
	}
	// first-party: always in full
	for _, w := range work {
		t0 := time.Now()
		for _, b := range batches(w.first) {
			if !run(w, b, firstPartyMax) {
				w.sr.Error = c.Errf(c.EScanFailed, "path", w.r.Path)
				rootErr = true
				break
			}
		}
		w.sr.DurationMS += time.Since(t0).Milliseconds()
	}
	// third-party: while the per-runtime budget lasts
	deadline := start.Add(budget)
	for _, w := range work {
		t0 := time.Now()
		for _, b := range batches(w.third) {
			left := time.Until(deadline)
			if firstOnly || left <= 0 || w.sr.Error != nil {
				// --first-party-only, or the budget is spent: counted, so the run reads partial (N of M files)
				w.sr.Truncated = w.sr.Error == nil
				doc.FilesNotScanned += len(b)
				continue
			}
			if !run(w, b, left) { // the budget ran out mid-batch (or phpcs failed on it): those files are not scanned
				w.sr.Truncated = true
				doc.FilesNotScanned += len(b)
			}
		}
		w.sr.DurationMS += time.Since(t0).Milliseconds()
	}
	first := tally(x, &doc, work)
	doc.Readiness = "complete"
	if doc.FilesNotScanned > 0 {
		doc.Readiness = "partial"
	}
	// decision: first-party ERROR → blocking; third-party-only errors, a cut, or missing evidence → review
	seen := map[string]bool{}
	for _, f := range first {
		if f.Type == "ERROR" && !seen[f.Source] {
			seen[f.Source] = true
			doc.BlockingReasons = append(doc.BlockingReasons, f.Source)
		}
	}
	sort.Strings(doc.BlockingReasons)
	switch {
	case len(doc.BlockingReasons) > 0:
		doc.Decision = c.VerdictBlocking
	case doc.Counts.ThirdParty.Errors > 0 || rootErr || len(roots) == 0 || doc.FilesNotScanned > 0:
		doc.Decision = c.VerdictReview
	default:
		doc.Decision = c.VerdictClear
	}
	x.Step("readiness-done", map[string]any{"decision": string(doc.Decision), "readiness": doc.Readiness,
		"files_not_scanned": doc.FilesNotScanned, "first_party_errors": doc.Counts.FirstParty.Errors,
		"third_party_errors": doc.Counts.ThirdParty.Errors})
	return finish(nil, exitCode(rootErr))
}

// unpackBundle fetches the scan bundle (GET "scanbundle", sha256-checked) and extracts it into dir.
func unpackBundle(x *rt.Ctx, dir string) *c.Error {
	tarball := filepath.Join(dir, "bundle.tar.zst")
	if _, err := x.Fetch("scanbundle", tarball, x.Arg("scanbundle_sha256")); err != nil {
		if e, ok := err.(*c.Error); ok {
			return e
		}
		return c.Errf(c.EScanFailed, "step", "fetch-bundle")
	}
	if err := extract(tarball, dir); err != nil {
		return c.Errf(c.EScanFailed, "step", "extract")
	}
	_ = os.Remove(tarball)
	return nil
}

// collectWork splits each readable root's PHP files into first- and third-party.
func collectWork(roots []Root) ([]*rootWork, bool) {
	var work []*rootWork
	rootErr := false
	for _, r := range roots {
		w := &rootWork{r: r, sr: &c.ScanRoot{Path: r.Path, Kind: c.CodeRootKind(r.Kind), RawKey: "raw/readiness-" + r.Slug + ".json"}}
		work = append(work, w)
		if !readable(r.Path) {
			w.sr.Error = c.Errf(c.ERootUnreadable, "path", r.Path)
			rootErr = true
			continue
		}
		_ = filepath.WalkDir(r.Path, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !inspect.IsPHPFile(p) {
				return nil
			}
			if r.Kind == "third-party" || inspect.IsThirdParty(p, r.ThirdPartyPaths) {
				w.third = append(w.third, p)
			} else {
				w.first = append(w.first, p)
			}
			return nil
		})
	}
	return work, rootErr
}

// tally writes each root's raw report and folds the findings into doc (roots, counts, by_sniff, top_findings). It
// returns the first-party findings, sorted.
func tally(x *rt.Ctx, doc *c.Scan, work []*rootWork) []c.Finding {
	bySniff := map[string]*c.SniffCount{}
	var first, third []c.Finding
	for _, w := range work {
		if raw, err := json.Marshal(w.rep); err == nil && w.sr.Error == nil {
			_ = x.WriteFile(w.sr.RawKey, raw)
		}
		for file, fr := range w.rep.Files {
			tp := w.r.Kind == "third-party" || inspect.IsThirdParty(file, w.r.ThirdPartyPaths)
			cnt := &doc.Counts.FirstParty
			if tp {
				cnt = &doc.Counts.ThirdParty
			}
			for _, m := range fr.Messages {
				sev := c.Severity(m.Type)
				if sev == "ERROR" {
					w.sr.Errors++
					cnt.Errors++
				} else {
					w.sr.Warnings++
					cnt.Warnings++
				}
				k := m.Source + "|" + m.Type
				sc, ok := bySniff[k]
				if !ok {
					sc = &c.SniffCount{Source: m.Source, Type: sev, Sample: c.FileLine{File: file, Line: m.Line}}
					bySniff[k] = sc
				}
				sc.Count++
				f := c.Finding{File: file, Line: m.Line, Source: m.Source, Type: sev, FirstParty: !tp}
				if tp {
					third = append(third, f)
					continue
				}
				sc.FirstParty++
				if sc.FirstParty == 1 {
					sc.Sample = c.FileLine{File: file, Line: m.Line}
				}
				first = append(first, f)
			}
		}
		doc.Roots = append(doc.Roots, *w.sr)
	}
	for _, s := range bySniff {
		doc.BySniff = append(doc.BySniff, *s)
	}
	sort.Slice(doc.BySniff, func(i, j int) bool {
		a, b := doc.BySniff[i], doc.BySniff[j]
		if a.FirstParty != b.FirstParty {
			return a.FirstParty > b.FirstParty
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Source < b.Source
	})
	sortFindings(first)
	sortFindings(third)
	doc.TopFindings = append(doc.TopFindings, capN(first, 40)...)
	doc.TopFindings = append(doc.TopFindings, capN(third, 50-len(doc.TopFindings))...)
	return first
}

func batches(files []string) [][]string {
	var out [][]string
	for len(files) > 0 {
		n := min(batchFiles, len(files))
		out = append(out, files[:n])
		files = files[n:]
	}
	return out
}

// workers sizes phpcs from the CPU we may really use, not the host: parallel = cpus/2, 1…4, where cpus is the
// tightest of GOMAXPROCS, every cpu.max on our cgroup path up to the root (a systemd scope under a limited parent
// shows "max" on its own level) and our own systemd-run CPUQuota; one total memory cap = min(1 GiB, 25% of
// the available memory), split across the workers (each phpcs process gets cap/parallel).
func workers(quota float64) (int, string) {
	cpus := float64(runtime.GOMAXPROCS(0))
	if l := cgroupCPULimit(cgroupPaths()); l > 0 && l < cpus {
		cpus = l
	}
	if quota > 0 && quota < cpus {
		cpus = quota
	}
	par := min(max(int(cpus)/2, 1), 4)
	capMB := 1024
	if avail := memAvailableMB(); avail > 0 {
		capMB = min(capMB, avail/4)
	}
	per := max(capMB/par, 128)
	return par, strconv.Itoa(per) + "M"
}

// memAvailableMB is the smaller of /proc/meminfo MemAvailable and the cgroup v2 headroom (memory.max − memory.current).
func memAvailableMB() int {
	avail := 0
	for _, l := range strings.Split(sys.ReadString("/proc/meminfo"), "\n") {
		if v, ok := strings.CutPrefix(l, "MemAvailable:"); ok {
			f := strings.Fields(v)
			if len(f) > 0 {
				kb, _ := strconv.Atoi(f[0])
				avail = kb / 1024
			}
		}
	}
	// cgroup v2: the tightest memory.max − memory.current on the path up to the root
	for _, d := range cgroupPaths() {
		maxB, err1 := strconv.ParseInt(strings.TrimSpace(sys.ReadString(filepath.Join(d, "memory.max"))), 10, 64)
		curB, err2 := strconv.ParseInt(strings.TrimSpace(sys.ReadString(filepath.Join(d, "memory.current"))), 10, 64)
		if err1 == nil && err2 == nil && maxB > curB {
			if head := int((maxB - curB) >> 20); avail == 0 || head < avail {
				avail = head
			}
		}
	}
	return avail
}

// cgroupPaths are our cgroup v2 directory and each ancestor up to the mount root (/sys/fs/cgroup), innermost
// first. With cgroupns=private the path is "/" and only the root is read.
func cgroupPaths() []string {
	rel := ""
	for _, l := range strings.Split(sys.ReadString("/proc/self/cgroup"), "\n") {
		if p, ok := strings.CutPrefix(l, "0::"); ok {
			rel = strings.TrimSpace(p)
		}
	}
	var out []string
	for p := filepath.Clean("/" + rel); ; p = filepath.Dir(p) {
		d := filepath.Join("/sys/fs/cgroup", p)
		if sys.IsDir(d) {
			out = append(out, d)
		}
		if p == "/" {
			return out
		}
	}
}

// cgroupCPULimit is the tightest cpu.max ("quota period") on the given cgroup dirs, in CPUs; 0 = unlimited.
func cgroupCPULimit(dirs []string) float64 {
	best := 0.0
	for _, d := range dirs {
		f := strings.Fields(sys.ReadString(filepath.Join(d, "cpu.max")))
		if len(f) != 2 || f[0] == "max" {
			continue
		}
		q, err1 := strconv.ParseFloat(f[0], 64)
		per, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil || per <= 0 {
			continue
		}
		if l := q / per; best == 0 || l < best {
			best = l
		}
	}
	return best
}

// phpcsFiles runs one phpcs process over a file list (--file-list) and parses its JSON report.
func phpcsFiles(dir string, wrap, files []string, tv string, par int, memLimit string, timeout time.Duration) (phpcsReport, error) {
	var rep phpcsReport
	list, err := os.CreateTemp(dir, "files-*.txt")
	if err != nil {
		return rep, err
	}
	defer os.Remove(list.Name())
	_, _ = list.WriteString(strings.Join(files, "\n") + "\n")
	list.Close()
	ini := append([]string{}, scannerIni...)
	ini[0] = "memory_limit=" + memLimit
	args := append(append([]string{}, wrap...), filepath.Join(dir, "bin", "php"), "-n")
	for _, d := range ini {
		args = append(args, "-d", d)
	}
	args = append(args, filepath.Join(dir, "phpcs.phar"),
		"--runtime-set", "installed_paths", filepath.Join(dir, "standards", "phpcompatibility")+","+filepath.Join(dir, "standards", "phpcsutils"),
		"--standard=PHPCompatibility", "--runtime-set", "testVersion", tv,
		"--report=json", "-q", "--extensions="+strings.Join(inspect.PHPExtensions, ","), "--parallel="+strconv.Itoa(par),
		"--file-list="+list.Name())
	out, _, err := sys.RunFull(timeout, nil, args[0], args[1:]...)
	// phpcs exits non-zero whenever it has findings, so the exit code alone says nothing; the report is the judge.
	_, rep, ok := parseReport(out)
	if !ok {
		if err == nil {
			err = os.ErrInvalid
		}
		return rep, err
	}
	return rep, nil
}

func (r *phpcsReport) merge(o phpcsReport) {
	if r.Files == nil {
		r.Files = map[string]phpcsFile{}
	}
	r.Totals.Errors += o.Totals.Errors
	r.Totals.Warnings += o.Totals.Warnings
	for f, v := range o.Files {
		r.Files[f] = v
	}
}
