package main

import (
	_ "embed"
	"io"
	"os"

	"github.com/asafg8/Phosphor/src/verifier/verify"
)

// banner is the Mars Pathfinder rover, Sojourner (ASCII only; kept in a file because it contains backquotes).
//
//go:embed banner.txt
var banner string

// printBanner writes the banner when w is an interactive terminal: piped or redirected text output keeps the
// output contract's first line (phosphor <version> · <cmd> · host <hostname>), and --json never gets it.
func printBanner(w io.Writer) {
	if f, ok := w.(*os.File); ok && verify.IsTerminal(f) {
		io.WriteString(w, banner+"\n")
	}
}
