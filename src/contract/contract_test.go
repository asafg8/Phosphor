package contract

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Every document type round-trips JSON and validates against its generated schema.
func TestU01_RoundTripAndSchema(t *testing.T) {
	root := "../../testing/testdata/sample-run"
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		key := filepath.ToSlash(strings.TrimPrefix(p, root+"/"))
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := ValidateKey(key, raw); err != nil {
			t.Errorf("golden %s: %v", key, err)
		}
		name := SchemaForKey(key)
		if name == "" || name == "event" {
			return nil
		}
		n++
		typ := reflect.TypeOf(Documents[name])
		v := reflect.New(typ).Interface()
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			t.Errorf("%s: decode: %v", key, err)
			return nil
		}
		again, err := Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		_ = json.Unmarshal(raw, &a)
		_ = json.Unmarshal(again, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: round-trip differs", key)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 10 {
		t.Fatalf("only %d golden documents checked", n)
	}
}

// Committed schemas are exactly what the generator produces (make schemas was run).
func TestU01_SchemasCommitted(t *testing.T) {
	for name, v := range Documents {
		want, err := GenerateSchema(name, v)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join("schema", name+".json"))
		if err != nil {
			t.Errorf("%s: %v (run make schemas)", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s.json is stale (run make schemas)", name)
		}
	}
}

// Enums are closed and documents are closed objects.
func TestU01_EnumsClosed(t *testing.T) {
	ev := `{"ts":"2026-10-24T09:00:00Z","seq":1,"type":"status","status":"done-ish"}`
	if err := Validate("event", []byte(ev)); err == nil {
		t.Error("unknown status accepted")
	}
	ev2 := `{"ts":"2026-10-24T09:00:00Z","seq":1,"type":"status","status":"swapped","extra":1}`
	if err := Validate("event", []byte(ev2)); err == nil {
		t.Error("unknown property accepted")
	}
	ok := `{"ts":"2026-10-24T09:00:00Z","seq":1,"type":"status","status":"swapped"}`
	if err := Validate("event", []byte(ok)); err != nil {
		t.Errorf("valid event rejected: %v", err)
	}
	for _, s := range AllStatuses {
		if !Status(s).Valid() {
			t.Errorf("status %s", s)
		}
	}
	if Status("patched-but-ephemeral").Valid() || Status("insufficient-signal").Valid() {
		t.Error("ephemeral / insufficient-signal are flags, not statuses")
	}
	for _, r := range AllReasons {
		if strings.Trim(r, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			t.Errorf("reason %q not a code", r)
		}
	}
	for _, e := range AllErrorCodes {
		if !strings.HasPrefix(e, "E_") {
			t.Errorf("error code %q", e)
		}
	}
}

func TestNormalizeEmptySlices(t *testing.T) {
	b, err := Marshal(Plan{Schema: SchemaPlan})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("null")) && bytes.Contains(b, []byte(`"runtimes": null`)) {
		t.Fatalf("nil slice marshalled as null: %s", b)
	}
}
