package scan

import (
	"regexp"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
)

// riskRules is the runtime-risk pack: behaviour changes static analysis can't prove. Always review,
// never blocking.
// lit is a substring every match contains (see pattern).
var riskRules = []struct {
	rule string
	re   *regexp.Regexp
	lit  string
}{
	{"register_globals", regexp.MustCompile(`register_globals`), "register_globals"},
	{"magic_quotes", regexp.MustCompile(`magic_quotes|get_magic_quotes_gpc`), "magic_quotes"},
	{"dynamic-call", regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*\s*\(`), "("},
	{"create_function", regexp.MustCompile(`\bcreate_function\s*\(`), "create_function"},
	{"extract-superglobal", regexp.MustCompile(`\bextract\s*\(\s*\$_(GET|POST|REQUEST|COOKIE)`), "extract"},
	{"preg-e-modifier", regexp.MustCompile(`preg_replace\s*\(\s*['"]/.*/[a-zA-Z]*e[a-zA-Z]*['"]`), "preg_replace"},
}

var shortTag = regexp.MustCompile(`<\?(\s|$)`)

func riskFile(path string) []c.RiskHit {
	var out []c.RiskHit
	eachLine(path, func(n int, l string) {
		if strings.Contains(l, "<?") && shortTag.MatchString(l) && !strings.Contains(l, "<?php") && !strings.Contains(l, "<?=") {
			out = append(out, c.RiskHit{Rule: "short_open_tag", File: path, Line: n, Kind: "review"})
		}
		for _, r := range riskRules {
			if strings.Contains(l, r.lit) && r.re.MatchString(l) {
				out = append(out, c.RiskHit{Rule: r.rule, File: path, Line: n, Kind: "review"})
			}
		}
	})
	return out
}
