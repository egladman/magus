package gen_test

import (
	"strings"
	"testing"

	"github.com/egladman/magus/schema/gen"
)

func TestKindString(t *testing.T) {
	cases := []struct {
		k    gen.Kind
		want string
	}{
		{gen.KindString, "KindString"},
		{gen.KindInt, "KindInt"},
		{gen.KindBool, "KindBool"},
		{gen.KindFloat64, "KindFloat64"},
		{gen.KindBoolPtr, "KindBoolPtr"},
		{gen.KindDuration, "KindDuration"},
		{gen.KindStringSlice, "KindStringSlice"},
	}
	for _, tc := range cases {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.k, got, tc.want)
		}
	}
}

func TestKindString_Unknown(t *testing.T) {
	var k gen.Kind = 255
	s := k.String()
	if !strings.HasPrefix(s, "Kind(") {
		t.Errorf("unknown Kind.String() = %q, want Kind(<n>)", s)
	}
}
