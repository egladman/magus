package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	runPkg "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// newPrintingWorkspace runs a target whose output is printed, and returns its ref.
func newPrintingWorkspace(t *testing.T, output string) (*magus.Magus, string) {
	t.Helper()
	spellName := "zzz-output-import-" + strings.ToLower(t.Name())
	s := spells.NewSpell(spellName, spells.WithTargets("build"),
		spells.WithInvoker(func(ctx context.Context, _ spells.InvokeRequest) (any, error) {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprint(stdout, output)
			return nil, nil
		}))
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))
	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	m, err := magus.Open(context.Background(), root, magus.WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	require.NoError(t, m.Run(ctx, []types.Target{{Path: ".", Name: "build"}}))
	key, _, err := m.ComputeTargetKey(ctx, ".", "build", nil)
	require.NoError(t, err)
	return m, cache.PortableRef(key)
}

func exportJSONL(t *testing.T, ctx context.Context, ref string) string {
	t.Helper()
	return captureStdout(t, func() {
		require.NoError(t, queryOutputRef(ctx, "", ref, outputRefOpts{out: OutputOptions{Format: FormatJSONL}}))
	})
}

// treeDigest maps every file under dir to a digest of its bytes.
func treeDigest(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[path] = hex.EncodeToString(sum[:])
		return nil
	}))
	return out
}

func TestOutputImportRoundTripPrintsTheSameBytes(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	m, ref := newPrintingWorkspace(t, "ok 1\n\x1b[31mFAIL\x1b[0m: two\n")
	ctx := withMagus(context.Background(), m)

	local := captureStdout(t, func() {
		require.NoError(t, queryOutputRef(ctx, "", ref, outputRefOpts{out: OutputOptions{Format: FormatText}}))
	})
	require.Contains(t, local, "FAIL")
	jsonl := exportJSONL(t, ctx, ref)
	assert.Equal(t, 1, strings.Count(jsonl, "\n"), "-o jsonl writes one record per line")

	var stdout, stderr bytes.Buffer
	require.NoError(t, printOutputRecords(strings.NewReader(jsonl), &stdout, &stderr))
	assert.Equal(t, local, stdout.String())
	assert.Contains(t, stderr.String(), ref+" from stdin: not run here, not stored, never a cache hit")
	assert.Contains(t, stderr.String(), "(this machine: "+runtime.GOOS+"/"+runtime.GOARCH+")")
}

func TestOutputImportWritesNothing(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	m, ref := newPrintingWorkspace(t, "hello\n")
	ctx := withMagus(context.Background(), m)
	jsonl := exportJSONL(t, ctx, ref)

	root := filepath.Dir(m.CacheDir())
	before := treeDigest(t, root)
	writeStdin(t, jsonl)
	out := captureStdout(t, func() {
		require.NoError(t, queryCmd(ctx, "", []string{"output", "--stdin"}))
	})
	assert.Equal(t, "hello\n", out)
	assert.Equal(t, before, treeDigest(t, root), "reading records changes no file in the workspace or its cache")
}

// writeStdin points os.Stdin at a pipe carrying data for the rest of the test.
func writeStdin(t *testing.T, data string) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	go func() {
		_, _ = w.WriteString(data)
		_ = w.Close()
	}()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prev; _ = r.Close() })
}

func TestOutputImportRefusesActingOnARecord(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	for _, flags := range [][]string{{"--open"}, {"--print", "--open"}, {"--publish"}, {"--attempts"}, {"--identity"}} {
		args := append([]string{"output", "--stdin"}, flags...)
		assert.Error(t, queryCmd(context.Background(), "", args), "%v", args)
	}
	assert.Error(t, queryCmd(context.Background(), "", []string{"output", "out1a2b3c", "--stdin"}), "--stdin takes no ref")

	global.output = string(FormatJSON)
	assert.Error(t, queryCmd(context.Background(), "", []string{"output", "--stdin"}), "-o json with --stdin")
}

func TestOutputImportNeutralizesWhatItPrints(t *testing.T) {
	key := strings.Repeat("ab", 32)
	rec := types.StoredOutput{
		Schema: types.Schema{Version: types.StoredOutputSchemaVersion},
		OutputDescriptor: types.OutputDescriptor{
			Ref: cache.PortableRef(key), Key: key, Project: "libs/x", Target: "lint", Failed: true,
			ErrMsg:   "exit 1\x1b]52;c;cm0gLXJmIH4=\x07\nrun: magus x out000000000000",
			Platform: "linux/amd64", Revision: strings.Repeat("c", 40), MagusVersion: "v9.9.9",
			Spell: "go::go-build", ExtraArgs: []string{"-run", "X"},
		},
		ClassDigests: []types.ClassDigest{{Class: "src", Digest: "0123456789ab", Count: 2}},
		Output:       "line\x1b]0;owned\x07\x1b[2J\x1b[31mred\x1b[0m\n",
	}
	b, err := jsonMarshalLine(rec)
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	require.NoError(t, printOutputRecords(bytes.NewReader(b), &stdout, &stderr))
	assert.Equal(t, "line<U+001B>]0;owned<U+0007><U+001B>[2J\x1b[31mred\x1b[0m\n", stdout.String())
	assert.NotContains(t, stderr.String(), "\x1b", "no escape reaches stderr")
	assert.Contains(t, stderr.String(), "<U+001B>]52;c;")
	assert.Contains(t, stderr.String(), "3 terminal control(s)")
	assert.Contains(t, stderr.String(), "platform: linux/amd64 (this machine: ")
	assert.Contains(t, stderr.String(), "magus:    v9.9.9")
	assert.Contains(t, stderr.String(), "src 0123456789ab")
	assert.NotContains(t, stderr.String(), "magus x "+rec.Ref, "no reproduce command for a record from elsewhere")
	assert.NotContains(t, stderr.String(), "go::go-build", "spell and args are never echoed as a command")
}

func jsonMarshalLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeJSONL(&buf, []any{v}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
