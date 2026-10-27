package build

import (
	"fmt"
	"strconv"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Route is the OpenSSL build route for a 7.4 build.
type Route struct {
	ID      string // system | B | A3 | A (lab only) | system-unpatched (lab only)
	Mode    c.OpenSSLMode
	Compat  bool // apply openssl3-compat 0048/0051 (always for 7.4; no-ops on 1.1)
	Reasons []c.Reason
}

// SelectRoute matches the original binary's OpenSSL linkage. It never selects a private *static* OpenSSL (route A):
// libcurl's OpenSSL-3 calls bind to the 1.1.1 symbols the php executable exports (spike: curl HTTPS breaks).
func SelectRoute(targetMinor string, link *c.OpenSSLLinkage) Route {
	if targetMinor != "7.4" {
		return Route{ID: "system", Mode: "system"}
	}
	if link == nil {
		return Route{ID: "B", Mode: "system", Compat: true, Reasons: []c.Reason{"openssl-route-b", "openssl-semantics"}}
	}
	switch link.Kind {
	case "none":
		return Route{ID: "system", Mode: "system", Compat: true}
	case "system":
		if opensslMajor(link.Version) >= 3 {
			return Route{ID: "B", Mode: "system", Compat: true, Reasons: []c.Reason{"openssl-route-b"}}
		}
		return Route{ID: "system", Mode: "system", Compat: true}
	case "private-shared", "private-static":
		return Route{ID: "A3", Mode: "private-shared", Compat: true, Reasons: []c.Reason{"openssl-route-a3", "eol-openssl"}}
	default:
		return Route{ID: "B", Mode: "system", Compat: true, Reasons: []c.Reason{"openssl-route-b", "openssl-semantics"}}
	}
}

// LabRoute applies the --lab-openssl override; callers must have checked manifest.lab.
func LabRoute(r Route, lab string) Route {
	switch lab {
	case "route-a":
		return Route{ID: "A", Mode: "private", Compat: true}
	case "system-unpatched":
		return Route{ID: "system-unpatched", Mode: "system", Compat: false}
	}
	return r
}

// opensslMajor parses "OpenSSL 3.0.2 15 Mar 2022", "1.1.1w", "3.0.2" → 3 / 1; 0 if unknown.
func opensslMajor(v string) int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(v, "OpenSSL/"), "OpenSSL "))
	if i := strings.IndexByte(v, '.'); i > 0 {
		n, _ := strconv.Atoi(v[:i])
		return n
	}
	return 0
}

// needsOpenSSL111: PHP 8.4 and later require OpenSSL 1.1.1 or newer (they build against OpenSSL 3 as well).
func needsOpenSSL111(minor string) bool {
	var maj, min int
	if _, err := fmt.Sscanf(minor, "%d.%d", &maj, &min); err != nil {
		return false
	}
	return maj > 8 || maj == 8 && min >= 4
}

// opensslBelow111 reports a parseable OpenSSL version older than 1.1.1 ("1.0.2k", "OpenSSL 1.1.0l  10 Sep 2019");
// an unknown version is not below: the configure check is the last word then.
func opensslBelow111(v string) bool {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(v, "OpenSSL/"), "OpenSSL "))
	var a, b, c int
	if n, _ := fmt.Sscanf(v, "%d.%d.%d", &a, &b, &c); n < 3 {
		return false
	}
	return a < 1 || a == 1 && (b < 1 || b == 1 && c < 1)
}
