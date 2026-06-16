package gen

import (
	"strings"
	"testing"
)

func TestKindString(t *testing.T) {
	cases := []struct {
		k    Kind
		want string
	}{
		{KindString, "KindString"},
		{KindInt, "KindInt"},
		{KindBool, "KindBool"},
		{KindFloat64, "KindFloat64"},
		{KindBoolPtr, "KindBoolPtr"},
		{KindDuration, "KindDuration"},
		{KindStringSlice, "KindStringSlice"},
	}
	for _, tc := range cases {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.k, got, tc.want)
		}
	}
}

func TestKindString_Unknown(t *testing.T) {
	var k Kind = 255
	s := k.String()
	if !strings.HasPrefix(s, "Kind(") {
		t.Errorf("unknown Kind.String() = %q, want Kind(<n>)", s)
	}
}
