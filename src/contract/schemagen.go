package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Documents maps a schema file name (src/contract/schema/<name>.json) to its root Go type.
var Documents = map[string]any{
	"fingerprint":  Fingerprint{},
	"scan":         Scan{},
	"readiness":    Scan{}, // readiness.json: the opt-in PHPCompatibility run, same shape as scan.json
	"plan":         Plan{},
	"build":        Build{},
	"build-index":  BuildIndex{},
	"preflight":    Preflight{},
	"swap":         Swap{},
	"watch":        Watch{},
	"event":        Event{},
	"marker":       Marker{},
	"verify":       Verify{},    // verify.json: `phosphor verify`'s per-runtime verdicts
	"cli-result":   CliResult{}, // `phosphor <cmd> --json` stdout; never on disk
	"pkg-fetch":    PkgFetch{},
	"watch-config": WatchConfig{},
}

// SchemaForKey picks the schema name for a results-directory path (used by ValidateSchema in tests).
func SchemaForKey(key string) string {
	base := key[strings.LastIndex(key, "/")+1:]
	switch {
	case strings.HasPrefix(key, "builds/") && strings.HasSuffix(base, ".json"):
		return "build-index"
	case strings.Contains(key, "/raw/"):
		return ""
	case strings.HasSuffix(base, ".ndjson"):
		return "event"
	case strings.HasSuffix(base, ".json"):
		n := strings.TrimSuffix(base, ".json")
		if _, ok := Documents[n]; ok {
			return n
		}
	}
	return ""
}

var timeType = reflect.TypeOf(time.Time{})
var enumIface = reflect.TypeOf((*enum)(nil)).Elem()

type gen struct{ defs map[string]map[string]any }

// GenerateSchema returns a JSON Schema (draft 2020-12) for v's type. Structs are closed
// (additionalProperties false), enums are closed, non-omitempty fields are required.
func GenerateSchema(name string, v any) ([]byte, error) {
	g := &gen{defs: map[string]map[string]any{}}
	root := g.typ(reflect.TypeOf(v))
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["$id"] = "https://phosphor.dev/schema/phosphor/" + name + ".json"
	if len(g.defs) > 0 {
		root["$defs"] = g.defs
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	err := enc.Encode(root)
	return buf.Bytes(), err
}

func (g *gen) typ(t reflect.Type) map[string]any {
	if t.Implements(enumIface) && t.Kind() == reflect.String {
		vals := reflect.Zero(t).Interface().(enum).EnumValues()
		return map[string]any{"type": "string", "enum": vals}
	}
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return map[string]any{"anyOf": []any{map[string]any{"type": "null"}, g.typ(t.Elem())}}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint64, reflect.Uint32:
		return map[string]any{"type": "integer", "minimum": 0} // watch-config offsets: inode
	case reflect.Float64, reflect.Float32:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": g.typ(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.typ(t.Elem())}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Struct:
		name := t.Name()
		if _, ok := g.defs[name]; !ok {
			g.defs[name] = nil // reserve (no recursion in contracts, but be safe)
			g.defs[name] = g.object(t)
		}
		return map[string]any{"$ref": "#/$defs/" + name}
	}
	panic(fmt.Sprintf("schemagen: unsupported kind %s", t))
}

func (g *gen) object(t reflect.Type) map[string]any {
	props := map[string]any{}
	var req []string
	g.fields(t, props, &req)
	sort.Strings(req)
	o := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(req) > 0 {
		o["required"] = req
	}
	return o
}

func (g *gen) fields(t reflect.Type, props map[string]any, req *[]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			g.fields(f.Type, props, req)
			continue
		}
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		name := parts[0]
		if name == "" {
			name = f.Name
		}
		omit := false
		for _, p := range parts[1:] {
			if p == "omitempty" {
				omit = true
			}
		}
		props[name] = g.typ(f.Type)
		if !omit {
			*req = append(*req, name)
		}
	}
}

// Normalize replaces nil slices and maps that are not omitempty with empty ones, so documents marshal
// `[]`/`{}` instead of `null` (schemas require arrays). v must be a pointer.
func Normalize(v any) {
	normalize(reflect.ValueOf(v))
}

func normalize(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			normalize(v.Elem())
		}
	case reflect.Struct:
		if v.Type() == timeType {
			return
		}
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			fv := v.Field(i)
			omit := strings.Contains(f.Tag.Get("json"), "omitempty")
			if !omit && fv.CanSet() {
				switch fv.Kind() {
				case reflect.Slice:
					if fv.IsNil() {
						fv.Set(reflect.MakeSlice(fv.Type(), 0, 0))
					}
				}
			}
			normalize(fv)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			normalize(v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			e := v.MapIndex(k)
			if e.Kind() == reflect.Struct || e.Kind() == reflect.Pointer {
				// map values are not addressable; copy, normalize, store back
				c := reflect.New(e.Type()).Elem()
				c.Set(e)
				normalize(c)
				v.SetMapIndex(k, c)
			}
		}
	}
}

// Marshal normalizes then encodes indented JSON without HTML escaping.
func Marshal(v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer {
		p := reflect.New(rv.Type())
		p.Elem().Set(rv)
		v = p.Interface()
	}
	Normalize(v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
