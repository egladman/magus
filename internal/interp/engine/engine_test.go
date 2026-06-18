package engine_test

import (
	"testing"

	"github.com/egladman/magus/internal/interp/engine"
	"github.com/stretchr/testify/assert"
)

func TestEngineValues_String(t *testing.T) {
	v := engine.StringValue("hello")
	assert.False(t, v.IsNil(), "StringValue.IsNil()")
	s, ok := v.AsString()
	assert.True(t, ok)
	assert.Equal(t, "hello", s)
	assert.True(t, v.AsBool(), "StringValue.AsBool()")
}

func TestEngineValues_Number(t *testing.T) {
	v := engine.NumberValue(3.14)
	assert.False(t, v.IsNil(), "NumberValue.IsNil()")
	n, ok := v.AsNumber()
	assert.True(t, ok)
	assert.Equal(t, 3.14, n)
	_, ok = v.AsString()
	assert.False(t, ok, "NumberValue.AsString() ok")
}

func TestEngineValues_Bool(t *testing.T) {
	vt := engine.BoolValue(true)
	assert.False(t, vt.IsNil(), "BoolValue(true).IsNil()")
	assert.True(t, vt.AsBool(), "BoolValue(true).AsBool()")

	vf := engine.BoolValue(false)
	assert.False(t, vf.AsBool(), "BoolValue(false).AsBool()")
}

func TestEngineValues_Nil(t *testing.T) {
	v := engine.NilValue
	assert.True(t, v.IsNil(), "NilValue.IsNil()")
	assert.False(t, v.AsBool(), "NilValue.AsBool()")
	_, ok := v.AsString()
	assert.False(t, ok, "NilValue.AsString() ok")
	_, ok = v.AsNumber()
	assert.False(t, ok, "NilValue.AsNumber() ok")
}

func TestEngineFor_Unknown(t *testing.T) {
	assert.Nil(t, engine.Lookup("no_such_engine_xyz"), "For(unknown engine) should return nil")
}
