package inspect

import (
	"bytes"
	"debug/elf"
	"strings"

	c "github.com/asafg8/Phosphor/src/contract"
)

// fixFingerprint is one branch's markers of the php2shell fix: present only in fixed builds.
type fixFingerprint struct {
	Branch    string   `json:"branch"`     // "7.4" or "8"
	AppliesTo []string `json:"applies_to"` // minors the markers were verified on ("8": 8.2–8.5); empty = the branch
	Symbols   []string `json:"symbols"`
	Rodata    []string `json:"rodata"`
}

// defaultFixFingerprints is the fallback when the inspect args carry no `fix_fingerprints` (data/fix-fingerprints.lock).
var defaultFixFingerprints = []fixFingerprint{
	{Branch: "7.4", Symbols: []string{"zend_mm_safe_error_ex", "zend_new_array_singleton"}}, // 712e60f / 0164
	{Branch: "8", AppliesTo: []string{"8.2", "8.3", "8.4", "8.5"}, Symbols: []string{"zend_set_memory_hard_limit_percent"}, Rodata: []string{"Hard memory limit of"}},
}

// binaryFingerprint reads the runtime's ELF (php/php-fpm binary or libphp*.so) for its branch's markers: defined
// .dynsym symbols (an UND reference does not count) and .rodata strings, no execution; matched = all of the
// branch's markers (data/fix-fingerprints.lock: identical on stripped copies). nil when the branch has no markers or
// the file isn't ELF.
func binaryFingerprint(path, version string, fps []fixFingerprint) *c.BinaryFingerprint {
	var fp *fixFingerprint
	for i := range fps {
		if !strings.HasPrefix(version, fps[i].Branch+".") {
			continue
		}
		ok := len(fps[i].AppliesTo) == 0
		for _, m := range fps[i].AppliesTo {
			ok = ok || strings.HasPrefix(version, m+".")
		}
		if ok { // 8.0/8.1 have no verified markers: no fingerprint, the lock alone decides
			fp = &fps[i]
		}
	}
	if fp == nil {
		return nil
	}
	f, err := elf.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := &c.BinaryFingerprint{Method: "elf", Markers: []string{}}
	defined := map[string]bool{}
	if syms, err := f.DynamicSymbols(); err == nil {
		for _, s := range syms {
			if s.Section != elf.SHN_UNDEF {
				defined[s.Name] = true
			}
		}
	}
	var rodata []byte
	if sec := f.Section(".rodata"); sec != nil {
		rodata, _ = sec.Data()
	}
	for _, m := range fp.Symbols {
		if defined[m] {
			out.Markers = append(out.Markers, m)
		}
	}
	for _, m := range fp.Rodata {
		if bytes.Contains(rodata, []byte(m)) {
			out.Markers = append(out.Markers, m)
		}
	}
	out.Matched = len(out.Markers) == len(fp.Symbols)+len(fp.Rodata)
	return out
}
