package interp_test

import (
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/engine"
)

func TestPrettyPrint_StringValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.StringValue("hello"), interp.PrettyOpts{})
	if !strings.Contains(sb.String(), "hello") {
		t.Errorf("PrettyPrint output %q should contain %q", sb.String(), "hello")
	}
}

func TestPrettyPrint_NilValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.NilValue, interp.PrettyOpts{})
	if !strings.Contains(sb.String(), "nil") {
		t.Errorf("PrettyPrint output %q should contain %q", sb.String(), "nil")
	}
}

func TestPrettyPrint_NumberValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.NumberValue(42), interp.PrettyOpts{})
	if !strings.Contains(sb.String(), "42") {
		t.Errorf("PrettyPrint output %q should contain %q", sb.String(), "42")
	}
}

func TestPrettyPrint_BoolValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.BoolValue(true), interp.PrettyOpts{})
	if !strings.Contains(sb.String(), "true") {
		t.Errorf("PrettyPrint output %q should contain %q", sb.String(), "true")
	}
}
