package build

import (
	"os"
	"strings"
	"testing"
)

func readTD(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Shell-words parse + rewrite on real configure lines.
func TestU03_SplitRealLines(t *testing.T) {
	for _, tc := range []struct {
		file string
		want []string // must be present, verbatim, after parsing
	}{
		{"configure/docker-library-7.4.txt", []string{"--with-config-file-path=/usr/local/etc/php", "--enable-option-checking=fatal", "--with-apxs2"}},
		{"configure/docker-library-8.2.txt", []string{"--enable-fpm", "--with-fpm-user=www-data"}},
		{"configure/custom-7.4-fpm-apxs.txt", []string{"--with-fpm-systemd", "CFLAGS=-O2 -g", "--with-apxs2=/usr/bin/apxs2"}},
	} {
		args, err := SplitConfigure(readTD(t, tc.file))
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if len(args) == 0 || strings.Contains(args[0], "configure") {
			t.Fatalf("%s: ./configure not dropped: %v", tc.file, args[:1])
		}
		for _, w := range tc.want {
			found := false
			for _, a := range args {
				if a == w {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: missing %q", tc.file, w)
			}
		}
	}
}

func TestU03_SplitQuoting(t *testing.T) {
	got, err := SplitConfigure(`'./configure' '--with-a=x y' "--with-b=\"q\"" --with-c=d\ e 'it'\''s'`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--with-a=x y", `--with-b="q"`, "--with-c=d e", "it's"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q want %q", got, want)
	}
	if _, err := SplitConfigure(`'--unterminated`); err == nil {
		t.Fatal("want error on unterminated quote")
	}
}

func TestU03_Rewrite(t *testing.T) {
	args, _ := SplitConfigure(readTD(t, "configure/custom-7.4-fpm-apxs.txt"))
	args = append(args, "EXTENSION_DIR=/usr/local/lib/php/extensions/no-debug-non-zts-20190902")
	eff, dropped := RewriteConfigure(args, RewriteOpts{Prefix: "/opt/phosphor/php/7.4.33-phosphor1-abcd", Strip: []string{"--with-fpm-systemd"}})
	if eff[0] != "--prefix=/opt/phosphor/php/7.4.33-phosphor1-abcd" {
		t.Fatalf("prefix not first: %v", eff[:1])
	}
	for _, e := range eff {
		// configure has no --with-extension-dir (fatal under --enable-option-checking=fatal); EXTENSION_DIR must not
		// point at the customer's live dir.
		if strings.HasPrefix(e, "--with-extension-dir") || strings.HasPrefix(e, "EXTENSION_DIR=") || e == "--prefix=/usr/local" {
			t.Fatalf("install-location flag kept: %s", e)
		}
	}
	if !HasFlag(eff, "--with-config-file-path") || FlagValue(eff, "--with-config-file-scan-dir") != "/usr/local/etc/php/conf.d" {
		t.Fatal("live ini tree flags must be kept")
	}
	if !HasFlag(eff, "--with-apxs2") || !HasFlag(eff, "--enable-fpm") || HasFlag(eff, "--with-fpm-systemd") {
		t.Fatalf("feature flags wrong: %v", eff)
	}
	reasons := map[string]string{}
	for _, d := range dropped {
		reasons[flagName(d.Arg)] = d.Reason
	}
	if reasons["--prefix"] != "prefix-rewrite" || reasons["--with-extension-dir"] != "prefix-rewrite" || reasons["EXTENSION_DIR"] != "prefix-rewrite" || reasons["--with-fpm-systemd"] != "lab-strip" {
		t.Fatalf("dropped reasons: %v", reasons)
	}
}

func TestU03_Unrecognized(t *testing.T) {
	out := "checking for foo... yes\nconfigure: WARNING: unrecognized options: --with-system-tzdata, --enable-debian-thing\nThank you for using PHP.\n"
	un := UnrecognizedOptions(out)
	if strings.Join(un, ",") != "--with-system-tzdata,--enable-debian-thing" {
		t.Fatalf("got %v", un)
	}
	d := WithUnrecognized(nil, []string{"--enable-fpm", "--with-system-tzdata=/usr/share/zoneinfo"}, un)
	if len(d) != 1 || d[0].Reason != "unrecognized" || d[0].Arg != "--with-system-tzdata=/usr/share/zoneinfo" {
		t.Fatalf("got %+v", d)
	}
	if len(UnrecognizedOptions("no warnings here")) != 0 {
		t.Fatal("false positive")
	}
}

// Fixture (f): the hand-built line points PKG_CONFIG_PATH at the customer's private OpenSSL; route A3 must replace it.
func TestU03_RewriteReplacesPrivateOpenSSLEnv(t *testing.T) {
	args, _ := SplitConfigure(readTD(t, "configure/custom-7.4-fpm-apxs.txt"))
	eff, dropped := RewriteConfigure(args, RewriteOpts{Prefix: "/opt/phosphor/php/p",
		Replace: []string{"PKG_CONFIG_PATH", "LDFLAGS"},
		Extra:   []string{"PKG_CONFIG_PATH=/opt/phosphor/php/p/openssl/lib/pkgconfig", "LDFLAGS=-Wl,-rpath,/opt/phosphor/php/p/openssl/lib"}})
	n := 0
	for _, e := range eff {
		if strings.HasPrefix(e, "PKG_CONFIG_PATH=") {
			n++
			if e != "PKG_CONFIG_PATH=/opt/phosphor/php/p/openssl/lib/pkgconfig" {
				t.Fatalf("customer OpenSSL leaked: %s", e)
			}
		}
	}
	if n != 1 {
		t.Fatalf("PKG_CONFIG_PATH count %d: %v", n, eff)
	}
	found := false
	for _, d := range dropped {
		if d.Arg == "PKG_CONFIG_PATH=/usr/local/openssl-1.1/lib/pkgconfig" && d.Reason == "prefix-rewrite" {
			found = true
		}
	}
	if !found {
		t.Fatalf("original PKG_CONFIG_PATH not recorded as dropped: %v", dropped)
	}
}

// --disable-cgi is dropped for the build so the rfc1867 smoke tests run.
func TestBUG053_KeepCGI(t *testing.T) {
	eff, dropped := RewriteConfigure([]string{"--prefix=/usr/local", "--with-apxs2", "--disable-cgi"}, RewriteOpts{Prefix: "/p", KeepCGI: true})
	if HasFlag(eff, "--disable-cgi") {
		t.Fatalf("--disable-cgi kept: %v", eff)
	}
	ok := false
	for _, x := range dropped {
		if x.Arg == "--disable-cgi" && x.Reason == "smoke-cgi" {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("not recorded: %v", dropped)
	}
	if eff, _ := RewriteConfigure([]string{"--disable-cgi"}, RewriteOpts{Prefix: "/p"}); !HasFlag(eff, "--disable-cgi") {
		t.Fatal("without KeepCGI the flag stays")
	}
}

// FlagValue returns the value of --name=value, or "".
func FlagValue(args []string, name string) string {
	for _, a := range args {
		if flagName(a) == name && strings.Contains(a, "=") {
			return a[len(name)+1:]
		}
	}
	return ""
}
