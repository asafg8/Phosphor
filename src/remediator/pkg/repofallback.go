package pkg

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Fallback is a temporary sources list for a distro whose release moved to an archive mirror.
type Fallback struct {
	Host        string
	SourcesList string
}

var debianCodename = map[string]string{"9": "stretch", "10": "buster", "11": "bullseye"}

// AptFallback returns the archive mirror sources for EOL Debian releases (verified: archive.debian.org serves
// bullseye + bullseye-security). Ubuntu non-LTS EOL releases use old-releases.ubuntu.com. nil when none applies.
func AptFallback(h c.HostFacts) *Fallback {
	switch h.OSID {
	case "debian":
		code, ok := debianCodename[h.OSVersionID]
		if !ok {
			return nil
		}
		return &Fallback{Host: "archive.debian.org", SourcesList: "deb http://archive.debian.org/debian " + code + " main\n" +
			"deb http://archive.debian.org/debian-security " + code + "-security main\n"}
	case "ubuntu":
		code := ubuntuCodename[h.OSVersionID]
		if code == "" {
			return nil
		}
		return &Fallback{Host: "old-releases.ubuntu.com", SourcesList: "deb http://old-releases.ubuntu.com/ubuntu " + code + " main universe\n" +
			"deb http://old-releases.ubuntu.com/ubuntu " + code + "-updates main universe\n" +
			"deb http://old-releases.ubuntu.com/ubuntu " + code + "-security main universe\n"}
	}
	return nil
}

// ubuntuCodename lists only interim releases that moved to old-releases (LTS releases stay on archive.ubuntu.com).
var ubuntuCodename = map[string]string{"21.04": "hirsute", "21.10": "impish", "22.10": "kinetic", "23.04": "lunar", "23.10": "mantic"}

// ConfiguredMirror is the host of the first enabled apt source (e.g. deb.debian.org): the "from" of a repo fallback.
// It reads /etc/apt/sources.list, *.list and deb822 *.sources; it never writes them.
func ConfiguredMirror(etcApt string) string {
	files := []string{filepath.Join(etcApt, "sources.list")}
	more, _ := filepath.Glob(filepath.Join(etcApt, "sources.list.d", "*"))
	files = append(files, more...)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			var u string
			switch {
			case strings.HasPrefix(l, "deb ") || strings.HasPrefix(l, "deb-src "):
				for _, w := range strings.Fields(l)[1:] {
					if strings.Contains(w, "://") {
						u = w
						break
					}
				}
			case strings.HasPrefix(l, "URIs:"):
				if f := strings.Fields(strings.TrimPrefix(l, "URIs:")); len(f) > 0 {
					u = f[0]
				}
			}
			if pu, err := url.Parse(u); err == nil && pu.Host != "" {
				return pu.Host
			}
		}
	}
	return ""
}
