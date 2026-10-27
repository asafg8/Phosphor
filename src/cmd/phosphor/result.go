package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
)

// fileStamp tells a file this command wrote from one an earlier command left.
type fileStamp struct {
	size int64
	mod  time.Time
}

// snapshot lists the out directory's files (data/ is the extracted copy of the built-in data, not a document).
func snapshot(out string) map[string]fileStamp {
	m := map[string]fileStamp{}
	_ = filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if rel, _ := filepath.Rel(out, p); rel == "data" {
				return filepath.SkipDir
			}
			return nil
		}
		if internal(d.Name()) {
			return nil
		}
		if info, err := d.Info(); err == nil {
			m[p] = fileStamp{info.Size(), info.ModTime()}
		}
		return nil
	})
	return m
}

// internal are the runtime's own files in out, not documents: the event seq counter and partial writes.
func internal(name string) bool {
	return name == "seq" || strings.HasSuffix(name, ".part")
}

// written are the files new or changed since before (sorted; never nil).
func written(out string, before map[string]fileStamp) []string {
	paths := []string{}
	for p, st := range snapshot(out) {
		if old, ok := before[p]; !ok || old != st {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

func wrote(docs []string, name string) bool {
	for _, p := range docs {
		if filepath.Base(p) == name {
			return true
		}
	}
	return false
}

func readDoc(out, name string, v any) bool {
	b, err := os.ReadFile(filepath.Join(out, name))
	return err == nil && json.Unmarshal(b, v) == nil
}

// summarize is the command's outcome read back from the documents in out: the host's runtimes (with the verdict
// after verify) and, for build/swap/watch/rollback, the change.
func summarize(name, out string, docs []string, code int) c.CliSummary {
	s := c.CliSummary{Runtimes: []c.CliRuntime{}}
	var fp c.Fingerprint
	if readDoc(out, "fingerprint.json", &fp) {
		for _, r := range fp.Runtimes {
			cr := c.CliRuntime{RuntimeID: r.ID, SAPI: r.SAPI, Version: r.Version, Binary: r.Binary, Provenance: provenanceKind(r.Provenance),
				Package: r.Provenance.Pkg, Repo: string(r.Provenance.Repo), Service: service(r.Service), CodeRoots: len(r.CodeRoots)}
			if len(r.CodeRoots) > 0 {
				cr.FirstCodeRoot = r.CodeRoots[0].Path
			}
			s.Runtimes = append(s.Runtimes, cr)
		}
	}
	var v c.Verify
	if name == "verify" && wrote(docs, "verify.json") && readDoc(out, "verify.json", &v) {
		for _, vr := range v.Runtimes {
			for i := range s.Runtimes {
				if s.Runtimes[i].RuntimeID == vr.RuntimeID {
					cr := &s.Runtimes[i]
					cr.TargetVersion, cr.Verdict, cr.Reasons, cr.Unblock = vr.TargetVersion, vr.Verdict, vr.Reasons, vr.Unblock
					for _, e := range vr.Evidence {
						cr.Evidence = append(cr.Evidence, evidence(e))
					}
				}
			}
		}
	}
	s.Change = change(name, out, docs, code)
	return s
}

// evidence is one evidence row as text: file:line when it points into code, else check: old → new.
func evidence(e c.VerifyCheck) string {
	if e.File != "" {
		if e.Line > 0 {
			return fmt.Sprintf("%s:%d (%s)", e.File, e.Line, e.Check)
		}
		return e.File + " (" + e.Check + ")"
	}
	s := e.Check
	switch {
	case e.Old != "" && e.New != "":
		s += ": " + e.Old + " → " + e.New
	case e.New != "":
		s += ": " + e.New
	case e.Log != "":
		s += ": " + e.Log
	}
	return s
}

// change is what build/swap/watch/rollback did, from the documents this command wrote; a refusal (3) or a "no"
// (2) without a document is reported as such.
func change(name, out string, docs []string, code int) *c.CliChange {
	ch := &c.CliChange{Step: name}
	switch name {
	case "build":
		var bd c.Build
		if !wrote(docs, "build.json") || !readDoc(out, "build.json", &bd) {
			break
		}
		ch.To, ch.Prefix = bd.VersionString, bd.Prefix
		switch {
		case bd.Status != "ok":
			ch.Result = "blocked"
			if bd.Error != nil {
				ch.Detail = string(bd.Error.Code)
			}
		case bd.Cache == "hit":
			ch.Result = "cache-hit"
		default:
			ch.Result = "built"
		}
	case "swap", "rollback":
		var sw c.Swap
		if !wrote(docs, "swap.json") || !readDoc(out, "swap.json", &sw) {
			break
		}
		ch.From, ch.To, ch.Result = sw.Old.Version, sw.New.Version, "swapped"
		if sw.RolledBackAt != nil {
			ch.Result, ch.From, ch.To = "rolled-back", sw.New.Version, sw.Old.Version
			if sw.RollbackReason != nil {
				ch.Detail = string(*sw.RollbackReason)
			}
		}
		var w c.Watch
		if name == "swap" && wrote(docs, "watch.json") && readDoc(out, "watch.json", &w) && ch.Detail == "" {
			ch.Detail = "watch " + string(w.Decision)
		}
	case "watch":
		var w c.Watch
		if !wrote(docs, "watch.json") || !readDoc(out, "watch.json", &w) {
			break
		}
		ch.Result = string(w.Decision)
		if w.Decision == "rollback" {
			ch.Result = "rolled-back"
		}
		if w.HeldReason != nil {
			ch.Detail = string(*w.HeldReason)
		} else if n := len(w.Triggers); n > 0 {
			ch.Detail = string(w.Triggers[n-1].Rule)
		}
	default:
		return nil
	}
	if ch.Result == "" {
		switch code {
		case 2:
			ch.Result = "declined"
		case 3:
			ch.Result = "refused"
		default:
			return nil
		}
	}
	return ch
}

// renderText prints the CLI's part of the text output (between the parts' own lines and `next:`): the runtimes after
// find (verify prints its verdict and evidence lines itself), the documents written and, on failure, one `error:`
// line that starts with `error: E_…` (the e2e harness parses it).
func renderText(w io.Writer, out string, r c.CliResult) {
	switch r.Command {
	case "find":
		if r.ExitCode == 0 && len(r.Summary.Runtimes) == 0 {
			fmt.Fprintln(w, "no PHP runtime found on this host")
		}
		if r.ExitCode == 0 {
			for _, cr := range r.Summary.Runtimes {
				fmt.Fprintf(w, "%s %s · %s · %s · service %s · %s\n", cr.SAPI, cr.Version, cr.Binary, provenanceText(cr), cr.Service,
					rootsText(cr))
			}
		}
	}
	if len(r.Documents) > 0 {
		fmt.Fprintln(w, "wrote "+documentsText(out, r.Documents))
	}
	if r.Error != nil {
		fmt.Fprintln(w, "error: "+errText(r.Error))
	}
}

// documentsText names the documents written, relative to out: top-level files by name, sub-directories (raw/,
// backup/) by their count.
func documentsText(out string, docs []string) string {
	var names []string
	sub := map[string]int{}
	for _, p := range docs {
		rel, err := filepath.Rel(out, p)
		if err != nil {
			rel = p
		}
		if dir, _, ok := strings.Cut(filepath.ToSlash(rel), "/"); ok {
			sub[dir]++
			continue
		}
		names = append(names, rel)
	}
	dirs := make([]string, 0, len(sub))
	for d := range sub {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		names = append(names, fmt.Sprintf("%s/ (%d files)", d, sub[d]))
	}
	return out + ": " + strings.Join(names, ", ")
}

// errText is a contract error as one line: `E_CODE — detail (k=v …); how`.
func errText(e *c.Error) string {
	s := string(e.Code)
	var parts []string
	how := ""
	keys := make([]string, 0, len(e.Data))
	for k := range e.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := e.Data[k]
		switch k {
		case "detail":
			parts = append([]string{fmt.Sprint(v)}, parts...)
		case "how":
			how = fmt.Sprint(v)
		case "runtimes":
			parts = append(parts, runtimesText(v))
		default:
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	if len(parts) > 0 {
		s += " — " + strings.Join(parts, "; ")
	}
	if how != "" {
		s += "; " + how
	}
	return s
}

// runtimesText renders a refusal's runtimes list ([{runtime_id, verdict, reason, how}]) as text.
func runtimesText(v any) string {
	list, ok := v.([]any)
	if !ok {
		return fmt.Sprint(v)
	}
	var out []string
	for _, x := range list {
		m, ok := x.(map[string]any)
		if !ok {
			out = append(out, fmt.Sprint(x))
			continue
		}
		s := fmt.Sprint(m["runtime_id"])
		if vd, ok := m["verdict"]; ok {
			s += " verdict " + fmt.Sprint(vd)
		}
		if r, ok := m["reason"]; ok {
			s += " (" + fmt.Sprint(r) + ")"
		}
		if h, ok := m["how"]; ok {
			s += ": " + fmt.Sprint(h)
		}
		out = append(out, s)
	}
	return strings.Join(out, "; ")
}

// provenanceKind is the summary's provenance: packaged for a distro package, else the kind.
func provenanceKind(p c.Provenance) string {
	if p.Kind == "distro-pkg" {
		return "packaged"
	}
	return string(p.Kind)
}

func provenanceText(cr c.CliRuntime) string {
	s := cr.Provenance
	if cr.Package != "" && cr.Provenance == "packaged" {
		s += " " + cr.Package
	}
	if cr.Repo != "" && cr.Provenance == "packaged" {
		s += " (" + cr.Repo + ")"
	}
	return s
}

func service(s c.ServiceInfo) string {
	switch {
	case s.Unit != "":
		return s.Unit
	case s.Program != "":
		return string(s.Manager) + ":" + s.Program
	case s.Manager != "":
		return string(s.Manager)
	}
	return "none"
}

func rootsText(cr c.CliRuntime) string {
	switch cr.CodeRoots {
	case 0:
		return "no code roots"
	case 1:
		return "1 code root " + cr.FirstCodeRoot
	}
	return fmt.Sprintf("%d code roots (first %s)", cr.CodeRoots, cr.FirstCodeRoot)
}
