package watch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuiltinIgnore_VCSMeta(t *testing.T) {
	for _, path := range []string{
		"/work/.git/COMMIT_EDITMSG",
		"/work/node_modules/pkg/index.js",
		"/work/.magus/cache.json",
	} {
		assert.True(t, BuiltinIgnore(path), "BuiltinIgnore(%q) should be true", path)
	}
	for _, path := range []string{
		"/work/src/main.go",
		"/work/README.md",
	} {
		assert.False(t, BuiltinIgnore(path), "BuiltinIgnore(%q) should be false", path)
	}
}

func TestCompose_AnyTrue(t *testing.T) {
	alwaysTrue := func(string) bool { return true }
	alwaysFalse := func(string) bool { return false }

	// OR semantics: true if any predicate returns true
	assert.True(t, Compose(alwaysTrue, alwaysFalse)("x"), "Compose(true,false): OR should return true")
	assert.True(t, Compose(alwaysTrue, alwaysTrue)("x"), "Compose(true,true): OR should return true")
	assert.False(t, Compose(alwaysFalse, alwaysFalse)("x"), "Compose(false,false): OR should return false")
}

func TestCompose_Empty(t *testing.T) {
	none := Compose()
	assert.False(t, none("anything"), "Compose() should return false for any input")
}
