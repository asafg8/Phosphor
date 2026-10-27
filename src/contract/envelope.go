// Package contract holds every JSON document phosphor writes.
// It is the single source of truth: Go structs here, JSON Schemas generated from them (make schemas).
package contract

import (
	"fmt"
	"time"
)

const (
	SchemaFingerprint = "phosphor/fingerprint/v1"
	SchemaScan        = "phosphor/scan/v1"
	SchemaReadiness   = "phosphor/readiness/v1"
	SchemaPlan        = "phosphor/plan/v1"
	SchemaBuild       = "phosphor/build/v1"
	SchemaPreflight   = "phosphor/preflight/v1"
	SchemaSwap        = "phosphor/swap/v1"
	SchemaWatch       = "phosphor/watch/v1"
	SchemaMarker      = "phosphor/marker/v1"
	SchemaBuildIndex  = "phosphor/build-index/v1"
	SchemaAdvisories  = "phosphor/advisories/v1"
)

// Error is the only way a failure is described: a code plus structured detail, no prose.
type Error struct {
	Code ErrorCode      `json:"code"`
	Data map[string]any `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s %v", e.Code, e.Data) }

func Errf(code ErrorCode, kv ...any) *Error {
	e := &Error{Code: code}
	if len(kv) > 0 {
		e.Data = map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			e.Data[fmt.Sprint(kv[i])] = kv[i+1]
		}
	}
	return e
}

// Envelope is embedded in every document a stage writes.
type Envelope struct {
	Schema          string     `json:"schema"`
	PhosphorVersion string     `json:"phosphor_version"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	Status          EnvStatus  `json:"status"`
	Error           *Error     `json:"error,omitempty"`
}

func NewEnvelope(schema, version string) Envelope {
	return Envelope{Schema: schema, PhosphorVersion: version, StartedAt: time.Now().UTC(), Status: "ok"}
}

func (e *Envelope) Finish(err *Error) {
	t := time.Now().UTC()
	e.FinishedAt = &t
	if err != nil {
		e.Status = "failed"
		e.Error = err
	}
}
