// Package stage dispatches the host CLI's stages. A stage is a handler over an rt.Ctx: its args, the local files it
// may read and the out directory it writes to.
package stage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/stage/rt"
)

// Handler runs one stage. It returns the stage exit code (0 ok, 2 "no" decision, 1 error).
type Handler func(x *rt.Ctx) int

var handlers = map[string]Handler{}

// Register is called from the stage packages' init.
func Register(sub string, h Handler) { handlers[sub] = h }

// Resume runs a stage from a saved manifest: the detached watch re-executing this binary.
func Resume(sub, manifest string, detached bool) int {
	if manifest == "" {
		Fail(c.Errf(c.EUsage, "missing", "manifest"))
		return 1
	}
	x, err := rt.Load(manifest)
	if err != nil {
		Fail(err)
		return 1
	}
	if detached {
		x.M.Args["detached"] = true
	}
	return run(sub, x)
}

// Run runs one stage: args are the stage's args, gets maps a GET name to a local file, and every document the stage
// writes lands in out (with events.ndjson).
func Run(sub, out string, args map[string]any, gets map[string]string) int {
	abs, err := filepath.Abs(out)
	if err == nil {
		err = os.MkdirAll(abs, 0o700)
	}
	if err != nil {
		Fail(c.Errf(c.EUsage, "out", out))
		return 1
	}
	m := &c.Manifest{Sub: sub, Args: args, Get: map[string]string{}}
	for name, p := range gets {
		ap, err := filepath.Abs(p)
		if err != nil {
			Fail(c.Errf(c.EUsage, "get", name))
			return 1
		}
		m.Get[name] = ap
	}
	rt.LocalDir = abs
	x, err := rt.New(m)
	if err != nil {
		Fail(err)
		return 1
	}
	return run(sub, x)
}

func run(sub string, x *rt.Ctx) int {
	h, ok := handlers[sub]
	if !ok {
		Fail(c.Errf(c.EUsage, "sub", sub))
		return 1
	}
	x.Step(sub+"-start", map[string]any{"root": rt.Root})
	code := h(x)
	x.Step(sub+"-end", map[string]any{"exit": code})
	x.Flush()
	return code
}

// Fail reports err, the one error path of every part. The CLI installs a sink (SetErrorSink) that renders it as a
// text line or as the --json object's error; without a sink (a stage run on its own, the detached watch) it is one
// {"error":{"code":…,"data":{…}}} line on stderr. An error that is not a contract error is E_INTERNAL with its text.
func Fail(err error) {
	e, ok := err.(*c.Error)
	if !ok {
		e = c.Errf(c.EInternal, "detail", err.Error())
	}
	if sink != nil {
		sink(e)
		return
	}
	b, _ := json.Marshal(map[string]any{"error": e})
	fmt.Fprintln(os.Stderr, string(b))
}

var sink func(*c.Error)

// SetErrorSink routes every Fail to f (nil restores the stderr JSON line).
func SetErrorSink(f func(*c.Error)) { sink = f }
