package gen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKindString(t *testing.T) {
	assert.Equal(t, "KindString", KindString.String())
	assert.Equal(t, "KindInt", KindInt.String())
	assert.Equal(t, "KindBool", KindBool.String())
	assert.Equal(t, "KindFloat64", KindFloat64.String())
	assert.Equal(t, "KindBoolPtr", KindBoolPtr.String())
	assert.Equal(t, "KindDuration", KindDuration.String())
	assert.Equal(t, "KindStringSlice", KindStringSlice.String())
}

func TestKindString_Unknown(t *testing.T) {
	var k Kind = 255
	s := k.String()
	assert.Truef(t, strings.HasPrefix(s, "Kind("), "unknown Kind.String() = %q, want Kind(<n>)", s)
}
