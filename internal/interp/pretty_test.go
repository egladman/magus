package interp_test

import (
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	// Blank import wires the active backend and host bindings for all interp
	// tests. It registers the backend and host modules before any test runs.
	_ "github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/interp/engine"
	"github.com/stretchr/testify/assert"
)

func TestPrettyPrint_StringValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.StringValue("hello"), interp.PrettyOpts{})
	assert.Contains(t, sb.String(), "hello")
}

func TestPrettyPrint_NilValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.NilValue, interp.PrettyOpts{})
	assert.Contains(t, sb.String(), "nil")
}

func TestPrettyPrint_NumberValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.NumberValue(42), interp.PrettyOpts{})
	assert.Contains(t, sb.String(), "42")
}

func TestPrettyPrint_BoolValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.BoolValue(true), interp.PrettyOpts{})
	assert.Contains(t, sb.String(), "true")
}
