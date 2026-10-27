package build

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
)

// buildPrivateOpenSSL builds the pinned OpenSSL 1.1.1w tarball into <prefix>/openssl. Route A3 is shared + rpath
// (coexists with the system libssl3 through symbol versioning); route A (lab only) is static.
// It returns the env the PHP configure/make must run with.
func buildPrivateOpenSSL(r *runner, g *guardrails, tarball, work, prefix string, shared bool) ([]string, *c.Error) {
	dir := filepath.Join(work, "openssl")
	_ = os.MkdirAll(dir, 0o755)
	if _, code := r.RunIn(dir, 5*time.Minute, nil, "tar", "-xzf", tarball, "--strip-components=1"); code != 0 {
		return nil, c.Errf(c.EBuildFailed, "step", "openssl-extract")
	}
	osslPrefix := filepath.Join(prefix, "openssl")
	mode := "no-shared"
	if shared {
		mode = "shared"
	}
	args := []string{mode, "no-tests", "--prefix=" + osslPrefix, "--openssldir=/usr/lib/ssl"}
	if shared {
		args = append(args, "-Wl,-rpath,"+osslPrefix+"/lib")
	}
	if _, code := r.Heavy(dir, 20*time.Minute, nil, "./config", args...); code != 0 {
		return nil, c.Errf(c.EBuildFailed, "step", "openssl-config")
	}
	if _, code := r.Heavy(dir, 90*time.Minute, nil, "make", "-j"+strconv.Itoa(g.Jobs)); code != 0 {
		return nil, c.Errf(c.EBuildFailed, "step", "openssl-make")
	}
	if _, code := r.RunIn(dir, 20*time.Minute, nil, "make", "install_sw"); code != 0 {
		return nil, c.Errf(c.EBuildFailed, "step", "openssl-install")
	}
	env := []string{"PKG_CONFIG_PATH=" + osslPrefix + "/lib/pkgconfig"}
	if shared {
		env = append(env, "LDFLAGS=-Wl,-rpath,"+osslPrefix+"/lib")
	}
	return env, nil
}
