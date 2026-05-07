package interp_test

import (
	"testing"

	"github.com/egladman/magus/internal/interp"
)

func TestSetLuaEngine_ClearsOverride(t *testing.T) {
	interp.SetLuaEngine("hypothetical_engine")
	interp.SetLuaEngine("") // clear; must not panic
}

func TestCompiledEngines_ReturnsList(t *testing.T) {
	engines := interp.CompiledEngines()
	// The list may be empty in CGO_ENABLED=0 builds without gopherlua registered,
	// but the function must return a non-nil slice.
	if engines == nil {
		t.Error("CompiledEngines() returned nil slice")
	}
}
