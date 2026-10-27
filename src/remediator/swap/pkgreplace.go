package swap

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
	"github.com/asafg8/Phosphor/src/remediator/watch"
	"github.com/asafg8/Phosphor/src/verifier/acquire"
	"github.com/asafg8/Phosphor/src/verifier/preflight"
)

// deb is one built package of a distro-source build (<prefix>/debs).
type deb struct {
	Path, Pkg, Version, Installed string
}

// packageReplace is install model package-replace: the `+phosphor1` debs of a distro-source build replace the
// installed packages in place. Before any change every original .deb (exact installed version) is cached under
// <root>/backup/<run>/ on the host; one missing → no-rollback-artifact, nothing installed. Then one
// `dpkg -i`, the package hold (guardPackages), one restart per serving unit. Rollback = the recorded inverse
// `dpkg -i <originals>` + unhold + the same restart (≤ 60 s budget). dpkg downgrades by
// default; --allow-downgrades is apt's flag and makes dpkg fail the undo.
func (j *job) packageReplace() *c.Error {
	debs, err := j.replaceSet()
	if err != nil {
		return err
	}
	originals, err := j.cacheOriginals(debs)
	if err != nil {
		return err
	}
	var newDebs, names []string
	for _, d := range debs {
		newDebs = append(newDebs, d.Path)
		names = append(names, d.Pkg)
	}
	before := map[string]uint64{}
	for _, ra := range j.a.Runtimes {
		before[ra.RuntimeID] = servingInode(ra.Runtime)
	}
	if e := j.e.Exec("exec", "dpkg", append([]string{"dpkg", "-i"}, newDebs...),
		append([]string{"dpkg", "-i"}, originals...), // never --allow-downgrades
		map[string]any{"op": "package-replace", "packages": names, "version": j.a.NewVersion}); e != nil {
		return e
	}
	for _, ra := range sortedForSwap(j.a.Runtimes) {
		r := ra.Runtime
		switch r.SAPI {
		case "fpm":
			if r.Service.Manager == "systemd" && r.Service.Unit != "" {
				j.reload = append(j.reload, "systemctl restart "+r.Service.Unit)
			}
		case "mod_php":
			cmds, e := j.apacheStopStart(preflight.ApacheCtl())
			if e != nil {
				return e
			}
			j.reload = append(j.reload, cmds...)
		}
		ra := ra
		old := before[ra.RuntimeID]
		j.verify = append(j.verify, verifier{ra.RuntimeID, func() (bool, map[string]any) { return j.verifyReplaced(ra.Runtime, old, debs) }})
	}
	for _, cs := range j.a.Consumers {
		if cs.Kind == "systemd" && cs.Path != "" {
			j.reload = append(j.reload, "systemctl restart "+cs.Path)
		}
	}
	// watch: any +phosphor1 package changing version under us is swap-reverted
	j.watchVerify = []string{"sh", "-c", "dpkg-query -W -f='${Version}\\n' " + debs[0].Pkg + " | grep -q 'phosphor1$'"}
	j.doc.PatchID = patchIDOf(j.a.NewVersion)
	return nil
}

// replaceSet is every built deb whose package is installed (ii) on this host, sorted by name.
func (j *job) replaceSet() ([]deb, *c.Error) {
	files, _ := filepath.Glob(filepath.Join(j.a.Prefix, "debs", "*.deb"))
	var out []deb
	for _, f := range files {
		pkg, _ := sys.Run(30*time.Second, nil, "dpkg-deb", "-f", f, "Package")
		ver, _ := sys.Run(30*time.Second, nil, "dpkg-deb", "-f", f, "Version")
		pkg, ver = strings.TrimSpace(pkg), strings.TrimSpace(ver)
		st, _ := sys.Run(15*time.Second, nil, "dpkg-query", "-W", "-f=${Status}\t${Version}", pkg)
		parts := strings.SplitN(strings.TrimSpace(st), "\t", 2)
		if len(parts) != 2 || parts[0] != "install ok installed" {
			continue
		}
		out = append(out, deb{Path: f, Pkg: pkg, Version: ver, Installed: parts[1]})
	}
	if len(out) == 0 {
		return nil, c.Errf(c.ESwapFailed, "reason", "no-installed-package-in-build", "prefix", j.a.Prefix)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Pkg < out[k].Pkg })
	return out, nil
}

// cacheOriginals downloads the exact installed version of every package to <root>/backup/<run>/ (kept on the host)
// and sha256s it. Any failure → no-rollback-artifact (nothing installed yet).
func (j *job) cacheOriginals(debs []deb) ([]string, *c.Error) {
	dir := filepath.Join(rt.Root, "backup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, c.Errf(c.ESwapFailed, "reason", "no-rollback-artifact", "step", "backup-dir")
	}
	if len(debs) > len(j.a.BackupSlots) {
		return nil, c.Errf(c.ESwapFailed, "reason", "no-rollback-artifact", "step", "slots", "need", len(debs), "have", len(j.a.BackupSlots))
	}
	defer os.RemoveAll(filepath.Join(rt.Root, "keys", "swap")) // private apt state, never left behind
	var files []string
	for i, d := range debs {
		f, via, _, e := j.debs().Fetch(dir, d.Pkg, d.Installed)
		if e != nil {
			return nil, c.Errf(c.ESwapFailed, "reason", "no-rollback-artifact", "pkg", d.Pkg, "version", d.Installed, "detail", e.Data)
		}
		sum := sys.SHA256File(f)
		j.x.Step("original-cached", map[string]any{"pkg": d.Pkg, "version": d.Installed, "via": via, "sha256": sum})
		j.doc.Backup = append(j.doc.Backup, c.PkgBackup{Pkg: d.Pkg, Version: d.Installed, SHA256: sum, Key: j.a.BackupSlots[i]})
		files = append(files, f)
	}
	j.x.Step("originals-cached", map[string]any{"packages": len(files), "dir": dir})
	return files, nil
}

// verifyReplaced is the package-replace proof: php -v has phosphor1, every package at the new version, dpkg --verify
// clean, and the serving binary's inode changed (the process runs the replaced file).
func (j *job) verifyReplaced(r c.Runtime, oldInode uint64, debs []deb) (bool, map[string]any) {
	ev := map[string]any{"runtime": r.ID}
	ok := poll(30*time.Second, func() bool {
		ino := servingInode(r)
		ev["inode_before"], ev["inode_after"] = oldInode, ino
		return ino != 0 && ino != oldInode
	})
	bad := []string{}
	for _, d := range debs {
		v, _ := sys.Run(15*time.Second, nil, "dpkg-query", "-W", "-f=${Version}", d.Pkg)
		if strings.TrimSpace(v) != d.Version {
			bad = append(bad, d.Pkg+"="+strings.TrimSpace(v))
		}
	}
	ev["version_mismatch"] = bad
	ok = ok && len(bad) == 0
	var names []string
	for _, d := range debs {
		names = append(names, d.Pkg)
	}
	vout, _ := sys.Combined(2*time.Minute, nil, "dpkg", append([]string{"--verify"}, names...)...)
	ev["dpkg_verify"] = strings.TrimSpace(vout)
	ok = ok && strings.TrimSpace(vout) == ""
	bin := r.Binary // php / php-fpm print -v; libphp is checked through its CLI
	if r.SAPI == "mod_php" {
		bin = r.CLIBinary
	}
	if bin != "" {
		pv, _ := sys.Combined(20*time.Second, nil, bin, "-v")
		ev["php_v"] = firstLine(pv)
		ok = ok && strings.Contains(pv, "phosphor1")
	}
	return ok, ev
}

// servingInode is the inode of what serves the runtime: fpm master /proc/<pid>/exe, an apache child's libphp from
// its maps, else the binary file itself.
func servingInode(r c.Runtime) uint64 {
	switch r.SAPI {
	case "fpm":
		if r.Service.Unit != "" {
			pid, _ := sys.Combined(10*time.Second, nil, "systemctl", "show", "-p", "MainPID", "--value", r.Service.Unit)
			if p := strings.TrimSpace(pid); p != "" && p != "0" {
				return inodeOf("/proc/" + p + "/exe")
			}
		}
	case "mod_php":
		for _, pid := range watch.ApacheWorkers() {
			for _, l := range sys.MapsLibsRaw(pid) {
				if strings.HasPrefix(strings.TrimSuffix(l, " (deleted)"), r.Binary) {
					if strings.HasSuffix(l, " (deleted)") {
						return 1 // still the replaced (old) file
					}
					return inodeOf(r.Binary)
				}
			}
		}
		return 0
	}
	return inodeOf(r.Binary)
}

func inodeOf(p string) uint64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(s.Ino)
	}
	return 0
}

// patchIDOf names the applied fix from the version (the build chose the patch; build.json has it as target.patchset).
func patchIDOf(v string) string {
	if strings.Contains(v, "+phosphor") {
		return "distro-source+phosphor"
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// debs is the original-package downloader (host mirror, else the pinned archive chain; src/verifier/acquire), prepared
// once per swap.
func (j *job) debs() *acquire.Debs {
	if j.dl == nil {
		j.dl = &acquire.Debs{X: j.x, Host: j.a.Host, Keys: j.a.DistroKeys}
	}
	return j.dl
}
