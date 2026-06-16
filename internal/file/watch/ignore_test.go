package watch

import (
	"testing"
)

func TestBuiltinIgnore_VCSMeta(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/work/.git/COMMIT_EDITMSG", true},
		{"/work/node_modules/pkg/index.js", true},
		{"/work/.magus/cache.json", true},
		{"/work/src/main.go", false},
		{"/work/README.md", false},
	}
	for _, tc := range cases {
		got := BuiltinIgnore(tc.path)
		if got != tc.want {
			t.Errorf("BuiltinIgnore(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestCompose_AnyTrue(t *testing.T) {
	alwaysTrue := func(string) bool { return true }
	alwaysFalse := func(string) bool { return false }

	// OR semantics: true if any predicate returns true
	if !Compose(alwaysTrue, alwaysFalse)("x") {
		t.Error("Compose(true,false): OR should return true")
	}
	if !Compose(alwaysTrue, alwaysTrue)("x") {
		t.Error("Compose(true,true): OR should return true")
	}
	if Compose(alwaysFalse, alwaysFalse)("x") {
		t.Error("Compose(false,false): OR should return false")
	}
}

func TestCompose_Empty(t *testing.T) {
	none := Compose()
	if none("anything") {
		t.Error("Compose() should return false for any input")
	}
}
