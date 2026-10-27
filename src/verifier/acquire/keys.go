package acquire

import (
	"encoding/json"
	"os"
	"path/filepath"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/assets"
)

// keyFiles maps the keyring names of distroArchives to data/distro-keys files.
var keyFiles = map[string]string{
	"keyring:bullseye":          "debian-archive-bullseye-automatic.gpg",
	"keyring:bullseye-security": "debian-archive-bullseye-security-automatic.gpg",
}

// DistroKeys writes the pinned archive keyrings into dir and returns them as Debs/FetchArgs keys (sha256 from
// keys.lock) plus the GET slots that point at the written files (stage.Run gets).
func DistroKeys(dir string) ([]c.BuildSource, map[string]string, error) {
	b, err := assets.ReadFile("data/distro-keys/keys.lock")
	if err != nil {
		return nil, nil, err
	}
	var l struct {
		Keys []struct{ File, SHA256 string } `json:"keys"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, nil, err
	}
	sums := map[string]string{}
	for _, k := range l.Keys {
		sums[k.File] = k.SHA256
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	var out []c.BuildSource
	get := map[string]string{}
	for _, name := range []string{"keyring:bullseye", "keyring:bullseye-security"} {
		f := keyFiles[name]
		raw, err := assets.ReadFile("data/distro-keys/" + f)
		if err != nil {
			return nil, nil, err
		}
		dst := filepath.Join(dir, f)
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			return nil, nil, err
		}
		get[name] = dst
		out = append(out, c.BuildSource{Name: name, Kind: "keyring", Get: name, SHA256: sums[f], Verify: "sha256"})
	}
	return out, get, nil
}
