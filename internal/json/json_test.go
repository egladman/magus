package json

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pair struct {
	K string `json:"k"`
	V int    `json:"v"`
}

func TestMarshalUnmarshalRoundtrip(t *testing.T) {
	t.Parallel()
	in := pair{K: "hello", V: 42}
	b, err := Marshal(in)
	require.NoError(t, err)
	var out pair
	require.NoError(t, Unmarshal(b, &out))
	assert.Equal(t, in, out)
}

func TestMarshalIndent(t *testing.T) {
	t.Parallel()
	b, err := MarshalIndent(map[string]int{"x": 1}, "", "  ")
	require.NoError(t, err)
	assert.True(t, bytes.Contains(b, []byte("\n")), "MarshalIndent output has no newlines")
}

func TestEncoderDecoder(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	require.NoError(t, enc.Encode(pair{K: "a", V: 1}))
	dec := NewDecoder(&buf)
	var got pair
	require.NoError(t, dec.Decode(&got))
	assert.Equal(t, pair{K: "a", V: 1}, got)
}

// TestDurationRoundTrips checks a time.Duration survives a Marshal/Unmarshal cycle
// under either json. Under json/v2 it doubles as the go.dev/issue/71631 regression
// guard: without the codec's explicit Duration handling, Marshal errors outright
// (Duration has no default v2 representation), which broke `magus config view -o json`.
func TestDurationRoundTrips(t *testing.T) {
	type withDur struct {
		TTL time.Duration `json:"ttl"`
	}
	b, err := Marshal(withDur{6 * time.Hour})
	require.NoError(t, err)
	var got withDur
	require.NoError(t, Unmarshal(b, &got))
	assert.Equal(t, 6*time.Hour, got.TTL)
}

// TestNoDirectEncodingJSONImport is the whole-tree rule: every Go file marshals through
// this package, so which codec magus uses is one decision in one place.
//
// The allowlist is the codecs themselves - they are what wraps encoding/json - and nothing
// else. Test files are deliberately in scope: a test that marshals through the stdlib is
// asserting the behaviour of a codec the binary does not use, which is how a GOEXPERIMENT
// this package exists to absorb (see TestDurationRoundTrips) passes its tests and breaks
// the CLI.
func TestNoDirectEncodingJSONImport(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	allowed := map[string]bool{
		"internal/json/json.go":                     true,
		"internal/json/json_v2.go":                  true,
		"libs/gopherbuzz/internal/codec/json.go":    true,
		"libs/gopherbuzz/internal/codec/json_v2.go": true,
	}
	importers, err := encodingJSONImporters(root, allowed)
	require.NoError(t, err)
	assert.Empty(t, importers, "JSON must use the shared JSON package")
}

func TestEncodingJSONImportersSkipsDependencyAndBuildTrees(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"cmd/magus/main.go",
		"node_modules/example/index.go",
		"vendor/example/json.go",
		"third_party/example/json.go",
		"build/generated.go",
		"dist/generated.go",
	} {
		fullPath := filepath.Join(root, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
		require.NoError(t, os.WriteFile(fullPath, []byte("package example\nimport _ \"encoding/json\"\n"), 0o644))
	}

	importers, err := encodingJSONImporters(root, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"cmd/magus/main.go"}, importers)
}

// encodingJSONImporters returns every Go file under root that imports encoding/json,
// workspace-relative and sorted, minus the allowed set.
func encodingJSONImporters(root string, allowed map[string]bool) ([]string, error) {
	var importers []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipJSONScanDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || allowed[rel] {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if strings.HasPrefix(strings.Trim(imp.Path.Value, "\""), "encoding/json") {
				importers = append(importers, rel)
			}
		}
		return nil
	})
	slices.Sort(importers)
	return importers, err
}

func skipJSONScanDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".cache", ".magus", ".quad", ".claude", "node_modules", "vendor", "third_party", "third-party", "build", "dist", "out":
		return true
	default:
		return false
	}
}
