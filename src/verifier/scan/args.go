package scan

import (
	"regexp"
	"slices"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
)

// The argument builders below derive the `scan` and `readiness` stage args from a fingerprint for `phosphor verify`.

// PrimaryRuntime is the runtime a build/scan is planned from: fpm → mod_php → cgi → cli → container.
func PrimaryRuntime(fp *c.Fingerprint) *c.Runtime {
	if fp == nil || len(fp.Runtimes) == 0 {
		return nil
	}
	best := &fp.Runtimes[0]
	for i := range fp.Runtimes {
		if fp.Runtimes[i].SAPI.Rank() < best.SAPI.Rank() {
			best = &fp.Runtimes[i]
		}
	}
	return best
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug names a root's raw report (raw/readiness-<slug>.json).
func Slug(p string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(p), "-"), "-")
}

// Roots are the asset's code roots minus nested third-party entries (they are classified inside their parent).
func Roots(fp *c.Fingerprint) []Root { return RootsOf(fp, nil) }

// RootsOf are the code roots of the runtimes in ids (every runtime when ids is nil), as Roots.
func RootsOf(fp *c.Fingerprint, ids []string) []Root {
	var out []Root
	seen := map[string]bool{}
	for _, rt := range fp.Runtimes {
		if ids != nil && !slices.Contains(ids, rt.ID) {
			continue
		}
		for _, cr := range rt.CodeRoots {
			if seen[cr.Path] {
				continue
			}
			nested := false
			for _, o := range rt.CodeRoots {
				if o.Path != cr.Path && strings.HasPrefix(cr.Path, o.Path+"/") {
					nested = true
				}
			}
			if nested {
				continue
			}
			seen[cr.Path] = true
			out = append(out, Root{Path: cr.Path, Kind: string(cr.Kind), ThirdPartyPaths: cr.ThirdPartyPaths, Slug: Slug(cr.Path)})
		}
	}
	return out
}

// Args are the `scan` stage args for the primary runtime at target (the migration target).
func Args(prim *c.Runtime, target string, roots []Root) map[string]any {
	return map[string]any{"runtime_id": prim.ID, "target_version": target, "roots": roots}
}

// ReadinessArgs are the `readiness` stage args; bundleSHA is the sha256 of the scan bundle behind GET "scanbundle".
func ReadinessArgs(prim *c.Runtime, target string, roots []Root, bundleSHA string, budget time.Duration, firstOnly bool) map[string]any {
	return map[string]any{"runtime_id": prim.ID, "target_version": target, "roots": roots, "scanbundle_sha256": bundleSHA,
		"budget_s": int(budget.Seconds()), "first_party_only": firstOnly}
}
