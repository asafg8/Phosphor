package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"
	"sort"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/sys"
)

// Binding is what a verdict is bound to (verify_hash): the runtime's plan.json row and the host state the checks
// read (StateHash). build and swap pass the same two to Current; any change since verify makes the verdict stale
// (an ini added to the scan dir after verify must not leave an old ready in force).
func Binding(row c.PlanRuntime, fr c.Runtime) string {
	h := sha256.Sum256([]byte(PlanRowHash(row) + "|" + StateHash(fr)))
	return hex.EncodeToString(h[:])
}

// StateHash digests the runtime's facts that decide a verdict: SAPI, binary and version, loaded modules, the service
// command, and the live content of every file the runtime's config is read from (the binary, php.ini, each scanned
// ini plus whatever *.ini now sits in the scan dir, the service's config files, the shared extensions). Files are
// read now, so a change is caught even before the next `phosphor find`; process ids and timings are left out.
func StateHash(fr c.Runtime) string {
	files := []string{fr.Binary, fr.Ini.LoadedFile}
	files = append(files, fr.Ini.ScannedFiles...)
	if fr.Ini.ScanDir != "" {
		live, _ := filepath.Glob(filepath.Join(fr.Ini.ScanDir, "*.ini"))
		files = append(files, live...)
	}
	files = append(files, fr.Service.ConfFiles...)
	for _, e := range fr.SharedExts {
		files = append(files, e.SOPath)
	}
	sums := map[string]string{}
	for _, f := range files {
		if f != "" {
			sums[f] = sys.SHA256File(f)
		}
	}
	b, _ := json.Marshal(struct {
		SAPI                       c.SAPI
		Binary, BinarySHA, Version string
		Modules, Zend              []string
		ExecStart, Unit, ExtDir    string
		Files                      map[string]string
	}{fr.SAPI, fr.Binary, fr.BinarySHA256, fr.Version, sorted(fr.Modules), sorted(fr.ZendExtensions), fr.Service.ExecStart,
		fr.Service.Unit, fr.ExtensionDir, sums})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func sorted(xs []string) []string {
	out := slices.Clone(xs)
	sort.Strings(out)
	return out
}
