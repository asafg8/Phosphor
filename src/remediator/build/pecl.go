package build

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// thirdPartyExts rebuilds every third-party extension against the new build from the pinned PECL release, or reuses
// the old .so (same minor: the CVE diff is ABI-additive) — never `failed` for a same-minor build.
// fetchFn downloads a PECL tarball (local GET entry or origin URL) and verifies its sha256.
type fetchFn func(p c.PECLRef, dst string) error

func thirdPartyExts(r *runner, g *guardrails, rtm c.Runtime, pecl []c.PECLRef, fetch fetchFn, work, prefix, extDir string) ([]c.ExtBuild, []c.Reason) {
	var out []c.ExtBuild
	var reasons []c.Reason
	byName := map[string]c.PECLRef{}
	for _, p := range pecl {
		byName[p.Name] = p
	}
	for _, e := range rtm.ThirdPartyExts {
		if p, ok := byName[e.Name]; ok {
			if so, err := buildPECL(r, g, p, fetch, work, prefix); err == nil {
				dst := filepath.Join(extDir, e.Name+".so")
				if copyFile(so, dst, 0o755) == nil {
					out = append(out, c.ExtBuild{Name: e.Name, Version: p.Version, Action: "rebuilt", SourceURL: firstNonEmpty(p.URL, p.Get), SHA256: p.SHA256})
					continue
				}
			}
		}
		if e.SOPath != "" && copyFile(e.SOPath, filepath.Join(extDir, filepath.Base(e.SOPath)), 0o755) == nil {
			out = append(out, c.ExtBuild{Name: e.Name, Version: e.Version, Action: "reused", SHA256: e.SHA256})
			reasons = append(reasons, "ext-reused")
			continue
		}
		out = append(out, c.ExtBuild{Name: e.Name, Version: e.Version, Action: "failed"})
	}
	for _, s := range rtm.SharedExts {
		if s.Bundled {
			out = append(out, c.ExtBuild{Name: s.Name, Action: "bundled"})
		}
	}
	return out, reasons
}

func buildPECL(r *runner, g *guardrails, p c.PECLRef, fetch fetchFn, work, prefix string) (string, error) {
	dir := filepath.Join(work, "pecl-"+p.Name)
	_ = os.MkdirAll(dir, 0o755)
	tgz := dir + ".tgz"
	if err := fetch(p, tgz); err != nil {
		return "", err
	}
	if _, code := r.RunIn(dir, 5*time.Minute, nil, "tar", "-xzf", tgz, "--strip-components=1", "--exclude=package.xml"); code != 0 {
		return "", errStatus(code)
	}
	if _, code := r.RunIn(dir, 5*time.Minute, nil, filepath.Join(prefix, "bin", "phpize")); code != 0 {
		return "", errStatus(code)
	}
	if _, code := r.Heavy(dir, 15*time.Minute, nil, "./configure", "--with-php-config="+filepath.Join(prefix, "bin", "php-config")); code != 0 {
		return "", errStatus(code)
	}
	if _, code := r.Heavy(dir, 30*time.Minute, nil, "make", "-j"+strconv.Itoa(g.Jobs)); code != 0 {
		return "", errStatus(code)
	}
	so := filepath.Join(dir, "modules", p.Name+".so")
	if !sys.Exists(so) {
		return "", errStatus(-1)
	}
	return so, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	return os.WriteFile(dst, b, mode)
}
