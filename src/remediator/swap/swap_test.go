package swap

import (
	"reflect"
	"testing"
)

// A child that still maps the old libphp (or not the new one) after graceful is stale → restart fallback.
func TestStaleDSO(t *testing.T) {
	nw, old := "/opt/phosphor/php/7.4.33-phosphor1-ab12cd34/libphp7.so", "/usr/local/php74-apache/modules/libphp7.so"
	maps := map[int][]string{
		10: {"/lib/x86_64-linux-gnu/libc.so.6", nw},
		11: {old},
		12: {old + " (deleted)", nw},
		13: {"/lib/x86_64-linux-gnu/libc.so.6"},
	}
	stale, fresh := StaleDSO(maps, nw, old)
	if !reflect.DeepEqual(stale, []int{11, 12, 13}) || fresh != 2 {
		t.Fatalf("stale=%v fresh=%d", stale, fresh)
	}
	stale, fresh = StaleDSO(map[int][]string{1: {nw}, 2: {nw}}, nw, old)
	if len(stale) != 0 || fresh != 2 {
		t.Fatalf("all fresh: stale=%v fresh=%d", stale, fresh)
	}
}

func TestParseExecStart(t *testing.T) {
	want := []string{"/usr/local/php74/sbin/php-fpm", "--nodaemonize", "--fpm-config", "/usr/local/php74/etc/php-fpm.conf"}
	for _, in := range []string{
		"/usr/local/php74/sbin/php-fpm --nodaemonize --fpm-config /usr/local/php74/etc/php-fpm.conf",
		"{ path=/usr/local/php74/sbin/php-fpm ; argv[]=/usr/local/php74/sbin/php-fpm --nodaemonize --fpm-config /usr/local/php74/etc/php-fpm.conf ; ignore_errors=no }",
		"-/usr/local/php74/sbin/php-fpm --nodaemonize --fpm-config /usr/local/php74/etc/php-fpm.conf",
	} {
		if got := ParseExecStart(in); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseExecStart(%q)=%v", in, got)
		}
	}
}

// The query's Status (auto|manual) is read so the rollback restores the mode, not only the value.
func TestAltField(t *testing.T) {
	q := "Name: php\nLink: /usr/bin/php\nStatus: auto\nBest: /usr/bin/php7.4\nValue: /usr/bin/php7.4\n\nAlternative: /usr/bin/php7.4\nPriority: 74\n"
	if altField(q, "Status") != "auto" || altField(q, "Value") != "/usr/bin/php7.4" || altField(q, "Best") != "/usr/bin/php7.4" {
		t.Fatalf("%q %q", altField(q, "Status"), altField(q, "Value"))
	}
}

// A module switch never crosses PHP majors (php7_module/libphp7.so vs php_module/libphp.so).
func TestModuleMatches(t *testing.T) {
	if moduleMatches("php7_module", "/opt/phosphor/php/x/libphp7.so", "r") != nil || moduleMatches("php_module", "/opt/phosphor/php/x/libphp.so", "r") != nil {
		t.Fatal("same major refused")
	}
	if moduleMatches("php7_module", "/opt/phosphor/php/x/libphp.so", "r") == nil || moduleMatches("php_module", "/opt/phosphor/php/x/libphp7.so", "r") == nil {
		t.Fatal("cross-major switch allowed")
	}
}

// The dnf hold is an excludepkgs line in [main] (no versionlock plugin to install and remove).
func TestWithExcludes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"[main]\ngpgcheck=1\n", "[main]\nexcludepkgs=php8.2*\ngpgcheck=1\n"},
		{"[main]\nexcludepkgs=kernel*\ngpgcheck=1\n", "[main]\nexcludepkgs=kernel* php8.2*\ngpgcheck=1\n"},
		{"[main]\ngpgcheck=1\n[other]\nexcludepkgs=x\n", "[main]\nexcludepkgs=php8.2*\ngpgcheck=1\n[other]\nexcludepkgs=x\n"},
		{"", "\n[main]\nexcludepkgs=php8.2*\n"},
	} {
		if got, _ := WithExcludes(tc.in, []string{"php8.2*"}); got != tc.want {
			t.Errorf("%q → %q, want %q", tc.in, got, tc.want)
		}
	}
}
