package scan

import (
	"bufio"
	"os"
	"regexp"

	c "github.com/asafg8/Phosphor/src/contract"
)

// pattern is one feature-use grep rule (feature-use pack); kind is recorded as evidence.kind.
type pattern struct {
	kind string
	re   *regexp.Regexp
}

var (
	uploadsUsed = []pattern{
		{"$_FILES", regexp.MustCompile(`\$_FILES\b`)},
		{"move_uploaded_file", regexp.MustCompile(`\bmove_uploaded_file\s*\(`)},
		{"is_uploaded_file", regexp.MustCompile(`\bis_uploaded_file\s*\(`)},
		{"multipart-form", regexp.MustCompile(`(?i)multipart/form-data`)},
		{"wp_handle_upload", regexp.MustCompile(`\b(wp_handle_upload|media_handle_upload)\s*\(`)},
	}
	dynFiles = regexp.MustCompile(`\$\{\s*['"]_FI['"]\s*\.|\$GLOBALS\s*\[\s*\$|\$\$[A-Za-z_]`)
	// session upload progress only: the ini names, or the default session key prefix as a string literal.
	// Not APC_UPLOAD_PROGRESS / PECL uploadprogress / a `$upload_progress_key` variable (Drupal 7 core).
	progressUsed = []pattern{
		{"session.upload_progress", regexp.MustCompile(`session\.upload_progress`)},
		{"upload_progress-key", regexp.MustCompile(`['"]upload_progress_`)},
	}
)

// Feature is one usage classifier (feeds the plan and the verify judge).
type Feature struct {
	Used    []c.FileLine
	Unknown []c.FileLine
	// Incomplete: some code was not read (unreadable file/subtree, unscanned root), so absence of hits proves nothing
	Incomplete bool
	kinds      map[c.FileLine]string
}

func (f *Feature) add(to *[]c.FileLine, fl c.FileLine, kind string) {
	*to = append(*to, fl)
	if f.kinds == nil {
		f.kinds = map[c.FileLine]string{}
	}
	if _, ok := f.kinds[fl]; !ok {
		f.kinds[fl] = kind
	}
}

// Use is the scan.json feature_use[] entry for setting (evidence as Evidence, capped at n).
func (f Feature) Use(setting string, n int) c.ScanFeatureUse {
	ev := []c.FeatureEvidence{}
	for _, e := range capN(f.Evidence(), n) {
		ev = append(ev, c.FeatureEvidence{File: e.File, Line: e.Line, Kind: f.kinds[e]})
	}
	return c.ScanFeatureUse{Setting: setting, Verdict: f.Verdict(), Evidence: ev}
}

// Verdict is used if any direct use, unknown if only dynamic access or the evidence is incomplete, unused otherwise.
func (f Feature) Verdict() c.Usage {
	switch {
	case len(f.Used) > 0:
		return "used"
	case len(f.Unknown) > 0 || f.Incomplete:
		return "unknown"
	}
	return "unused"
}

// Evidence is one file:line per file (first hit), so a big tree can't crowd out other files.
func (f Feature) Evidence() []c.FileLine {
	src := f.Unknown
	if len(f.Used) > 0 {
		src = f.Used
	}
	var out []c.FileLine
	seen := map[string]bool{}
	for _, e := range src {
		if !seen[e.File] {
			seen[e.File] = true
			out = append(out, e)
		}
	}
	return out
}

// ClassifyFile adds a file's evidence for the uploads ($_FILES) and upload-progress features.
func ClassifyFile(path string, uploads, progress *Feature) {
	ok := eachLine(path, func(n int, l string) {
		fl := c.FileLine{File: path, Line: n}
		if k := match(uploadsUsed, l); k != "" {
			uploads.add(&uploads.Used, fl, k)
		} else if dynFiles.MatchString(l) {
			uploads.add(&uploads.Unknown, fl, "dynamic")
		}
		if progress != nil {
			if k := match(progressUsed, l); k != "" {
				progress.add(&progress.Used, fl, k)
			}
		}
	})
	if !ok {
		uploads.Incomplete = true
		if progress != nil {
			progress.Incomplete = true
		}
	}
}

// eachLine calls fn for every line of path (1-based); false when the file cannot be opened.
func eachLine(path string, fn func(n int, l string)) bool {
	fh, err := os.Open(path)
	if err != nil {
		return false
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for n := 1; sc.Scan(); n++ {
		fn(n, sc.Text())
	}
	return true
}

func match(ps []pattern, l string) string {
	for _, p := range ps {
		if p.re.MatchString(l) {
			return p.kind
		}
	}
	return ""
}

// extraFeatures are the other feature-use evidence packs (known regressions, used extensions): file:line
// only, never a gate by themselves.
var extraFeatures = []struct {
	setting string
	pats    []pattern
}{
	// code paths exposed to the simplexml regression (php-src#24488)
	{"simplexml", []pattern{
		{"simplexml_load", regexp.MustCompile(`\bsimplexml_(load_string|load_file|import_dom)\s*\(`)},
		{"SimpleXMLElement", regexp.MustCompile(`\bnew\s+\\?SimpleXMLElement\b`)},
	}},
	// shutdown handlers (OOM / fatal-error reporting runs there)
	{"shutdown-handler", []pattern{{"register_shutdown_function", regexp.MustCompile(`\bregister_shutdown_function\s*\(`)}}},
}

// classifyExtra adds a file's evidence for every extra feature.
func classifyExtra(path string, fs map[string]*Feature) {
	ok := eachLine(path, func(n int, l string) {
		for _, ef := range extraFeatures {
			if k := match(ef.pats, l); k != "" {
				f := fs[ef.setting]
				f.add(&f.Used, c.FileLine{File: path, Line: n}, k)
			}
		}
	})
	if !ok {
		for _, f := range fs {
			f.Incomplete = true
		}
	}
}
