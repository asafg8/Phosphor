package contract

const SchemaPkgFetch = "phosphor/pkg-fetch/v1"

// PkgFetchArgs is manifest.args["fetch"] for sub=pkg-fetch: the runtime's installed binary package and the fixed
// version to fetch candidates for. Producer: src/verifier/verify. Consumer: src/verifier/acquire.
type PkgFetchArgs struct {
	RuntimeID string `json:"runtime_id"`
	// Package is the runtime's installed binary package (provenance.pkg, e.g. php7.4-fpm / php-fpm): the candidate
	// is every installed package built from the SAME source package (never by name prefix).
	Package string `json:"package"`
	// Version is the fixed version (advisories.lock).
	Version    string        `json:"version"`
	Host       HostFacts     `json:"host"`
	DistroKeys []BuildSource `json:"distro_keys,omitempty"`
}

// PkgFetch is DIR/pkg-fetch.json from `phosphor verify`: the P2 (distro-update) candidate packages
// downloaded, never installed, for verification; where they came from and the verified sha256 of each. Producer:
// the verifier (`pkg-fetch` stage, src/verifier/acquire). Consumers: preflight (it reads <dir>/debs), swap.
type PkgFetch struct {
	Envelope
	RuntimeID string `json:"runtime_id"`
	Dir       string `json:"dir"` // the candidate dir: <dir>/debs holds the packages (preflight layout)
	Version   string `json:"version"`
	// Source: archive-chain | host-mirror | host-repo-gpg (RPM), or mixed when packages came from more than one.
	Source string `json:"source"`
	// SkippedHostMirrorReason: why the host mirror was not used (host-apt-untrusted | host-mirror-unavailable |
	// host-sha-mismatch); empty when it was used for every package.
	SkippedHostMirrorReason string            `json:"skipped_host_mirror_reason,omitempty"`
	Packages                []PkgFetchPackage `json:"packages"`
}

type PkgFetchPackage struct {
	Pkg     string `json:"pkg"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"` // equal to the signed index (deb) / of an rpmkeys-checked file (rpm)
	Via     string `json:"via"`    // archive-chain | host-mirror | host-repo-gpg
}
