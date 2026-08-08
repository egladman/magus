package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	runPkg "github.com/egladman/magus/internal/proc/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The contracts in this file are the ones portable refs REST ON. Each is cheap to
// break by accident from a long way away (the hit path, the resolver, a shape
// constant, a consumer's regexp) and expensive to notice, because the system keeps
// working locally - refs simply stop meaning the same thing on two machines. Line
// coverage does not protect any of them; these assertions do.

// realRefShape is what a run actually prints: the prefix plus exactly refHexLen hex.
var realRefShape = regexp.MustCompile(`^out[0-9a-f]{12}$`)

// TestRefShapeContract pins the WIRE SHAPE of both id kinds, because they are pasted
// into terminals, matched by consumers' own patterns (the MCP hint scanner), and
// quoted in docs. Changing a length is legitimate; changing it ACCIDENTALLY is what
// this catches. It also pins that both shapes satisfy the two public predicates, so a
// consumer scanning free text keeps recognizing real ids.
func TestRefShapeContract(t *testing.T) {
	assert.Equal(t, "out", RefPrefix)
	assert.Equal(t, 12, refHexLen, "a ref is out + 12 hex; changing this changes every printed id")
	assert.Equal(t, 8, attemptHexLen, "an attempt keeps the pre-portable width, so old ids still parse")

	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "build"
	r, err := c.Run(context.Background(), step, func(context.Context) error { return nil })
	require.NoError(t, err)

	require.Regexp(t, realRefShape, r.Ref, "a real run must print out + 12 hex")
	assert.Len(t, r.Ref, 15, "callers size terminal output and test fixtures against this")
	assert.True(t, LooksLikeRef(r.Ref), "`magus query output` must accept what a run printed")
	assert.True(t, IsMintedRef(r.Ref), "a free-text scanner must recognize what a run printed")

	attempts, err := c.outputs.Attempts(r.Ref)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.True(t, IsMintedRef(attempts[0].Attempt), "an attempt id must also be recognizable")
	assert.True(t, LooksLikeRef(attempts[0].Attempt))
}

// TestCacheKeyUnaffectedByPlatform pins that Manifest.Platform (the replay-time
// gate added alongside portable refs) is NOT a key input. The key is what an
// output ref truncates, and a ref must be identical on every machine - that is
// the whole portable-ref feature - so platform must only ever be consulted after
// the key is computed, never folded into hashStepInputs. Two Cache instances that
// differ ONLY in platform must hash the same step to the same key.
func TestCacheKeyUnaffectedByPlatform(t *testing.T) {
	root := t.TempDir()
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "build"

	cLinux, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"), withPlatform("linux/amd64"))
	require.NoError(t, err)
	cDarwin, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"), withPlatform("darwin/arm64"))
	require.NoError(t, err)

	hLinux, err := cLinux.hashStep(context.Background(), &step)
	require.NoError(t, err)
	hDarwin, err := cDarwin.hashStep(context.Background(), &step)
	require.NoError(t, err)

	assert.Equal(t, hLinux, hDarwin, "cache key must be byte-identical across platforms")
}

// TestCacheHitReusesTheSameRef is THE portability contract, and the easiest one to
// break from far away: a hit must answer with the ref the miss printed. If a hit ever
// minted a fresh id, every existing test would still pass while two machines - or the
// same machine twice - silently stopped agreeing on what a run is called.
func TestCacheHitReusesTheSameRef(t *testing.T) {
	root, cdir, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}
	build := func(context.Context) error { return os.WriteFile(out, []byte("built"), 0o644) }

	miss, err := c.Run(context.Background(), step, build)
	require.NoError(t, err)
	require.False(t, miss.Hit)
	require.NotEmpty(t, miss.Ref)

	c2, err := Open(t.Context(), cdir, WithMutable(false))
	require.NoError(t, err)
	hit, err := c2.Run(context.Background(), step, build)
	require.NoError(t, err)
	require.True(t, hit.Hit, "second run must hit")
	assert.Equal(t, miss.Ref, hit.Ref, "a hit must reuse the miss's ref, never mint a new one")

	// And the ref still resolves to the captured bytes after the hit.
	_, desc, err := c2.outputs.ByRef(hit.Ref)
	require.NoError(t, err)
	assert.Equal(t, "test/pkg", desc.Project)
}

// TestFailingRunRefResolves: failures are the outputs people most want to retrieve,
// and they take a different code path (never snapshotted, never pushed). The ref a
// failing run prints must resolve to that run's bytes.
func TestFailingRunRefResolves(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "test"

	boom := errors.New("exit status 1")
	res, err := c.Run(context.Background(), step, func(ctx context.Context) error {
		stdout, _ := runPkg.OutputWriters(ctx)
		fmt.Fprintln(stdout, "FAIL: one assertion")
		return boom
	})
	require.ErrorIs(t, err, boom)
	require.Regexp(t, realRefShape, res.Ref, "a failing run prints a real ref too")

	data, desc, err := c.outputs.ByRef(res.Ref)
	require.NoError(t, err)
	assert.Contains(t, string(data), "FAIL: one assertion")
	assert.True(t, desc.Failed)
}

// TestConcurrentPersistsShareRefWithDistinctAttempts: the store advertises safety for
// concurrent Persist. Two executions of one step must agree on the ref and disagree on
// the attempt - if attempts ever collided, one run's output would overwrite another's.
func TestConcurrentPersistsShareRefWithDistinctAttempts(t *testing.T) {
	s := NewOutputStore(t.TempDir())
	const key = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	const n = 8

	var wg sync.WaitGroup
	refs := make([]string, n)
	attempts := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stored, err := s.Persist(context.Background(), key,
				[]byte(fmt.Sprintf("run %d\n", i)), OutputDescriptor{Project: "p", Target: "build"})
			if err == nil {
				refs[i], attempts[i] = stored.Ref, stored.Attempt
			}
		}()
	}
	wg.Wait()

	seen := map[string]struct{}{}
	for i := range n {
		require.Equal(t, PortableRef(key), refs[i], "every concurrent execution shares the step's ref")
		require.NotEmpty(t, attempts[i])
		_, dup := seen[attempts[i]]
		require.False(t, dup, "attempt ids collided: one run would overwrite another")
		seen[attempts[i]] = struct{}{}
	}
}

// TestOldRefStillResolvesAfterUpgrade: a ref copied from scrollback, a ticket, or a CI
// log written by the PREVIOUS magus is 11 chars. Upgrading must not turn those into
// dead ids while the outputs are still on disk.
func TestOldRefStillResolvesAfterUpgrade(t *testing.T) {
	dir := t.TempDir()
	const key = "9988776655443322119900aabbccddee"
	keyDir := filepath.Join(dir, "outputs", key)
	require.NoError(t, os.MkdirAll(keyDir, 0o755))
	const oldRef = "outfeedface" // out + 8 hex, exactly what a pre-portable run printed
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, oldRef+outExt), []byte("legacy output\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, oldRef+descExt),
		[]byte(`{"ref":"`+oldRef+`","project":"pkg/a","target":"build","failed":false,"timestamp_ms":1,"duration_ms":1}`), 0o644))

	s := NewOutputStore(dir)
	data, _, err := s.ByRef(oldRef)
	require.NoError(t, err, "an id printed by the previous magus must still resolve")
	assert.Equal(t, "legacy output\n", string(data))

	// A git-style short prefix of the old id keeps working too.
	data, _, err = s.ByRef("outfeed")
	require.NoError(t, err)
	assert.Equal(t, "legacy output\n", string(data))
}
