package preflight

import (
	"debug/elf"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// LinkFacts are what preflight reads from the new build (ELF dynsym + ldd + php -r).
type LinkFacts struct {
	Kind        string   // linkage kind of ext/openssl: system | private-shared | private-static | none
	Version     string   // OPENSSL_VERSION_TEXT of the new build
	StaticSyms  int      // defined (non-UND) SSL_CTX_new / OPENSSL_init_ssl / EVP_EncryptInit_ex in the executable
	NeedsLibssl []string // libssl.so.* NEEDED by the binary or any loaded object
	CurlSSL     string   // curl_version()["ssl_version"], "" when ext/curl is absent
	Mixed       bool     // libssl resolves both under the prefix and outside it
}

// opensslCoherent is check `openssl-coherent`. It fails (why != "") on route A's signature (static
// OpenSSL symbols exported while something needs libssl.so.*), on curl "not available", or when the new linkage
// kind / OpenSSL major.minor differs from the original's. Two shared, versioned OpenSSLs side by side (route A3,
// fixture f's original) pass.
func opensslCoherent(orig *c.OpenSSLLinkage, nw LinkFacts) (why string, data map[string]any) {
	data = map[string]any{"static_syms": nw.StaticSyms, "needs": nw.NeedsLibssl, "curl_ssl": nw.CurlSSL,
		"kind": nw.Kind, "version": nw.Version}
	switch {
	case nw.StaticSyms > 0 && len(nw.NeedsLibssl) > 0:
		return "static-openssl-with-shared-libssl", data
	case strings.Contains(nw.CurlSSL, "not available"):
		return "curl-ssl-unavailable", data
	}
	if orig != nil && orig.Kind != "" && orig.Kind != "unknown" {
		wantKind := orig.Kind
		if wantKind == "private-static" { // the policy maps private-static originals to A3 (shared)
			wantKind = "private-shared"
		}
		if nw.Kind != wantKind {
			return "linkage-kind-changed", data
		}
		if om, nm := opensslMinor(orig.Version), opensslMinor(nw.Version); om != "" && nm != "" && om != nm {
			return "openssl-version-changed", data
		}
	}
	return "", data
}

// opensslMinor returns "1.1" / "3.0" (the coherence comparison unit), or "".
func opensslMinor(v string) string {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(v, "OpenSSL/"), "OpenSSL "))
	p := strings.SplitN(v, ".", 3)
	if len(p) < 2 {
		return ""
	}
	return p[0] + "." + strings.TrimRightFunc(p[1], func(r rune) bool { return r < '0' || r > '9' })
}

// linkFacts reads the new build's OpenSSL linkage: ldd NEEDED libssl, the ELF dynamic symbols of obj,
// and OPENSSL_VERSION_TEXT / curl's ssl_version from the new CLI.
func linkFacts(cli, obj, prefix string) LinkFacts {
	out, _ := sys.Combined(20*time.Second, nil, "ldd", obj)
	lf := LinkFacts{}
	lf.Kind, lf.NeedsLibssl, lf.Mixed = parseLinkage(out, prefix)
	lf.StaticSyms = staticSSLSyms(obj)
	if lf.Kind == "none" && lf.StaticSyms > 0 {
		lf.Kind = "private-static"
	}
	v, _ := sys.Combined(20*time.Second, nil, cli, "-r",
		`echo defined("OPENSSL_VERSION_TEXT")?OPENSSL_VERSION_TEXT:"","|",function_exists("curl_version")?curl_version()["ssl_version"]:"";`)
	if p := strings.SplitN(strings.TrimSpace(lastLine(v)), "|", 2); len(p) == 2 {
		lf.Version, lf.CurlSSL = p[0], p[1]
	}
	return lf
}

// parseLinkage reads ldd output: the libssl sonames needed, and the kind: private-shared when ANY libssl resolves
// under the prefix (route A3's private libssl.so.1.1 beside the system libssl.so.3 that libcurl brings,
// whatever order ldd lists them in), system when only others resolve, none without libssl. mixed: libssl resolves
// both under and outside the prefix (two OpenSSLs in one process).
func parseLinkage(ldd, prefix string) (kind string, needs []string, mixed bool) {
	private, other := false, false
	for _, l := range strings.Split(ldd, "\n") {
		if !strings.Contains(l, "libssl.so") {
			continue
		}
		needs = append(needs, strings.Fields(l)[0])
		if i := strings.Index(l, "=> "); i >= 0 {
			if f := strings.Fields(l[i+3:]); len(f) > 0 {
				if strings.HasPrefix(f[0], prefix) {
					private = true
				} else {
					other = true
				}
			}
		}
	}
	switch {
	case private:
		kind = "private-shared"
	case other:
		kind = "system"
	default:
		kind = "none"
	}
	return kind, needs, private && other
}

// newMix: the new build runs two OpenSSLs side by side (mixed) where the original did not (its ext/openssl and its
// curl used the same OpenSSL minor): a review, not a failure (two versioned shared OpenSSLs are route A3's design).
func newMix(mixed bool, orig *c.OpenSSLLinkage) bool {
	if !mixed {
		return false
	}
	if orig == nil {
		return true
	}
	om, cm := opensslMinor(orig.Version), opensslMinor(orig.CurlSSL)
	return om == "" || cm == "" || om == cm
}

// staticSSLSyms counts defined (non-UND) OpenSSL entry points in obj's dynamic symbol table (route A's signature).
func staticSSLSyms(obj string) int {
	f, err := elf.Open(obj)
	if err != nil {
		return 0
	}
	defer f.Close()
	syms, err := f.DynamicSymbols()
	if err != nil {
		return 0
	}
	n := 0
	for _, s := range syms {
		switch s.Name {
		case "SSL_CTX_new", "OPENSSL_init_ssl", "EVP_EncryptInit_ex":
			if s.Section != elf.SHN_UNDEF {
				n++
			}
		}
	}
	return n
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
