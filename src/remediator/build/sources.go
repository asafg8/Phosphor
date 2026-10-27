package build

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/ProtonMail/go-crypto/openpgp"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

var httpClient = &http.Client{Timeout: 10 * time.Minute}

// fetchURL downloads u to dst and verifies sha256 before keeping it.
func fetchURL(u, dst, want string) error {
	resp, err := httpClient.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errStatus(resp.StatusCode)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	if got := hex.EncodeToString(h.Sum(nil)); want != "" && got != want {
		os.Remove(tmp)
		return errSHA(got)
	}
	return os.Rename(tmp, dst)
}

type errStatus int

func (e errStatus) Error() string { return "http status " + strconv.Itoa(int(e)) }

type errSHA string

func (e errSHA) Error() string { return "sha256 mismatch " + string(e) }

// fetchSources gets and verifies every source (sha256 always; gpg in addition when required). Nothing is installed
// before this returns nil. Lab sources are never special-cased here.
func fetchSources(x *rt.Ctx, r *runner, srcs []c.BuildSource, dir string) (map[string]string, []c.SourceRef, *c.Error) {
	paths := map[string]string{}
	var refs []c.SourceRef
	for _, s := range srcs {
		if s.Kind == "signature" || s.Kind == "keyring" {
			continue
		}
		dst := filepath.Join(dir, safeName(s.Name))
		var err error
		switch {
		case s.Get != "":
			_, err = x.Fetch(s.Get, dst, s.SHA256)
		case s.URL != "":
			err = fetchURL(s.URL, dst, s.SHA256)
		default:
			return nil, nil, c.Errf(c.ESourceIntegrity, "name", s.Name, "reason", "no-location")
		}
		if err != nil {
			r.logf("# source %s: %v\n", s.Name, err)
			return nil, nil, c.Errf(c.ESourceIntegrity, "name", s.Name, "reason", reasonOf(err))
		}
		if got := sys.SHA256File(dst); s.SHA256 == "" || got != s.SHA256 {
			return nil, nil, c.Errf(c.ESourceIntegrity, "name", s.Name, "reason", "sha256", "got", got)
		}
		ref := c.SourceRef{Name: s.Name, URL: firstNonEmpty(s.URL, s.Get), SHA256: s.SHA256, Verified: "sha256"}
		if s.Verify == "gpg" {
			if e := verifyGPG(x, r, s, dst, dir); e != nil {
				return nil, nil, e
			}
			ref.Verified = "gpg"
		}
		paths[s.Name] = dst
		refs = append(refs, ref)
	}
	return paths, refs, nil
}

// verifyGPG checks the detached signature in-agent (no host gpg: minimal AL2023 has none) against the vendored
// php.net keyring and requires the signing key to belong to a pinned release-manager fingerprint (data/php-8.x.lock).
func verifyGPG(x *rt.Ctx, r *runner, s c.BuildSource, file, dir string) *c.Error {
	if (s.SigURL == "" && s.SigGet == "") || s.KeyringGet == "" {
		return c.Errf(c.ESourceIntegrity, "name", s.Name, "reason", "gpg-unavailable")
	}
	asc := file + ".asc"
	var ferr error
	if s.SigGet != "" {
		_, ferr = x.Fetch(s.SigGet, asc, "")
	} else {
		ferr = fetchURL(s.SigURL, asc, "")
	}
	if ferr != nil {
		return c.Errf(c.ESourceIntegrity, "name", s.Name, "reason", "signature-fetch")
	}
	keyring := filepath.Join(dir, "php-keyring.asc")
	if _, err := x.Fetch(s.KeyringGet, keyring, ""); err != nil {
		return c.Errf(c.ESourceIntegrity, "name", s.Name, "reason", "keyring-fetch")
	}
	fpr, reason := checkDetachedSignature(keyring, file, asc, s.SignerFprs)
	if reason != "" {
		r.logf("# gpg %s: %s\n", s.Name, reason)
		return c.Errf(c.ESourceIntegrity, "name", s.Name, "reason", reason)
	}
	r.logf("# gpg %s: good signature, signer %s\n", s.Name, fpr)
	return nil
}

// checkDetachedSignature verifies sigPath (armored or binary) over dataPath with keyringPath and returns the signer's
// primary fingerprint; reason is "" on success, else bad-signature | signer-not-pinned | keyring.
func checkDetachedSignature(keyringPath, dataPath, sigPath string, pinned []string) (string, string) {
	kf, err := os.Open(keyringPath)
	if err != nil {
		return "", "keyring"
	}
	defer kf.Close()
	kr, err := openpgp.ReadArmoredKeyRing(kf)
	if err != nil {
		return "", "keyring"
	}
	df, err := os.Open(dataPath)
	if err != nil {
		return "", "bad-signature"
	}
	defer df.Close()
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return "", "bad-signature"
	}
	var signer *openpgp.Entity
	if strings.HasPrefix(strings.TrimSpace(string(sig)), "-----BEGIN") {
		signer, err = openpgp.CheckArmoredDetachedSignature(kr, df, strings.NewReader(string(sig)), nil)
	} else {
		signer, err = openpgp.CheckDetachedSignature(kr, df, strings.NewReader(string(sig)), nil)
	}
	if err != nil || signer == nil {
		return "", "bad-signature"
	}
	fpr := strings.ToUpper(hex.EncodeToString(signer.PrimaryKey.Fingerprint))
	if !fprAllowed(fpr, pinned) {
		return fpr, "signer-not-pinned"
	}
	return fpr, ""
}

func fprAllowed(fpr string, fprs []string) bool {
	for _, f := range fprs {
		if f != "" && strings.EqualFold(strings.ReplaceAll(f, " ", ""), fpr) {
			return true
		}
	}
	return false
}

var patchFailRE = regexp.MustCompile(`(?m)^(?:checking|patching) file '?([^'\n]+)'?$|^Hunk #\d+ FAILED`)

// applyPatch applies p in dir at fuzz 0 (never -N/--forward: it silently skips whole files),
// then proves every hunk landed with a reverse dry run.
func applyPatch(r *runner, dir, name, p string) *c.Error {
	out, code := r.RunIn(dir, 2*time.Minute, nil, "patch", "-p1", "-t", "--fuzz=0", "--dry-run", "-i", p)
	if code < 0 { // patch could not start: a missing tool is a build failure, not a patch conflict
		return c.Errf(c.EBuildFailed, "step", "patch-tool")
	}
	if code != 0 {
		return c.Errf(c.EPatchApply, "patch", name, "file", failedFile(out))
	}
	if out, code := r.RunIn(dir, 2*time.Minute, nil, "patch", "-p1", "-t", "--fuzz=0", "-i", p); code != 0 {
		return c.Errf(c.EPatchApply, "patch", name, "file", failedFile(out))
	}
	if out, code := r.RunIn(dir, 2*time.Minute, nil, "patch", "-p1", "-t", "--fuzz=0", "-R", "--dry-run", "-i", p); code != 0 {
		return c.Errf(c.EPatchApply, "patch", name, "file", failedFile(out), "check", "reverse")
	}
	return nil
}

// failedFile returns the file whose hunk failed, from patch's output.
func failedFile(out string) string {
	cur := ""
	for _, l := range strings.Split(out, "\n") {
		if m := patchFailRE.FindStringSubmatch(l); m != nil {
			if m[1] != "" {
				cur = m[1]
			} else {
				return cur
			}
		}
	}
	return cur
}

func reasonOf(err error) string {
	switch err.(type) {
	case errSHA:
		return "sha256"
	case errStatus:
		return "fetch-status"
	}
	if strings.Contains(err.Error(), "sha") {
		return "sha256"
	}
	return "fetch"
}

func safeName(s string) string {
	return strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(s)
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
