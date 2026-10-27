package build

import (
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

func hasReason(rs []c.Reason, r c.Reason) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}

// The route selector policy.
func TestU18_SelectRoute(t *testing.T) {
	for _, tc := range []struct {
		name   string
		link   *c.OpenSSLLinkage
		id     string
		mode   c.OpenSSLMode
		reason c.Reason
	}{
		{"system 1.1 (20.04, bullseye)", &c.OpenSSLLinkage{Kind: "system", Version: "1.1.1w"}, "system", "system", ""},
		{"system 3 (f3, jammy)", &c.OpenSSLLinkage{Kind: "system", Version: "OpenSSL 3.0.2 15 Mar 2022"}, "B", "system", "openssl-route-b"},
		{"private shared (fixture f)", &c.OpenSSLLinkage{Kind: "private-shared", Version: "1.1.1w", CurlSSL: "OpenSSL/3.0.2"}, "A3", "private-shared", "eol-openssl"},
		{"private static", &c.OpenSSLLinkage{Kind: "private-static", Version: "1.1.1w", StaticSyms: true}, "A3", "private-shared", "openssl-route-a3"},
		{"no ext/openssl", &c.OpenSSLLinkage{Kind: "none"}, "system", "system", ""},
		{"unknown", &c.OpenSSLLinkage{Kind: "unknown"}, "B", "system", "openssl-semantics"},
		{"no linkage recorded", nil, "B", "system", "openssl-semantics"},
	} {
		r := SelectRoute("7.4", tc.link)
		if r.ID != tc.id || r.Mode != tc.mode || !r.Compat || (tc.reason != "" && !hasReason(r.Reasons, tc.reason)) {
			t.Errorf("%s: got %+v", tc.name, r)
		}
		if r.ID == "A" {
			t.Errorf("%s: route A must never be selected", tc.name)
		}
	}
	if r := SelectRoute("8.2", &c.OpenSSLLinkage{Kind: "private-static"}); r.ID != "system" {
		t.Errorf("8.x always system: %+v", r)
	}
	if r := LabRoute(SelectRoute("7.4", nil), "route-a"); r.ID != "A" {
		t.Error("lab route-a override")
	}
	if r := LabRoute(SelectRoute("7.4", nil), "system-unpatched"); r.Compat {
		t.Error("system-unpatched must skip compat patches")
	}
}

// PHP 8.4+ needs OpenSSL ≥ 1.1.1: an older (parseable) one blocks the build up front; unknown versions pass to configure.
func TestOpenSSLFloor84(t *testing.T) {
	for _, tc := range []struct {
		minor string
		need  bool
	}{{"7.4", false}, {"8.2", false}, {"8.3", false}, {"8.4", true}, {"8.5", true}, {"9.0", true}} {
		if needsOpenSSL111(tc.minor) != tc.need {
			t.Errorf("%s", tc.minor)
		}
	}
	for v, below := range map[string]bool{"1.0.2k": true, "OpenSSL 1.1.0l  10 Sep 2019": true, "1.1.1w": false,
		"OpenSSL 3.0.2 15 Mar 2022": false, "3.5.0": false, "": false, "unknown": false} {
		if opensslBelow111(v) != below {
			t.Errorf("%q", v)
		}
	}
	if k := ExpectedFailKeys("8.4", 3); len(k) != 1 || k[0] != "php84" {
		t.Errorf("8.4 smoke keys %v", k)
	}
}
