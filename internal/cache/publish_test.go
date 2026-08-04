package cache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runPkg "github.com/egladman/magus/internal/proc/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRemoteHitResolvesProducersRef is Phase 3's acceptance check: machine A builds
// and pushes; machine B, with an EMPTY cache, gets a remote hit and resolves A's
// exact ref to A's bytes - instead of minting a fresh local ref for identical inputs.
func TestRemoteHitResolvesProducersRef(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err, "NewFSRemoteBackend")
	pub, seed := genKeypair(t)
	trusted := [][]byte{pub}

	rootA, cA := openSigned(t, remote, seed, trusted)
	writeMain(t, rootA, "package main")
	stepA := makeStep(rootA)
	stepA.Target = "build"
	rA, err := cA.Run(context.Background(), stepA, func(ctx context.Context) error {
		stdout, _ := runPkg.OutputWriters(ctx)
		_, _ = stdout.Write([]byte("built on machine A\n"))
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, rA.Ref)

	// Machine B: same workspace content, empty cache.
	rootB, cB := openSigned(t, remote, nil, trusted)
	writeMain(t, rootB, "package main")
	stepB := makeStep(rootB)
	stepB.Target = "build"
	rB, err := cB.Run(context.Background(), stepB, func(context.Context) error {
		t.Fatal("machine B rebuilt instead of replaying the remote entry")
		return nil
	})
	require.NoError(t, err)
	require.True(t, rB.Hit, "machine B should hit the remote entry")
	assert.Equal(t, rA.Ref, rB.Ref, "machine B must resolve the ref machine A printed")

	data, desc, err := cB.outputs.ByRef(rA.Ref)
	require.NoError(t, err, "A's ref must resolve locally on B")
	assert.Contains(t, string(data), "built on machine A", "B serves A's captured bytes")
	assert.Equal(t, "test/pkg", desc.Project)

	lines, err := cB.outputs.KeyLinesByRef(rA.Ref)
	require.NoError(t, err, "the key lines travel too, so B can diff its key against A's")
	assert.Equal(t, rA.Hash, hashOfLines(lines), "the imported lines re-derive the shared key")
}

// TestImportRejectsTamperedLog: the signature now covers every non-manifest member,
// so flipping the build log inside an otherwise valid artifact fails the import - and
// nothing from the rejected artifact is left on disk.
func TestImportRejectsTamperedLog(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err, "NewFSRemoteBackend")
	pub, seed := genKeypair(t)
	trusted := [][]byte{pub}

	rootA, cA := openSigned(t, remote, seed, trusted)
	rA, _ := buildCanonical(t, rootA, cA)
	require.NotEmpty(t, rA.Hash)

	// Rewrite the pushed artifact with a poisoned log member.
	stored := findBackendArtifact(t, remote, "test__pkg")
	poisoned := rewriteTarMember(t, stored, func(name string) bool {
		return filepath.Ext(name) == ".log"
	}, []byte("MALICIOUS: pretend this build passed\n"))
	require.NoError(t, os.WriteFile(stored, poisoned, 0o644))

	_, cB := openSigned(t, remote, nil, trusted)
	err = cB.importArtifact(context.Background(), bytes.NewReader(poisoned))
	require.Error(t, err, "a tampered log must fail verification")
	assert.Contains(t, err.Error(), "signature")

	// The rejected artifact leaves nothing behind - not even the log it smuggled.
	var found []string
	_ = filepath.WalkDir(cB.dir, func(p string, d os.DirEntry, werr error) error {
		if werr == nil && !d.IsDir() {
			found = append(found, p)
		}
		return nil
	})
	for _, p := range found {
		assert.NotContains(t, p, ".log", "a rejected import must not leave a log on disk: %s", p)
	}
}

// TestPublishedBundleResolvesRemotely covers the failure-sharing path: a FAILING run
// is never pushed with the cache, so it is published explicitly, and a teammate then
// resolves that exact ref through the remote fallback.
func TestPublishedBundleResolvesRemotely(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err, "NewFSRemoteBackend")
	pub, seed := genKeypair(t)
	trusted := [][]byte{pub}

	rootA, cA := openSigned(t, remote, seed, trusted)
	writeMain(t, rootA, "package main")
	step := makeStep(rootA)
	step.Target = "test"
	boom := errors.New("exit status 1")
	_, err = cA.Run(context.Background(), step, func(ctx context.Context) error {
		stdout, _ := runPkg.OutputWriters(ctx)
		_, _ = stdout.Write([]byte("FAIL: assertion failed on machine A\n"))
		return boom
	})
	require.ErrorIs(t, err, boom)

	hash, err := cA.hashStep(context.Background(), &step)
	require.NoError(t, err)
	ref := cA.outputs.StepRef(hash)
	require.NotEmpty(t, ref)

	// Machine B cannot resolve it yet: a failure is never shared automatically.
	_, cB := openSigned(t, remote, nil, trusted)
	_, _, err = cB.OutputByRef(context.Background(), ref)
	require.ErrorIs(t, err, fs.ErrNotExist)
	var missing *RefNotFoundError
	require.ErrorAs(t, err, &missing, "a miss must name the stores consulted")
	assert.Len(t, missing.Stores, 2)

	published, err := cA.PublishOutput(context.Background(), ref)
	require.NoError(t, err, "publish")
	assert.Equal(t, ref, published, "publishing does not change the ref")

	data, desc, err := cB.OutputByRef(context.Background(), ref)
	require.NoError(t, err, "machine B resolves the published ref")
	assert.Contains(t, string(data), "FAIL: assertion failed on machine A")
	assert.True(t, desc.Failed)
	assert.Equal(t, "test/pkg", desc.Project)
}

// TestPublishRequiresSigningKey: an unsigned bundle would be refused by every
// consumer, so publishing without a key fails loudly at the source instead.
func TestPublishRequiresSigningKey(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err, "NewFSRemoteBackend")
	rootA, cA := openSigned(t, remote, nil, nil) // no signing key, insecure remote
	r, _ := buildCanonical(t, rootA, cA)
	require.NotEmpty(t, r.Ref)

	_, err = cA.PublishOutput(context.Background(), r.Ref)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signing key")
}

// TestBundleRejectsTamperedOutput: the bundle signature covers the captured bytes, so
// a store that swaps them is caught rather than believed.
func TestBundleRejectsTamperedOutput(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err, "NewFSRemoteBackend")
	pub, seed := genKeypair(t)
	trusted := [][]byte{pub}

	rootA, cA := openSigned(t, remote, seed, trusted)
	r, _ := buildCanonical(t, rootA, cA)
	_, err = cA.PublishOutput(context.Background(), r.Ref)
	require.NoError(t, err)

	stored := findBackendArtifact(t, remote, bundleProject)
	poisoned := rewriteTarMember(t, stored, func(name string) bool { return name == bundleOutputName },
		[]byte("everything is fine\n"))

	_, cB := openSigned(t, remote, nil, trusted)
	_, _, err = cB.readBundle(bytes.NewReader(poisoned))
	require.Error(t, err, "a tampered bundle payload must fail verification")
	assert.Contains(t, err.Error(), "signature")
}

// findBackendArtifact returns the single stored object in a dir-backed remote whose
// path contains want ("" for the only object). A run that both pushes its cache
// artifact and publishes a bundle stores two, under different namespaces.
func findBackendArtifact(t *testing.T, backend RemoteBackend, want string) string {
	t.Helper()
	fsb, ok := backend.(*FSRemoteBackend)
	require.True(t, ok, "expected a dir-backed remote")
	var found []string
	require.NoError(t, filepath.WalkDir(fsb.dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(p, want) {
			found = append(found, p)
		}
		return nil
	}))
	require.Len(t, found, 1, "expected exactly one stored object matching %q", want)
	return found[0]
}

// rewriteTarMember returns the gzip-tar at path with the first member matching sel
// replaced by body, every other member preserved verbatim.
func rewriteTarMember(t *testing.T, path string, sel func(name string) bool, body []byte) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	gzr, err := gzip.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	defer gzr.Close()

	var out bytes.Buffer
	gzw := gzip.NewWriter(&out)
	tw := tar.NewWriter(gzw)
	tr := tar.NewReader(gzr)
	replaced := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		if !replaced && sel(hdr.Name) {
			data, replaced = body, true
		}
		hdr.Size = int64(len(data))
		require.NoError(t, tw.WriteHeader(hdr))
		_, err = tw.Write(data)
		require.NoError(t, err)
	}
	require.True(t, replaced, "no member matched the selector")
	require.NoError(t, tw.Close())
	require.NoError(t, gzw.Close())
	return out.Bytes()
}
