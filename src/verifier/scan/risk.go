package scan

import (
	"regexp"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
)

// riskRules is the runtime-risk pack: behaviour changes static analysis can't prove. Always review,
// never blocking.
var riskRules = []struct {
	rule string
	re   *regexp.Regexp
}{
	{"register_globals", regexp.MustCompile(`register_globals`)},
	{"magic_quotes", regexp.MustCompile(`magic_quotes|get_magic_quotes_gpc`)},
	{"dynamic-call", regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*\s*\(`)},
	{"create_function", regexp.MustCompile(`\bcreate_function\s*\(`)},
	{"extract-superglobal", regexp.MustCompile(`\bextract\s*\(\s*\$_(GET|POST|REQUEST|COOKIE)`)},
	{"preg-e-modifier", regexp.MustCompile(`preg_replace\s*\(\s*['"]/.*/[a-zA-Z]*e[a-zA-Z]*['"]`)},
}

var shortTag = regexp.MustCompile(`<\?(\s|$)`)

func riskFile(path string) []c.RiskHit {
	var out []c.RiskHit
	eachLine(path, func(n int, l string) {
		if shortTag.MatchString(l) && !strings.Contains(l, "<?php") && !strings.Contains(l, "<?=") {
			out = append(out, c.RiskHit{Rule: "short_open_tag", File: path, Line: n, Kind: "review"})
		}
		for _, r := range riskRules {
			if r.re.MatchString(l) {
				out = append(out, c.RiskHit{Rule: r.rule, File: path, Line: n, Kind: "review"})
			}
		}
	})
	return out
}
