package watch

import (
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIgnoreFlagAccumulatesRepeatedValues(t *testing.T) {
	var f IgnoreFlag
	require.NoError(t, f.Set(`type=glob,pattern=**/scratch/*`))
	require.NoError(t, f.Set(`type=regex,pattern=\.tmp$`))
	assert.Equal(t, []types.IgnorePattern{
		{Type: types.PatternType("glob"), Pattern: "**/scratch/*"},
		{Type: types.PatternType("regex"), Pattern: `\.tmp$`},
	}, f.Patterns)
	assert.Equal(t, `glob=**/scratch/*,regex=\.tmp$`, f.String())
}

func TestIgnoreFlagRefusesATypeLessValueAndKeepsWhatItHas(t *testing.T) {
	var f IgnoreFlag
	require.NoError(t, f.Set(`type=literal,pattern=bazel-out`))
	err := f.Set(`pattern=foo`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ignore: type is required (use type=glob, type=regex, or type=literal)")
	assert.Len(t, f.Patterns, 1)
}

func TestIgnoreFlagRefusesAnUnknownType(t *testing.T) {
	var f IgnoreFlag
	err := f.Set(`type=bogus,pattern=foo`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `ignore: unknown type "bogus" (use glob, regex, or literal)`)
}

func TestIgnoreFlagRefusesAnEmptyPattern(t *testing.T) {
	var f IgnoreFlag
	err := f.Set(`type=glob`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ignore: pattern is empty")
}

func TestIgnoreFlagRefusesAValueWithoutAKey(t *testing.T) {
	var f IgnoreFlag
	err := f.Set("justastring")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `ignore: "justastring" is not in key=value form`)
}

func TestToWorkspaceRelMakesAnAbsolutePathRelativeAndSlashed(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "ws")
	assert.Equal(t, "a/b.go", ToWorkspaceRel(root, filepath.Join(root, "a", "b.go")))
}

func TestToWorkspaceRelPassesThroughWhatItCannotRelate(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "ws")
	outside := filepath.Join(string(filepath.Separator), "elsewhere", "x.go")
	assert.Equal(t, outside, ToWorkspaceRel(root, outside), "a path outside the root stays absolute")
	assert.Equal(t, AllSentinel, ToWorkspaceRel(root, AllSentinel), "a sentinel token stays verbatim")
	assert.Equal(t, "rel/x.go", ToWorkspaceRel(root, "rel/x.go"), "a relative path stays as given")
}
