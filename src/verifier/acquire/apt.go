// Package acquire obtains a verify target without installing anything: the fixed distro packages of a runtime
// (the `pkg-fetch` stage, P2 distro-update targets) and single .debs at an exact version through the host mirror or
// the pinned archive chain. The remediator's package-replace swap downloads its originals with the same Debs.
package acquire

import (
	"strings"
)

// SafeAptOpts force signature enforcement on regardless of host apt.conf.d (keys.lock).
var SafeAptOpts = []string{"-o", "APT::Get::AllowUnauthenticated=false", "-o", "Acquire::AllowInsecureRepositories=false",
	"-o", "Acquire::AllowDowngradeToInsecureRepositories=false"}

// Archive is one pinned distro archive (URL + suite) and the keyring that signs it.
type Archive struct {
	URL, Suite, Keyring string // Keyring is the DistroKeys name
}

// distroArchives lists, per Debian release, where the source of an installed package comes from. bullseye moved to
// archive.debian.org (security pool 404s on deb.debian.org).
var distroArchives = map[string][]Archive{
	"debian/11": {
		{URL: "http://archive.debian.org/debian", Suite: "bullseye", Keyring: "keyring:bullseye"},
		{URL: "http://archive.debian.org/debian-security", Suite: "bullseye-security", Keyring: "keyring:bullseye-security"},
	},
}

// ArchivesFor lists the pinned archives of a Debian release ("debian", "11").
func ArchivesFor(osID, osVersionID string) []Archive { return distroArchives[osID+"/"+osVersionID] }

// VerifiedAptOpts is every apt invocation that touches the pinned archives: our own list only, host keyrings
// ignored, signature enforcement forced on regardless of host apt.conf.d (keys.lock how_to_verify.deb).
func VerifiedAptOpts(list string) []string {
	return append(append([]string{}, SafeAptOpts...), "-o", "Dir::Etc::sourcelist="+list, "-o", "Dir::Etc::sourceparts=-",
		"-o", "Dir::Etc::trustedparts=/nonexistent", "-o", "Acquire::Check-Valid-Until=false")
}

// unverifiedMarkers in apt output mean the archive chain did not verify.
var unverifiedMarkers = []string{"NO_PUBKEY", "is not signed", "not signed", "cannot be authenticated", "BADSIG", "The repository", "InRelease' is not signed"}

// Unverified reports whether apt's output shows a signature that did not verify.
func Unverified(aptOut string) bool {
	for _, n := range unverifiedMarkers {
		if strings.Contains(aptOut, n) {
			return true
		}
	}
	return false
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func shqAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = shq(x)
	}
	return out
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}
