package verify

import (
	"bufio"
	"fmt"
	c "github.com/asafg8/Phosphor/src/contract"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Prompter asks a y/N question of a human. Confirm is false whenever no human can answer (approval is a
// y/N on a real terminal; there is no --yes and piped input is refused).
type Prompter interface {
	Confirm(question string) bool
}

// TTY is the terminal Prompter: it asks on Out and reads In, and refuses (false) when In is not a terminal.
type TTY struct {
	In  *os.File
	Out io.Writer
	br  *bufio.Reader
}

func (t *TTY) Confirm(question string) bool {
	if !IsTerminal(t.In) {
		fmt.Fprintln(t.Out, "verify: stdin is not a terminal, nothing consented (the target is not built)")
		return false
	}
	if t.br == nil {
		t.br = bufio.NewReader(t.In)
	}
	fmt.Fprint(t.Out, question)
	l, _ := t.br.ReadString('\n')
	l = strings.ToLower(strings.TrimSpace(l))
	return l == "y" || l == "yes"
}

// IsTerminal: only an interactive terminal answers the termios ioctl; /dev/null, pipes and files do not.
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	_, err := unix.IoctlGetTermios(int(f.Fd()), ioctlReadTermios)
	return err == nil
}

// buildConsent asks the one y/N verify needs: building the target installs build dependencies on this host. It
// records the answer for verify.json (nil = not asked or declined).
func (s *state) buildConsent(target string) bool {
	if s.o.NoBuild || s.o.Prompt == nil {
		return false
	}
	if !s.o.Prompt.Confirm(s.host + " · build " + target + " into a side prefix to check it: may install build dependencies on this host · y/N? ") {
		return false
	}
	s.consent = &c.VerifyConsent{By: user(), At: time.Now().UTC(), Via: "prompt"}
	return true
}

// user names who consented (the sudo caller when there is one).
func user() string {
	for _, k := range []string{"SUDO_USER", "USER", "LOGNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "unknown"
}
