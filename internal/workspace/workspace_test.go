package workspace

import (
	"testing"

	"github.com/egladman/magus/internal/config"
)

func TestWithLoadedConfig(t *testing.T) {
	opt := WithLoadedConfig(config.Config{})
	var l Load
	opt(&l)
	if l.Preloaded == nil {
		t.Fatal("WithLoadedConfig: Load.Preloaded is nil")
	}
}

func TestWithWorkspaceRegistry(t *testing.T) {
	reg := NewWorkspaceRegistry()
	var l Load
	l.Registry = reg
	if l.Registry != reg {
		t.Error("Load.Registry round-trip failed")
	}
}
