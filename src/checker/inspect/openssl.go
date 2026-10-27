package inspect

import (
	"debug/elf"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// opensslLinkage is runtimes[].openssl_linkage: which OpenSSL ext/openssl runs on, so the build
// matches it (system 1.1 → system, system 3 → B, private 1.1 → A3). Sources: the runtime's own phpinfo (OpenSSL
// Library Version, curl's SSL Version), ldd of the ELF that carries ext/openssl (the binary/libphp, or openssl.so
// when it is a shared extension), and its dynamic symbol table.
func opensslLinkage(r *c.Runtime, pi *phpInfo) *c.OpenSSLLinkage {
	if !hasModule(r.Modules, "openssl") {
		return &c.OpenSSLLinkage{Kind: "none"}
	}
	l := &c.OpenSSLLinkage{Kind: "unknown", Version: opensslVersionText(pi.ExtVersions["openssl library"])}
	if hasModule(r.Modules, "curl") {
		l.CurlSSL = pi.ExtVersions["ssl"]
	}
	obj := r.Binary
	for _, e := range r.SharedExts {
		if e.Name == "openssl" {
			obj = e.SOPath
		}
	}
	l.StaticSyms = definesSSLSyms(obj)
	out, err := sys.Run(15*time.Second, nil, "ldd", obj)
	if err != nil {
		return l
	}
	l.Path = lddLibssl(out)
	switch {
	case l.Path != "" && systemLibDir(l.Path):
		l.Kind = "system"
	case l.Path != "":
		l.Kind = "private-shared"
	default:
		// ext/openssl is loaded but no libssl is linked: OpenSSL is compiled into the object
		l.Kind = "private-static"
	}
	if l.Version == "" {
		l.Version = sonameVersion(l.Path)
	}
	return l
}

func hasModule(mods []string, name string) bool {
	for _, m := range mods {
		if strings.EqualFold(m, name) {
			return true
		}
	}
	return false
}

var opensslVerRe = regexp.MustCompile(`OpenSSL\s+(\d+\.\d+\.\d+[a-z]*)`)

// opensslVersionText turns "OpenSSL 1.1.1w  11 Sep 2023" into "1.1.1w".
func opensslVersionText(s string) string {
	if m := opensslVerRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

var lddSSLRe = regexp.MustCompile(`(?m)^\s*libssl\.so[.\d]*\s+=>\s+(\S+)`)

// lddLibssl is the resolved libssl path in ldd output ("" if not linked or not found).
func lddLibssl(out string) string {
	if m := lddSSLRe.FindStringSubmatch(out); m != nil && m[1] != "not" {
		return m[1]
	}
	return ""
}

// systemLibDir: the distro's library dirs (incl. multiarch); anything else (/usr/local, /opt, a prefix) is private.
func systemLibDir(p string) bool {
	d := filepath.Dir(p)
	for _, s := range []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64"} {
		if d == s || strings.HasPrefix(d, s+"/") && !strings.Contains(strings.TrimPrefix(d, s+"/"), "/") {
			return true
		}
	}
	return false
}

// sonameVersion is "1.1" / "3" from a libssl.so.1.1 / libssl.so.3 path.
func sonameVersion(p string) string {
	_, v, ok := strings.Cut(filepath.Base(p), ".so.")
	if !ok {
		return ""
	}
	return v
}

// definesSSLSyms: the object exports (defines) OpenSSL entry points in its dynamic symbol table, the signature of
// a statically linked OpenSSL whose symbols can capture other libraries' calls (route A, banned).
func definesSSLSyms(path string) bool {
	f, err := elf.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	syms, err := f.DynamicSymbols()
	if err != nil {
		return false
	}
	for _, s := range syms {
		if s.Section == elf.SHN_UNDEF {
			continue
		}
		switch s.Name {
		case "SSL_CTX_new", "OPENSSL_init_ssl", "EVP_EncryptInit_ex":
			return true
		}
	}
	return false
}
