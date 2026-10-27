package contract

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	compileOnce sync.Once
	compiled    map[string]*jsonschema.Schema
	compileErr  error
)

func schemas() (map[string]*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		compiled = map[string]*jsonschema.Schema{}
		c := jsonschema.NewCompiler()
		c.AssertFormat()
		for name, v := range Documents {
			raw, err := GenerateSchema(name, v)
			if err != nil {
				compileErr = err
				return
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
			if err != nil {
				compileErr = err
				return
			}
			url := "https://phosphor.dev/schema/phosphor/" + name + ".json"
			if err := c.AddResource(url, doc); err != nil {
				compileErr = err
				return
			}
		}
		for name := range Documents {
			s, err := c.Compile("https://phosphor.dev/schema/phosphor/" + name + ".json")
			if err != nil {
				compileErr = err
				return
			}
			compiled[name] = s
		}
	})
	return compiled, compileErr
}

// Validate checks raw JSON against the named schema. For "event" the input may be ndjson (one event per line).
func Validate(name string, raw []byte) error {
	all, err := schemas()
	if err != nil {
		return err
	}
	s, ok := all[name]
	if !ok {
		return fmt.Errorf("no schema %q", name)
	}
	if name == "event" {
		sc := bufio.NewScanner(bytes.NewReader(raw))
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		n := 0
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			n++
			if err := validateOne(s, line); err != nil {
				return fmt.Errorf("line %d: %w", n, err)
			}
		}
		return sc.Err()
	}
	return validateOne(s, raw)
}

func validateOne(s *jsonschema.Schema, raw []byte) error {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return s.Validate(inst)
}

// ValidateKey validates a results-directory document by its path; paths without a schema (logs, raw/) pass.
func ValidateKey(key string, raw []byte) error {
	name := SchemaForKey(key)
	if name == "" {
		return nil
	}
	if err := Validate(name, raw); err != nil {
		return fmt.Errorf("%s (%s): %w", key, name, err)
	}
	return nil
}

// MarshalLine encodes one compact JSON line (ndjson) after normalising.
func MarshalLine(v any) ([]byte, error) {
	Normalize(v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(v)
	return buf.Bytes(), err
}
