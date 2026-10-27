//go:build e2e && local

package local

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	h "github.com/asafg8/Phosphor/testing/e2e/harness"
)

// Runtime fingerprint (`phosphor find`).

const t2FP = "fingerprint.json"

// t2Check runs `phosphor find` in fx (shared: check is read-only) and returns nothing; documents are read with h.JQ.
func t2Check(t *testing.T, fx string) {
	t.Helper()
	h.Need(t, fx)
	h.Ready(t, fx) // reset: an earlier swap test may have left the fixture on the phosphor build
	h.Find(t, fx)
}

// mod_php fingerprint, custom apxs build (j): configure read from the binary with the Go strings scanner.
func TestT201_ModPHPFingerprintCustom(t *testing.T) {
	t2Check(t, "j")
	mp := `.runtimes[]|select(.sapi=="mod_php")|`
	h.JQ(t, "j", t2FP, mp+`(.binary|startswith("`+h.Label(t, "j", "php.prefix")+`/")) and (.binary|endswith("/libphp7.so"))`)
	h.JQ(t, "j", t2FP, mp+`(.configure_command|test("--with-apxs2")) and .configure_source=="verbatim"`)
	h.JQ(t, "j", t2FP, mp+`.provenance.kind=="custom" and .service.unit=="apache2.service" and (.modules|index("gd")!=null)`)
	h.JQ(t, "j", t2FP, mp+`.logs.access[0]=={"path":"/var/log/apache2/access.log","format":"combined"}`)
	h.JQ(t, "j", t2FP, mp+`.code_roots[0]|.path=="/var/www/html" and .kind=="first-party" and .via=="apache-docroot"`)
	h.JQ(t, "j", t2FP, `[.runtimes[]|.php2shell.status]|all(.=="vulnerable")`)
	h.JQ(t, "j", t2FP, `.host.pkg_mgr=="apt" and .host.init=="systemd"`)
	cp := exec.Command("docker", "exec", h.Container("j"), "sh", "-c", "grep -a -o -m1 \"'./configure'[^\\x00]*\" $(find "+h.Label(t, "j", "php.prefix")+" -name libphp7.so | head -1)")
	if out, err := cp.Output(); err == nil {
		got := h.JQ(t, "j", t2FP, mp+`.configure_command`)
		want := strings.TrimSpace(string(out))
		if len(want) > 40 {
			want = want[:40]
		}
		if want == "" || !strings.Contains(strings.Trim(got, `"`), want) {
			t.Errorf("configure differs from the binary")
		}
	}
}

// Distro build fingerprint (a): configure stripped, version = PHP_VERSION.
func TestT201b_DistroFingerprint(t *testing.T) {
	t2Check(t, "a")
	fpm := `.runtimes[]|select(.sapi=="fpm")|`
	h.JQ(t, "a", t2FP, fpm+`.provenance.kind=="distro-pkg" and .provenance.pkg=="php7.4-fpm" and .provenance.pkg_version=="7.4.33-1+deb11u12"`)
	h.JQ(t, "a", t2FP, fpm+`.version=="7.4.33" and .configure_source=="absent" and (.static_modules|length>0) and (.shared_exts|length>0)`)
	h.JQ(t, "a", t2FP, fpm+`.php2shell.status=="vulnerable"`)
}

// fpm + nginx + WordPress (distro, a).
func TestT202_FpmWordPress(t *testing.T) {
	t2Check(t, "a")
	fpm := `.runtimes[]|select(.sapi=="fpm")|`
	h.JQ(t, "a", t2FP, fpm+`.service|.unit=="php7.4-fpm.service" and .unit_type=="notify" and .listen==["/run/php/php7.4-fpm.sock"]`)
	h.JQ(t, "a", t2FP, fpm+`.code_roots|any(.path=="/var/www/wordpress" and .via=="nginx-root" and .kind=="first-party")`)
	h.JQ(t, "a", t2FP, fpm+`.code_roots|any(.path=="/var/www/wordpress/wp-includes" and .kind=="third-party")`)
	h.JQ(t, "a", t2FP, fpm+`(.code_roots[0].third_party_paths|map(select(startswith("/var/www/wordpress/phosphor-app")))|length)==0`)
	h.JQ(t, "a", t2FP, `.host.openssl_system|startswith("1.1.1")`)
}

// Custom fpm + hidden consumers (i).
func TestT202b_CustomFpmConsumers(t *testing.T) {
	t2Check(t, "i")
	prefix := h.Label(t, "i", "php.prefix")
	h.JQ(t, "i", t2FP, `.runtimes[]|select(.sapi=="fpm")|.binary=="`+prefix+`/sbin/php-fpm" and .provenance.kind=="custom" and .service.unit=="`+h.Label(t, "i", "php.fpm_unit")+`" and .service.unit_type=="notify"`)
	h.JQ(t, "i", t2FP, `.runtimes[]|select(.sapi=="cli")|.binary=="`+prefix+`/bin/php"`)
	h.JQ(t, "i", t2FP, `.runtimes[]|select(.sapi=="fpm")|.third_party_exts|any(.name=="redis" and .source=="pecl")`)
	h.JQ(t, "i", t2FP, `[.consumers[]|.kind]|sort==["cron","shebang","systemd"]`)
	h.JQ(t, "i", t2FP, `.consumers|any(.kind=="systemd" and .path=="acme-queue.service") and any(.kind=="shebang" and .path=="/usr/local/bin/acme-report")`)
}

// supervisord + custom build (c).
func TestT203_SupervisordCustom(t *testing.T) {
	t2Check(t, "c")
	h.JQ(t, "c", t2FP, `.runtimes[]|select(.sapi=="fpm")|.version=="5.6.40" and .provenance.kind=="custom" and .service.manager=="supervisord"`)
	h.JQ(t, "c", t2FP, `.host.init=="supervisord"`)
}

// dnf + PECL (h).
func TestT204_DnfPecl(t *testing.T) {
	t2Check(t, "h")
	h.JQ(t, "h", t2FP, `.host.pkg_mgr=="dnf"`)
	h.JQ(t, "h", t2FP, `.runtimes[]|select(.sapi=="fpm")|.provenance.repo=="amzn" and (.third_party_exts[]|select(.name=="redis")|.source=="pecl" and (.version|length>0) and (.sha256|length==64))`)
}

// check + verify are read-only and leave nothing behind: verify without a terminal asks nothing and
// builds nothing (static checks), so the only changes are the CLI's own binary and results directory.
func TestT205_ReadOnlyAndClean(t *testing.T) {
	for _, fx := range []string{"j", "a", "c"} {
		fx := fx
		t.Run(fx, func(t *testing.T) {
			h.Need(t, fx)
			h.Ready(t, fx)
			h.Load(t, fx, "off")
			defer h.Load(t, fx, "on")
			time.Sleep(3 * time.Second)
			before := h.Snap(t, fx, "before")
			h.FindVerify(t, fx)
			if d := h.HostDiff(t, before, h.Snap(t, fx, "after"), "/opt/phosphor/state"); d != "" {
				t.Errorf("host changed:\n%s", d)
			}
			if out, _ := h.ShErr(fx, "sh", "-c", "ls -A /opt/phosphor 2>/dev/null | grep -vx state || true"); strings.TrimSpace(out) != "" {
				t.Errorf("/opt/phosphor left behind: %s", out)
			}
		})
	}
}

// Several runtimes per host (j, a).
func TestT209_SeveralRuntimes(t *testing.T) {
	t.Run("j", func(t *testing.T) {
		t2Check(t, "j")
		h.JQ(t, "j", t2FP, `[.runtimes[].sapi]|sort==["cli","mod_php"]`)
	})
	t.Run("a", func(t *testing.T) {
		t2Check(t, "a")
		h.JQ(t, "a", t2FP, `[.runtimes[].sapi]|sort==["cli","fpm"]`)
	})
}
