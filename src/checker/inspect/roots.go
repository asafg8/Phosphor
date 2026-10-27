package inspect

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// PHPExtensions are the file extensions that hold PHP code: the scan's phpcs --extensions, the usage walk and the
// root size count all use this one list. Drupal 7 keeps PHP in .install/.theme/.engine/.profile/.test.
var PHPExtensions = []string{"php", "inc", "phtml", "module", "install", "theme", "engine", "profile", "test"}

var phpExt = func() map[string]bool {
	m := map[string]bool{}
	for _, e := range PHPExtensions {
		m["."+e] = true
	}
	return m
}()

// IsPHPFile reports whether p has one of PHPExtensions.
func IsPHPFile(p string) bool { return phpExt[filepath.Ext(p)] }

// maxWalk bounds root analysis on huge trees.
const maxWalk = 300000

// analyzeRoot labels a code root and its third-party sub-trees.
func analyzeRoot(path string, via c.CodeRootVia) []c.CodeRoot {
	root := c.CodeRoot{Path: path, Kind: "first-party", Via: via, Frameworks: []string{}, ThirdPartyPaths: []string{}}
	st, err := os.Stat(path)
	if err != nil {
		root.Readable = false
		return []c.CodeRoot{root}
	}
	if !st.IsDir() {
		root.Readable = true
		if phpFileOrShebang(path) {
			root.PHPFiles = 1
			root.Bytes = st.Size()
		}
		return []c.CodeRoot{root}
	}
	if f, err := os.Open(path); err == nil {
		_, err = f.Readdirnames(1)
		f.Close()
		root.Readable = err == nil || err.Error() == "EOF"
	}
	root.Frameworks = frameworks(path)
	tp := thirdPartyPaths(path)
	root.ThirdPartyPaths = tp
	n := 0
	sub := map[string]*c.CodeRoot{}
	for _, p := range tp {
		if !sys.IsDir(p) {
			continue
		}
		sub[p] = &c.CodeRoot{Path: p, Kind: "third-party", Via: via, Frameworks: []string{}, ThirdPartyPaths: []string{}, Readable: true}
	}
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		n++
		if n > maxWalk {
			return filepath.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		if !IsPHPFile(p) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		root.PHPFiles++
		root.Bytes += info.Size()
		if s := owningThirdParty(p, tp); s != "" && sub[s] != nil {
			sub[s].PHPFiles++
			sub[s].Bytes += info.Size()
		}
		return nil
	})
	out := []c.CodeRoot{root}
	for _, p := range tp {
		if sub[p] != nil {
			out = append(out, *sub[p])
		}
	}
	return out
}

func owningThirdParty(p string, tp []string) string {
	for _, t := range tp {
		if p == t || strings.HasPrefix(p, t+"/") {
			return t
		}
	}
	return ""
}

// IsThirdParty reports whether file lies in one of the third-party sub-trees.
func IsThirdParty(file string, tp []string) bool { return owningThirdParty(file, tp) != "" }

// thirdPartyPaths finds vendor/, WordPress core and wp-content/{plugins,themes}/*, and the shipped core of the other
// known frameworks (frameworks.go) under a root.
func thirdPartyPaths(root string) []string {
	var out []string
	for _, d := range []string{"wp-includes", "wp-admin"} {
		if sys.IsDir(filepath.Join(root, d)) {
			out = append(out, filepath.Join(root, d))
		}
	}
	// WordPress core files at the root are third-party too
	if sys.Exists(filepath.Join(root, "wp-includes", "version.php")) {
		ms, _ := filepath.Glob(filepath.Join(root, "wp-*.php"))
		out = append(out, ms...)
		if sys.Exists(filepath.Join(root, "xmlrpc.php")) {
			out = append(out, filepath.Join(root, "xmlrpc.php"))
		}
	}
	for _, g := range []string{"wp-content/plugins/*", "wp-content/themes/*", "wp-content/mu-plugins/*"} {
		ms, _ := filepath.Glob(filepath.Join(root, g))
		for _, m := range ms {
			if sys.IsDir(m) {
				out = append(out, m)
			}
		}
	}
	for _, p := range frameworkCore(root) {
		if owningThirdParty(p, out) == "" {
			out = append(out, p)
		}
	}
	// vendor/ and node_modules/ up to depth 4
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if strings.Count(rel, "/") >= 4 {
			return filepath.SkipDir
		}
		if owningThirdParty(p, out) != "" {
			return filepath.SkipDir // already third-party (libraries/vendor in Joomla, …)
		}
		if d.Name() == "vendor" || d.Name() == "node_modules" || d.Name() == "bower_components" {
			out = append(out, p)
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(out)
	// drop entries nested in another one (a framework dir inside a detected parent)
	var flat []string
	for _, p := range out {
		if owningThirdParty(p, flat) == "" {
			flat = append(flat, p)
		}
	}
	return flat
}

var shebangRe = regexp.MustCompile(`^#!\s*(\S+)(\s+(\S+))?`)
var phpNameRe = regexp.MustCompile(`^php[0-9.]*$`)

// shebangPHP returns the interpreter if file starts with a php shebang (#!/usr/bin/php or #!/usr/bin/env php).
func shebangPHP(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 128)
	n, _ := f.Read(buf)
	line := strings.SplitN(string(buf[:n]), "\n", 2)[0]
	m := shebangRe.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	interp := m[1]
	if filepath.Base(interp) == "env" && m[3] != "" {
		interp = m[3]
	}
	if phpNameRe.MatchString(filepath.Base(interp)) {
		return interp
	}
	return ""
}

func phpFileOrShebang(p string) bool { return IsPHPFile(p) || shebangPHP(p) != "" }
