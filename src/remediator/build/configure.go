package build

import (
	"errors"
	"regexp"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
)

// SplitConfigure parses a "Configure Command" line as php -i prints it ('./configure'  '--with-x' 'CFLAGS=-O2 -g')
// with POSIX shell quoting rules (single, double, backslash). The leading ./configure word is dropped.
func SplitConfigure(line string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord, sq, dq := false, false, false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case sq:
			if ch == '\'' {
				sq = false
			} else {
				cur.WriteByte(ch)
			}
		case dq:
			switch ch {
			case '"':
				dq = false
			case '\\':
				if i+1 < len(line) && strings.ContainsRune("\"\\$`", rune(line[i+1])) {
					i++
				}
				cur.WriteByte(line[i])
			default:
				cur.WriteByte(ch)
			}
		case ch == '\'':
			sq, inWord = true, true
		case ch == '"':
			dq, inWord = true, true
		case ch == '\\':
			if i+1 < len(line) {
				i++
				cur.WriteByte(line[i])
				inWord = true
			}
		case ch == ' ' || ch == '\t' || ch == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(ch)
			inWord = true
		}
	}
	if sq || dq {
		return nil, errors.New("unterminated quote")
	}
	if inWord {
		out = append(out, cur.String())
	}
	if len(out) > 0 && strings.HasSuffix(out[0], "configure") {
		out = out[1:]
	}
	return out, nil
}

// RewriteOpts controls how the original configure line is replayed into our prefix.
// There is no --with-extension-dir in PHP's configure: the extension dir is the EXTENSION_DIR variable, defaulting
// to <prefix>/lib/php/extensions/<api> — which is inside our prefix, so we keep the default.
type RewriteOpts struct {
	Prefix string
	// Strip are lab-only flags to remove (--lab-strip-configure), recorded as reason lab-strip.
	Strip []string
	// Replace are flag / VAR names whose original value must not survive (reason prefix-rewrite); route A3 uses it
	// for PKG_CONFIG_PATH / LDFLAGS, which a hand-built 7.4 often embeds pointing at the customer's private OpenSSL
	// (fixture f: PKG_CONFIG_PATH=/usr/local/openssl-1.1/lib/pkgconfig) — configure VAR=value args override env.
	Replace []string
	// Extra are flags / VAR=value args appended last (e.g. our private OpenSSL's PKG_CONFIG_PATH).
	Extra []string
	// KeepCGI drops --disable-cgi (reason smoke-cgi): the CGI SAPI must exist in the build tree so the rfc1867 /
	// multipart tests the patch touches actually run. It is not installed unless the original had it.
	KeepCGI bool
}

// prefixFlags are replaced by ours; they would otherwise point the new build at the live install. EXTENSION_DIR=…
// in an original line would make our build load from — and install into — the customer's live extension dir.
var prefixFlags = []string{"--prefix", "--exec-prefix", "--bindir", "--sbindir", "--libdir", "--libexecdir",
	"--with-extension-dir", "EXTENSION_DIR", "--datadir", "--sysconfdir", "--localstatedir", "--mandir", "--includedir"}

func flagName(a string) string {
	if i := strings.IndexByte(a, '='); i >= 0 {
		return a[:i]
	}
	return a
}

// RewriteConfigure keeps config-file-path / scan-dir and every feature flag identical (live ini tree),
// replaces the install-location flags with ours, and keeps env assignments (CFLAGS=…) as configure arguments.
// --with-apxs2 stays: libphp is built with apxs but never installed through it (no httpd.conf edits).
func RewriteConfigure(orig []string, o RewriteOpts) (effective []string, dropped []c.DroppedFlag) {
	strip := map[string]bool{}
	for _, s := range o.Strip {
		strip[flagName(s)] = true
	}
	for _, a := range orig {
		n := flagName(a)
		switch {
		case strip[n]:
			dropped = append(dropped, c.DroppedFlag{Arg: a, Reason: "lab-strip"})
		case o.KeepCGI && n == "--disable-cgi":
			dropped = append(dropped, c.DroppedFlag{Arg: a, Reason: "smoke-cgi"})
		case contains(prefixFlags, n) || contains(o.Replace, n):
			dropped = append(dropped, c.DroppedFlag{Arg: a, Reason: "prefix-rewrite"})
		default:
			effective = append(effective, a)
		}
	}
	effective = append([]string{"--prefix=" + o.Prefix}, effective...)
	effective = append(effective, o.Extra...)
	return effective, dropped
}

var unrecognizedRE = regexp.MustCompile(`(?m)^configure: WARNING: unrecognized options: (.+)$`)

// UnrecognizedOptions extracts the flags configure ignored (7.4 only warns; distro-only flags).
func UnrecognizedOptions(configureOutput string) []string {
	var out []string
	for _, m := range unrecognizedRE.FindAllStringSubmatch(configureOutput, -1) {
		for _, f := range strings.Split(m[1], ",") {
			if f = strings.TrimSpace(f); f != "" {
				out = append(out, f)
			}
		}
	}
	return out
}

// WithUnrecognized marks flags that configure reported as unrecognized in dropped[] (they stay in effective: configure
// already ignored them; recording them is what turns the runtime into `review`).
func WithUnrecognized(dropped []c.DroppedFlag, effective, unrecognized []string) []c.DroppedFlag {
	for _, u := range unrecognized {
		for _, e := range effective {
			if flagName(e) == flagName(u) {
				dropped = append(dropped, c.DroppedFlag{Arg: e, Reason: "unrecognized"})
				break
			}
		}
	}
	return dropped
}

// HasFlag reports whether a flag name (e.g. --with-apxs2, --enable-fpm) is present.
func HasFlag(args []string, name string) bool {
	for _, a := range args {
		if flagName(a) == name {
			return true
		}
	}
	return false
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
