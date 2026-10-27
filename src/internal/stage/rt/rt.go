// Package rt is the stage runtime shared by every stage of the host CLI: the stage's args, the files it may read
// (GET entries: local paths), and the out directory that receives every document it writes plus events.ndjson.
// Nothing leaves the host.
package rt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
	"github.com/asafg8/Phosphor/src/internal/version"
)

// Root is phosphor's home on the host: builds, backups and the swap's state live under it. PHOSPHOR_ROOT moves it
// to an exec-capable filesystem on hosts where /opt is mounted noexec.
var Root = "/opt/phosphor"

// downloads fetches pinned upstream sources (php-src, OpenSSL, PECL); large, so the timeout is generous.
var downloads = &http.Client{Timeout: 30 * time.Minute}

// LocalDir is the out directory of the current command: every document and events.ndjson are written there.
var LocalDir string

type Ctx struct {
	M        *c.Manifest
	StateDir string
	Version  string

	mu      sync.Mutex
	pending []c.Event
	seq     int64
}

// New starts a stage context over m that writes to LocalDir (or Root/state when no out directory is set).
func New(m *c.Manifest) (*Ctx, error) {
	if m.Args == nil {
		m.Args = map[string]any{}
	}
	x := &Ctx{M: m, Version: version.String(), StateDir: LocalDir}
	if x.StateDir == "" {
		x.StateDir = filepath.Join(Root, "state")
	}
	if err := os.MkdirAll(x.StateDir, 0o700); err != nil {
		return nil, c.Errf(c.EInternal, "step", "state-dir")
	}
	x.seq = x.loadSeq()
	return x, nil
}

// Load starts a stage context from a manifest file (the detached watch's).
func Load(path string) (*Ctx, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, c.Errf(c.EManifest, "step", "read", "manifest", path)
	}
	var m c.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, c.Errf(c.EManifest, "step", "decode")
	}
	return New(&m)
}

// SaveManifest writes the context's manifest to path, for a re-exec to Load.
func (x *Ctx) SaveManifest(path string) error {
	b, err := json.MarshalIndent(x.M, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func (x *Ctx) Arg(name string) string {
	if v, ok := x.M.Args[name]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func (x *Ctx) ArgBool(name string) bool {
	b, _ := x.M.Args[name].(bool)
	return b
}

// ArgInto decodes an argument (any JSON value) into v.
func (x *Ctx) ArgInto(name string, v any) bool {
	raw, ok := x.M.Args[name]
	if !ok {
		return false
	}
	b, _ := json.Marshal(raw)
	return json.Unmarshal(b, v) == nil
}

func (x *Ctx) Envelope(schema string) c.Envelope {
	return c.NewEnvelope(schema, x.Version)
}

func (x *Ctx) seqFile() string { return filepath.Join(x.StateDir, "seq") }

func (x *Ctx) loadSeq() int64 {
	b, err := os.ReadFile(x.seqFile())
	if err != nil {
		return 0
	}
	var n int64
	fmt.Sscan(string(b), &n)
	return n
}

// Emit records an event; seq continues across the commands that share the out directory.
func (x *Ctx) Emit(typ c.EventType, step, code string, data map[string]any) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.seq++
	_ = os.WriteFile(x.seqFile(), []byte(fmt.Sprint(x.seq)), 0o600)
	x.pending = append(x.pending, c.Event{TS: time.Now().UTC(), Seq: x.seq, Type: typ, Step: step, Code: code,
		Data: data})
}

// Step is shorthand for a step event.
func (x *Ctx) Step(step string, data map[string]any) { x.Emit(c.EvStep, step, "", data) }

// Flush appends the buffered events to events.ndjson.
func (x *Ctx) Flush() {
	x.mu.Lock()
	evs := x.pending
	x.pending = nil
	x.mu.Unlock()
	if len(evs) == 0 {
		return
	}
	f, err := os.OpenFile(filepath.Join(x.StateDir, "events.ndjson"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	for i := range evs {
		b, _ := c.MarshalLine(&evs[i])
		_, _ = f.Write(b)
	}
}

// WriteDoc normalises and writes a contract document by name.
func (x *Ctx) WriteDoc(name string, v any) error {
	b, err := c.Marshal(v)
	if err != nil {
		return err
	}
	return x.WriteFile(name, b)
}

// WriteFile writes a file under the state (out) directory.
func (x *Ctx) WriteFile(name string, b []byte) error {
	p := filepath.Join(x.StateDir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// ReadLocal returns a document previously written for this run.
func (x *Ctx) ReadLocal(name string, v any) error {
	b, err := os.ReadFile(filepath.Join(x.StateDir, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Fetch copies the GET entry name to dst and returns its sha256; with wantSHA the copy is verified before it is
// kept. An entry is a local path, or an http(s) URL of a pinned upstream download, which needs wantSHA: nothing
// unpinned is ever downloaded.
func (x *Ctx) Fetch(name, dst, wantSHA string) (string, error) {
	u, ok := x.M.Get[name]
	if !ok {
		return "", fmt.Errorf("no get entry %s", name)
	}
	var r io.ReadCloser
	switch {
	case strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://"):
		if wantSHA == "" {
			return "", fmt.Errorf("get %s: a download needs a pinned sha256", name)
		}
		resp, err := downloads.Get(u)
		if err != nil {
			return "", err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return "", fmt.Errorf("get %s: status %d", name, resp.StatusCode)
		}
		r = resp.Body
	case strings.Contains(u, "://"):
		return "", fmt.Errorf("get %s: unsupported URL", name)
	default:
		f, err := os.Open(u)
		if err != nil {
			return "", err
		}
		r = f
	}
	defer r.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), r); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	f.Close()
	sum := hex.EncodeToString(h.Sum(nil))
	if wantSHA != "" && sum != wantSHA {
		os.Remove(tmp)
		return sum, c.Errf(c.ESourceIntegrity, "name", name, "want", wantSHA, "got", sum)
	}
	return sum, os.Rename(tmp, dst)
}
