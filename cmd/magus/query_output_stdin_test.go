package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// newPrintingWorkspace runs a target whose output is printed, and returns its ref.
func newPrintingWorkspace(t *testing.T, output string) (*magus.Magus, string) {
	t.Helper()
	return newRunningWorkspace(t, output, nil)
}

// newRunningWorkspace is newPrintingWorkspace for a target that ends in runErr after printing
// output; a non-nil runErr is a failed run, whose output is stored all the same.
func newRunningWorkspace(t *testing.T, output string, runErr error) (*magus.Magus, string) {
	t.Helper()
	spellName := register.Spell(t, "zzz-output-import", spells.WithTargets("build"),
		spells.WithInvoker(func(ctx context.Context, _ spells.InvokeRequest) (any, error) {
			stdout, _ := spawn.OutputWriters(ctx)
			fmt.Fprint(stdout, output)
			return nil, runErr
		}))

	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()
	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	m, err := magus.Open(context.Background(), root, magus.WithWorkspaceRegistry(reg), magus.WithSpawnRule(magus.TierCeiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	if runErr == nil {
		require.NoError(t, m.Run(ctx, []types.Target{{Path: ".", Name: "build"}}))
	} else {
		require.Error(t, m.Run(ctx, []types.Target{{Path: ".", Name: "build"}}))
	}
	key, _, err := m.ComputeTargetKey(ctx, ".", "build", nil)
	require.NoError(t, err)
	return m, cache.PortableRef(key)
}

func exportJSONL(t *testing.T, ctx context.Context, ref string) string {
	t.Helper()
	return captureStdout(t, func() {
		require.NoError(t, queryOutputRef(ctx, "", ref, outputRefOpts{out: output.Options{Format: outputJSONL}}))
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
		require.NoError(t, queryOutputRef(ctx, "", ref, outputRefOpts{out: output.Options{Format: outputText}}))
	})
	require.Contains(t, local, "FAIL")
	jsonl := exportJSONL(t, ctx, ref)
	assert.Equal(t, 1, strings.Count(jsonl, "\n"), "-o jsonl writes one record per line")

	var stdout, stderr bytes.Buffer
	require.NoError(t, cache.PrintOutputRecords(strings.NewReader(jsonl), &stdout, &stderr))
	assert.Equal(t, local, stdout.String())
	assert.Contains(t, stderr.String(), ref+" from stdin: not run here, not stored, never a cache hit")
	assert.Contains(t, stderr.String(), "(this machine: "+runtime.GOOS+"/"+runtime.GOARCH+")")
}

// A drift check fails with an empty log and puts the reason, which files and the command
// that fixes them, in the descriptor alone; the text form used to print nothing of it.
func TestQueryOutputTextPrintsAFailedRunsErrorToStderr(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	m, ref := newRunningWorkspace(t, "", errors.New("generated files differ: run `magus run generate:rw`\x1b[31m"))
	ctx := withMagus(context.Background(), m)

	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			require.NoError(t, queryOutputRef(ctx, "", ref, outputRefOpts{out: output.Options{Format: outputText}}))
		})
	})

	assert.Empty(t, stdout, "stdout stays the bytes the run wrote, and this run wrote none")
	assert.Contains(t, stderr, "magus query output: ", "the error is a notice through the display, so -q and -s govern it")
	assert.Contains(t, stderr, "generated files differ: run `magus run generate:rw`")
	assert.NotContains(t, stderr, "\x1b", "a recorded error is neutralized before it reaches the terminal")
}

// A composed target's own log restates the member's error; the findings are under the
// member's ref, so every reader of the failed descriptor names it.
func TestQueryOutputNamesTheFailedNeed(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	const memberRef = "out0123456789ab"
	member := cache.WrapNeedFailure(errors.New("golangci exited 1"), cache.NeedMember{Target: "lint-go", Ref: memberRef})
	m, ref := newRunningWorkspace(t, "", fmt.Errorf("ctx.needs: %w", member))
	ctx := withMagus(context.Background(), m)
	want := "magus query output " + memberRef

	t.Run("text", func(t *testing.T) {
		stderr := captureStderr(t, func() {
			require.NoError(t, queryOutputRef(ctx, "", ref, outputRefOpts{out: output.Options{Format: outputText}}))
		})
		assert.Contains(t, stderr, "ctx.needs: golangci exited 1", "the error notice is unchanged")
		assert.Contains(t, stderr, "lint-go")
		assert.Contains(t, stderr, want)
	})

	t.Run("identity", func(t *testing.T) {
		stdout := captureStdout(t, func() {
			require.NoError(t, queryOutputRef(ctx, "", ref, outputRefOpts{identity: true, out: output.Options{Format: outputText}}))
		})
		assert.Contains(t, stdout, "ctx.needs: golangci exited 1")
		assert.Contains(t, stdout, want)
	})

	t.Run("json", func(t *testing.T) {
		stdout := captureStdout(t, func() {
			require.NoError(t, queryOutputRef(ctx, "", ref, outputRefOpts{out: output.Options{Format: outputJSON}}))
		})
		assert.Contains(t, stdout, `"failed_need_target": "lint-go"`)
		assert.Contains(t, stdout, `"failed_need_ref": "`+memberRef+`"`)
	})

	t.Run("imported record", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		require.NoError(t, cache.PrintOutputRecords(strings.NewReader(exportJSONL(t, ctx, ref)), &stdout, &stderr))
		assert.Contains(t, stderr.String(), want)
		assert.Contains(t, stderr.String(), "lint-go")
	})
}

// The real path: a Buzz composer whose ctx.needs member throws. The composer's descriptor
// names the member and the ref the member's own run stored, the member's error text is
// still the one hopChain reads, and two hops down it is the deepest member that is named.
func TestComposedTargetRecordsTheFailedNeedsRef(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	w := testkit.NewWorkspace(t)
	root := w.Root()
	const mf = `import "magus";
export fun lint_go(ctx: magus\Context, args: [str]) > void !> any {
    throw "golangci found 3 issues";
}
export fun lint_all(ctx: magus\Context, args: [str]) > void !> any {
    ctx.needs(lint_go);
}
export fun lint(ctx: magus\Context, args: [str]) > void !> any {
    ctx.needs(lint_all);
}
magus\project({"targets": {
    "lint": {"skip_cache": "composes"},
    "lint-all": {"skip_cache": "composes"}
}})
`
	w.Magusfile(mf)
	m, err := magus.Open(context.Background(), root, magus.WithSpawnRule(magus.TierCeiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()

	require.Error(t, m.Run(ctx, []types.Target{{Path: ".", Name: "lint"}}))

	byTarget := func(target string) magus.OutputDescriptor {
		key, _, err := m.ComputeTargetKey(ctx, ".", target, nil)
		require.NoError(t, err, target)
		_, d, err := m.OutputByRef(ctx, cache.PortableRef(key))
		require.NoError(t, err, target)
		require.True(t, d.Failed, target)
		return d
	}
	for _, target := range []string{"lint", "lint-all"} {
		d := byTarget(target)
		assert.Contains(t, d.ErrMsg, "ctx.needs: ", target)
		assert.Contains(t, d.ErrMsg, "golangci found 3 issues", target)
		assert.Equal(t, "lint-go", d.FailedNeedTarget, target, "the deepest member, not lint-all")
		require.NotEmpty(t, d.FailedNeedRef, target)

		_, member, err := m.OutputByRef(ctx, d.FailedNeedRef)
		require.NoError(t, err, target)
		assert.True(t, member.Failed)
		assert.Equal(t, "lint-go", member.Target)
		assert.Empty(t, member.FailedNeedTarget, "the throwing member composes nothing")
	}
}

func TestQueryOutputTextPrintsNoErrorForAPassingRun(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	m, ref := newPrintingWorkspace(t, "fine\n")
	ctx := withMagus(context.Background(), m)

	stderr := captureStderr(t, func() {
		out := captureStdout(t, func() {
			require.NoError(t, queryOutputRef(ctx, "", ref, outputRefOpts{out: output.Options{Format: outputText}}))
		})
		assert.Equal(t, "fine\n", out)
	})

	assert.NotContains(t, stderr, "error:")
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
		require.NoError(t, queryCommand(ctx, "", []string{"output", "--stdin"}))
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
		assert.Error(t, queryCommand(context.Background(), "", args), "%v", args)
	}
	assert.Error(t, queryCommand(context.Background(), "", []string{"output", "out1a2b3c", "--stdin"}), "--stdin takes no ref")

	global.output = string(outputJSON)
	assert.Error(t, queryCommand(context.Background(), "", []string{"output", "--stdin"}), "-o json with --stdin")
}

func jsonMarshalLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := renderJSONL(&buf, []any{v}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
