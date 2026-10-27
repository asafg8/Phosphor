// Package scan reads the code a runtime serves. The `scan` stage is a Go pass over the code roots: feature-use
// evidence (uploads, session upload progress and the other packs) and the runtime-risk pack. The `readiness` stage
// (readiness.go) runs phpcs + PHPCompatibility from the pinned bundle under CPU/memory limits, with a
// first/third-party split.
package scan

import (
	"archive/tar"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/asafg8/Phosphor/src/checker/inspect"
	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

func init() { stage.Register("scan", Run) }

// Root is one code root to scan, as passed by verify in args.roots.
type Root struct {
	Path            string   `json:"path"`
	Kind            string   `json:"kind"`
	ThirdPartyPaths []string `json:"third_party_paths"`
	Slug            string   `json:"slug"`
}

// Run is the always-on scan step: the Go-side feature-use pack and the runtime-risk pack over every PHP file
// of the roots. No PHPCompatibility here: that is the opt-in `readiness` subcommand.
func Run(x *rt.Ctx) int {
	doc := newDoc(x, c.SchemaScan)
	var roots []Root
	x.ArgInto("roots", &roots)
	x.Step("scan", map[string]any{"roots": len(roots)})
	uploads, progress := &Feature{}, &Feature{}
	extra := map[string]*Feature{}
	for _, f := range extraFeatures {
		extra[f.setting] = &Feature{}
	}
	// some code was not read: usage evidence is missing, so never "unused"
	incomplete := func() {
		uploads.Incomplete, progress.Incomplete = true, true
		for _, f := range extra {
			f.Incomplete = true
		}
	}
	rootErr := false
	for _, r := range roots {
		sr := c.ScanRoot{Path: r.Path, Kind: c.CodeRootKind(r.Kind)}
		if !readable(r.Path) {
			sr.Error = c.Errf(c.ERootUnreadable, "path", r.Path)
			rootErr = true
			incomplete()
			doc.Roots = append(doc.Roots, sr)
			continue
		}
		t0 := time.Now()
		_ = filepath.WalkDir(r.Path, func(p string, d fs.DirEntry, err error) error {
			if err != nil { // unreadable subtree
				incomplete()
				return nil
			}
			if d.IsDir() || !inspect.IsPHPFile(p) {
				return nil
			}
			sr.Files++
			ClassifyFile(p, uploads, progress)
			classifyExtra(p, extra)
			if r.Kind != "third-party" && !inspect.IsThirdParty(p, r.ThirdPartyPaths) && len(doc.RuntimeRisk) < 200 {
				doc.RuntimeRisk = append(doc.RuntimeRisk, riskFile(p)...)
			}
			return nil
		})
		sr.DurationMS = time.Since(t0).Milliseconds()
		doc.Roots = append(doc.Roots, sr)
	}
	if len(doc.RuntimeRisk) > 200 {
		doc.RuntimeRisk = doc.RuntimeRisk[:200]
	}
	if len(roots) == 0 {
		incomplete()
	}
	doc.UploadsUsage = uploads.Verdict()
	doc.UsesUploads = doc.UploadsUsage == "used"
	doc.UploadsEvidence = capN(uploads.Evidence(), 200)
	doc.FeatureUse = []c.ScanFeatureUse{uploads.Use("file_uploads", 200), progress.Use("session.upload_progress", 200)}
	for _, f := range extraFeatures {
		doc.FeatureUse = append(doc.FeatureUse, extra[f.setting].Use(f.setting, 200))
	}
	// no compat scan here: no verdict, never `clear`; `phosphor readiness` computes one
	doc.Readiness = "not-run"
	x.Step("scan-done", map[string]any{"uploads": string(doc.UploadsUsage), "upload_progress": string(progress.Verdict())})
	doc.Envelope.Finish(nil)
	_ = x.WriteDoc("scan.json", doc)
	return exitCode(rootErr)
}

// newDoc is an empty scan/readiness document (every list non-nil, so it serializes as []).
func newDoc(x *rt.Ctx, schema string) c.Scan {
	return c.Scan{Envelope: x.Envelope(schema), RuntimeID: x.Arg("runtime_id"), TargetVersion: x.Arg("target_version"),
		Roots: []c.ScanRoot{}, BySniff: []c.SniffCount{}, TopFindings: []c.Finding{}, RuntimeRisk: []c.RiskHit{},
		UploadsEvidence: []c.FileLine{}, FeatureUse: []c.ScanFeatureUse{}, BlockingReasons: []string{}}
}

// exitCode is 2 when a root could not be scanned (partial result), else 0.
func exitCode(rootErr bool) int {
	if rootErr {
		return 2
	}
	return 0
}

// testVersion is the PHPCompatibility testVersion of a migration-readiness scan: the target minor, open-ended
// ("8.2" or "8.2.29" -> "8.2-"), so everything removed or changed from the target onwards is reported.
func testVersion(target string) string {
	if strings.HasSuffix(target, "-") {
		return target
	}
	parts := strings.SplitN(target, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + strings.SplitN(parts[1], "-", 2)[0] + "-"
	}
	return target + "-"
}

// readable: the root exists and its first entry can be listed.
func readable(p string) bool {
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	if st.Mode().Perm() == 0 {
		return false // chmod 000: root could read it, the service user can't; treat as missing evidence
	}
	if st.IsDir() {
		f, err := os.Open(p)
		if err != nil {
			return false
		}
		defer f.Close()
		_, err = f.Readdirnames(1)
		return err == nil || err == io.EOF
	}
	return true
}

func capN[T any](xs []T, n int) []T {
	if n < 0 {
		n = 0
	}
	if len(xs) > n {
		return xs[:n]
	}
	if xs == nil {
		return []T{}
	}
	return xs
}

func sortFindings(f []c.Finding) {
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Type != f[j].Type {
			return f[i].Type == "ERROR"
		}
		if f[i].File != f[j].File {
			return f[i].File < f[j].File
		}
		return f[i].Line < f[j].Line
	})
}

type phpcsReport struct {
	Totals struct {
		Errors   int `json:"errors"`
		Warnings int `json:"warnings"`
	} `json:"totals"`
	Files map[string]phpcsFile `json:"files"`
}

type phpcsFile struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Messages []struct {
		Message string `json:"message"`
		Source  string `json:"source"`
		Type    string `json:"type"`
		Line    int    `json:"line"`
	} `json:"messages"`
}

// limiter decides how phpcs is confined: a systemd-run scope (CPUQuota quota%, MemoryMax 1G) when systemd is PID 1,
// else nice/ionice.
func limiter(noSystemd bool, quota int) (c.ResourceLimits, []string) {
	nice := []string{}
	if p := sys.Which("nice"); p != "" {
		nice = append(nice, p, "-n", "19")
	}
	if p := sys.Which("ionice"); p != "" {
		nice = append(nice, p, "-c", "3")
	}
	comm := strings.TrimSpace(sys.ReadString("/proc/1/comm"))
	if sr := sys.Which("systemd-run"); !noSystemd && comm == "systemd" && sr != "" {
		q := strconv.Itoa(quota) + "%"
		scope := []string{"--scope", "--quiet", "-p", "CPUQuota=" + q, "-p", "MemoryMax=1G"}
		if _, code := sys.Combined(15*time.Second, nil, "systemd-run", append(scope, "true")...); code == 0 {
			return c.ResourceLimits{CPUQuota: q, MemoryMax: "1G", Nice: 19, IONice: "idle", Via: "systemd-run"},
				append(append([]string{sr}, scope...), nice...)
		}
	}
	return c.ResourceLimits{Nice: 19, IONice: "idle", Via: "nice-ionice"}, nice
}

// scannerIni is the whole ini of the scanner's PHP (it runs with -n): errors to stderr so nothing precedes the JSON.
var scannerIni = []string{"memory_limit=1G", "display_errors=stderr", "display_startup_errors=0", "log_errors=0",
	"error_reporting=E_ALL&~E_DEPRECATED", "date.timezone=UTC", "short_open_tag=1"}

// parseReport finds phpcs's JSON report in stdout, skipping any non-JSON prefix (a stray warning line).
func parseReport(out string) ([]byte, phpcsReport, bool) {
	var rep phpcsReport
	out = strings.TrimSpace(out)
	i := strings.Index(out, `{"totals"`)
	if i < 0 {
		return nil, rep, false
	}
	b := []byte(out[i:])
	if json.Unmarshal(b, &rep) != nil {
		return nil, rep, false
	}
	return b, rep, true
}

// extract unpacks the zstd tar bundle in-agent (the host needs no zstd tool).
func extract(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p := filepath.Join(dst, filepath.Clean("/"+h.Name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)|0o400)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		out.Close()
	}
}
