package inspect

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// pkgOwner returns the owning package and version of a file via dpkg or rpm ("" if unowned).
func pkgOwner(path string) (pkg, version, extra string) {
	cands := []string{path}
	if rp, err := filepath.EvalSymlinks(path); err == nil && rp != path {
		cands = append(cands, rp)
	}
	// usrmerge: dpkg may know /lib/... while the file is at /usr/lib/...
	for _, p := range append([]string(nil), cands...) {
		if strings.HasPrefix(p, "/usr/lib/") {
			cands = append(cands, strings.TrimPrefix(p, "/usr"))
		}
	}
	if sys.Which("dpkg-query") != "" {
		for _, p := range cands {
			out, err := sys.Run(10*time.Second, nil, "dpkg-query", "-S", p)
			if err != nil || out == "" {
				continue
			}
			first := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
			name, _, ok := strings.Cut(first, ": ")
			if !ok {
				continue
			}
			name = strings.Split(name, ",")[0]
			name = strings.Split(name, ":")[0] // multiarch qualifier
			v, _ := sys.Run(10*time.Second, nil, "dpkg-query", "-W", "-f", "${Version}", name)
			return name, strings.TrimSpace(v), ""
		}
	}
	if sys.Which("rpm") != "" {
		for _, p := range cands {
			out, err := sys.Run(10*time.Second, nil, "rpm", "-qf", "--qf", "%{NAME}\t%{VERSION}-%{RELEASE}\t%{VENDOR}\n", p)
			if err != nil || strings.Contains(out, "not owned") {
				continue
			}
			f := strings.Split(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0], "\t")
			if len(f) >= 2 {
				vendor := ""
				if len(f) > 2 {
					vendor = f[2]
				}
				return f[0], f[1], vendor
			}
		}
	}
	return "", "", ""
}

func provenanceOf(bin string, osID string) c.Provenance {
	pkg, ver, vendor := pkgOwner(bin)
	if v := vendorRepo(bin, pkg, vendor, osID); v != "" {
		// vendor builds are classified only via advisories.lock entries, never by version compare
		return c.Provenance{Kind: "vendor", Pkg: pkg, PkgVersion: ver, Repo: v}
	}
	if pkg == "" {
		if strings.HasPrefix(bin, "/opt/bitnami/") || sys.IsDir("/opt/bitnami/php") && strings.HasPrefix(bin, "/opt/bitnami") {
			return c.Provenance{Kind: "bitnami"}
		}
		return c.Provenance{Kind: "custom", Repo: "unknown"}
	}
	repo := repoOf(pkg, ver, osID)
	if repo == "debian" && aptOrigin(pkg, "deb.freexian.com") {
		repo = "elts" // Freexian Extended LTS for an EOL Debian release
	}
	return c.Provenance{Kind: "distro-pkg", Pkg: pkg, PkgVersion: ver, Repo: repo}
}

// vendorRepo recognises hosting-panel / vendor PHP builds by install prefix, package name, rpm vendor or OS.
func vendorRepo(bin, pkg, rpmVendor, osID string) c.Repo {
	v := strings.ToLower(rpmVendor)
	switch {
	case strings.HasPrefix(bin, "/opt/cpanel/ea-php"), strings.HasPrefix(pkg, "ea-php"), strings.Contains(v, "cpanel"):
		return "cpanel-ea"
	case strings.HasPrefix(bin, "/opt/plesk/php/"), strings.HasPrefix(pkg, "plesk-php"), strings.Contains(v, "plesk"):
		return "plesk"
	case strings.HasPrefix(bin, "/opt/alt/php"), strings.HasPrefix(pkg, "alt-php"):
		return "cloudlinux-alt"
	case osID == "wolfi" || osID == "chainguard":
		return "chainguard"
	}
	return ""
}

// aptOrigin reports whether the installed version of pkg comes from a source whose URL contains host
// (`apt-cache policy` version table: the "***" line, then its origins).
func aptOrigin(pkg, host string) bool {
	if sys.Which("apt-cache") == "" {
		return false
	}
	out, err := sys.Run(30*time.Second, nil, "apt-cache", "policy", pkg)
	if err != nil {
		return false
	}
	return installedFrom(out, host)
}

func installedFrom(policy, host string) bool {
	in := false
	for _, l := range strings.Split(policy, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "***"):
			in = true
		case in && strings.HasPrefix(l, "        ") && !strings.HasPrefix(l, "     ***"):
			if strings.Contains(t, host) {
				return true
			}
		case in:
			return false
		}
	}
	return false
}

// Sury's Debian builds carry "+0~<date>.<n>+debian<N>~1.gbp<hash>" (no "sury" in the version), PPA builds "deb.sury.org".
var suryVerRe = regexp.MustCompile(`\+0~\d{8}\.\d+\+debian\d+~`)

var elRepo = map[string]c.Repo{"almalinux": "alma", "rocky": "rocky", "rhel": "rhel"}

func repoOf(pkg, ver, osID string) c.Repo {
	v := strings.ToLower(ver)
	switch {
	case strings.Contains(v, "sury"), suryVerRe.MatchString(v):
		return "ondrej"
	case strings.Contains(v, "remi") || strings.Contains(v, ".remi"):
		return "remi"
	case strings.Contains(v, "amzn2023"):
		return "amzn"
	case strings.Contains(v, "amzn2"):
		if amznExtras(pkg) {
			return "amzn-extras"
		}
		return "amzn"
	case osID == "almalinux" || osID == "rocky" || osID == "rhel":
		return elRepo[osID] // AppStream/BaseOS module builds (…module_el8…); EPEL ships no php
	case strings.Contains(v, "ubuntu"):
		return "ubuntu"
	case strings.Contains(v, "deb"):
		return "debian"
	case strings.Contains(v, ".el"):
		return "epel"
	}
	switch osID {
	case "debian":
		return "debian"
	case "ubuntu":
		return "ubuntu"
	case "amzn":
		return "amzn"
	}
	return "unknown"
}

// amznExtras checks yumdb's from_repo for an amzn2extra-* repo (AL2 amazon-linux-extras topics).
func amznExtras(pkg string) bool {
	ms, _ := filepath.Glob("/var/lib/yum/yumdb/*/*-" + pkg + "-*/from_repo")
	for _, m := range ms {
		if b, err := os.ReadFile(m); err == nil && strings.Contains(string(b), "amzn2extra") {
			return true
		}
	}
	return false
}

// extSource classifies a third-party .so: owned by a package → pkg; registered in a PECL registry → pecl.
func extSource(name, so string) (c.ExtSource, string) {
	if pkg, _, _ := pkgOwner(so); pkg != "" {
		return "pkg", pkg
	}
	regs := []string{}
	// a custom prefix keeps its own registry: <prefix>/lib/php/extensions/<api>/x.so → <prefix>/lib/php/.registry (i)
	if i := strings.Index(so, "/lib/php/extensions/"); i > 0 {
		regs = append(regs, so[:i]+"/lib/php/.registry/.channel.pecl.php.net/"+name+".reg")
	}
	for _, g := range append(regs, "/usr/share/php/.registry/.channel.pecl.php.net/"+name+".reg",
		"/usr/local/lib/php/.registry/.channel.pecl.php.net/"+name+".reg",
		"/usr/share/pear/.registry/.channel.pecl.php.net/"+name+".reg",
		"/var/lib/pear/.registry/.channel.pecl.php.net/"+name+".reg") {
		if sys.Exists(g) {
			return "pecl", ""
		}
	}
	// built by hand with phpize from the PECL tarball (no registry): known PECL package names
	if peclNames[strings.ToLower(name)] {
		return "pecl", ""
	}
	return "unknown", ""
}

// peclNames are widely deployed pecl.php.net packages (extension name = package name).
var peclNames = wordSet(`redis imagick apcu memcached memcache xdebug mongodb igbinary msgpack yaml ssh2 mailparse
	oauth uploadprogress geoip amqp rdkafka grpc protobuf swoole event ev uuid xhprof pcov ds zstd lzf mcrypt sqlsrv pdo_sqlsrv
	oci8 gnupg inotify solr stomp timezonedb zip`)
