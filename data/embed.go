// Package data embeds the pinned data files so the binary needs no checkout. research/ is not embedded.
package data

import "embed"

//go:embed *.lock known-flaky.txt expected-fail keys distro-keys
var FS embed.FS
