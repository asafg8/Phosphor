// Package version is stamped by the Makefile (-X): Describe = git describe, Build = hash of the Go sources.
package version

var (
	Describe = "dev"
	Build    = "0"
)

// String is the phosphor version (first line of every command, the documents' phosphor_version).
func String() string { return Describe + "-" + Build }
