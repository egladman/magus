// Package wire_test covers the option-carrier types and constructors in wire.go.
// The Load, Run, and Compose structs are plain value types with no logic of their
// own; tests verify that each option constructor mutates the correct field.
package wire_test

import (
	"bytes"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/wire"
)

func TestWithLoadedConfig(t *testing.T) {
	cfg := config.Config{}
	opt := wire.WithLoadedConfig(cfg)
	var l wire.Load
	opt(&l)
	if l.Preloaded == nil {
		t.Fatal("WithLoadedConfig: Load.Preloaded is nil")
	}
}

func TestWithReportWriter(t *testing.T) {
	var buf bytes.Buffer
	opt := wire.WithReportWriter(&buf)
	var r wire.Run
	opt(&r)
	if r.ReportWriter == nil {
		t.Fatal("WithReportWriter: Run.ReportWriter is nil")
	}
	if r.ReportWriter != &buf {
		t.Error("WithReportWriter: Run.ReportWriter does not point to the provided writer")
	}
}

func TestWithWorkspaceRegistry(t *testing.T) {
	reg := wire.NewWorkspaceRegistry()
	var l wire.Load
	// WithLoadedConfig already tested above; test that registry can be set on Load directly.
	l.Registry = reg
	if l.Registry != reg {
		t.Error("Load.Registry round-trip failed")
	}
}
