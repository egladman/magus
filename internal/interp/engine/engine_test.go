package engine_test

import (
	"testing"

	"github.com/egladman/magus/internal/interp/engine"
)

func TestEngineValues_String(t *testing.T) {
	v := engine.StringValue("hello")
	if v.IsNil() {
		t.Error("StringValue.IsNil() = true")
	}
	s, ok := v.AsString()
	if !ok || s != "hello" {
		t.Errorf("AsString() = %q, %v; want %q, true", s, ok, "hello")
	}
	if !v.AsBool() {
		t.Error("StringValue.AsBool() = false")
	}
}

func TestEngineValues_Number(t *testing.T) {
	v := engine.NumberValue(3.14)
	if v.IsNil() {
		t.Error("NumberValue.IsNil() = true")
	}
	n, ok := v.AsNumber()
	if !ok || n != 3.14 {
		t.Errorf("AsNumber() = %v, %v; want 3.14, true", n, ok)
	}
	_, ok = v.AsString()
	if ok {
		t.Error("NumberValue.AsString() ok = true, want false")
	}
}

func TestEngineValues_Bool(t *testing.T) {
	vt := engine.BoolValue(true)
	if vt.IsNil() {
		t.Error("BoolValue(true).IsNil() = true")
	}
	if !vt.AsBool() {
		t.Error("BoolValue(true).AsBool() = false")
	}

	vf := engine.BoolValue(false)
	if vf.AsBool() {
		t.Error("BoolValue(false).AsBool() = true")
	}
}

func TestEngineValues_Nil(t *testing.T) {
	v := engine.NilValue
	if !v.IsNil() {
		t.Error("NilValue.IsNil() = false")
	}
	if v.AsBool() {
		t.Error("NilValue.AsBool() = true")
	}
	_, ok := v.AsString()
	if ok {
		t.Error("NilValue.AsString() ok = true")
	}
	_, ok = v.AsNumber()
	if ok {
		t.Error("NilValue.AsNumber() ok = true")
	}
}

func TestEngineFor_Unknown(t *testing.T) {
	if engine.Lookup("no_such_engine_xyz") != nil {
		t.Error("For(unknown engine) should return nil")
	}
}
