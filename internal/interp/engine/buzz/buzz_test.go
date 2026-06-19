package buzz_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings" // init() wires the magus host bindings (magus.project, etc.)
	"github.com/egladman/magus/internal/interp/engine"
	_ "github.com/egladman/magus/internal/interp/engine/buzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuzzEngine_Registered(t *testing.T) {
	e := engine.Lookup("buzz")
	require.NotNil(t, e, "buzz engine not registered after package import")
	assert.Equal(t, "buzz", e.ID())
}

func TestBuzzEngine_NewSession(t *testing.T) {
	e := engine.Lookup("buzz")
	if e == nil {
		t.Skip("buzz engine not registered")
	}
	s, err := e.NewSession(context.Background())
	require.NoError(t, err)
	defer s.Close()

	assert.NoError(t, s.DoString(`var x: int = 1;`))
}

func TestBuzzEngine_GetSetGlobal(t *testing.T) {
	e := engine.Lookup("buzz")
	if e == nil {
		t.Skip("buzz engine not registered")
	}
	s, err := e.NewSession(context.Background())
	require.NoError(t, err)
	defer s.Close()

	s.SetGlobal("msg", engine.StringValue("hi"))
	v := s.GetGlobal("msg")
	require.NotNil(t, v)
	require.False(t, v.IsNil(), "GetGlobal returned nil after SetGlobal")
	got, ok := v.AsString()
	assert.True(t, ok)
	assert.Equal(t, "hi", got)
}

func TestIntegration_ParseTargets(t *testing.T) {
	src := &interp.Source{
		Dir:    t.TempDir(),
		Engine: "buzz",
	}

	// Write a minimal magusfile.buzz to a temp file.
	path := filepath.Join(src.Dir, "magusfile.buzz")
	content := `
import "magus";

export fun build(args: [str]) > void {}
export fun test(args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	src.Files = []string{path}

	targets, err := interp.Parse(context.Background(), src)
	require.NoError(t, err)

	got := make(map[string]bool)
	for _, tgt := range targets {
		got[tgt.Key] = true
	}
	for _, want := range []string{"build", "test"} {
		assert.Truef(t, got[want], "target %q not found; got %v", want, targets)
	}
}

func TestIntegration_RunTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	// We can't capture the side effect of an empty function body,
	// so we test that Run succeeds without error.
	content := `
import "magus";
export fun greet(args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	src := &interp.Source{Dir: dir, Files: []string{path}, Engine: "buzz"}
	err := interp.Run(context.Background(), src, "greet", nil, "")
	require.NoError(t, err)
}

func TestIntegration_UnknownTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	content := `import "magus";
export fun build() > void {}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	src := &interp.Source{Dir: dir, Files: []string{path}, Engine: "buzz"}
	err := interp.Run(context.Background(), src, "notexist", nil, "")
	assert.Error(t, err, "expected error for unknown target")
}

func TestIntegration_ProjectRegister(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	content := `
import "magus";
magus.project(".", {
    "outputs": ["bin/*"],
});
export fun build(args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	src := &interp.Source{Dir: dir, Files: []string{path}, Engine: "buzz"}
	require.NoError(t, interp.Run(context.Background(), src, "build", nil, ""))
}
