package cache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRemoteBackendFSBuiltOnce verifies that two independent caches sharing a
// single FSRemoteBackend dedup builds: the first workspace misses (builds, pushes
// to remote); the second workspace for the same spec gets a remote hit (fn
// never runs, output is replayed) — i.e. the second workspace restores instead
// of rebuilding.
func TestRemoteBackendFSBuiltOnce(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err, "NewFSRemoteBackend")

	openWithRemote := func(t *testing.T) (root string, c *Cache) {
		t.Helper()
		root = t.TempDir()
		c, err = Open(
			filepath.Join(t.TempDir(), ".magus"),
			WithMutable(true),
			WithRemoteBackend(remote),
			WithInsecureRemote(),
		)
		require.NoError(t, err, "cache.Open")
		return root, c
	}

	// workspace 1: local miss → build → push to remote
	root1, c1 := openWithRemote(t)
	writeMain(t, root1, "package main")
	out1 := touchOut(t, root1)
	spec1 := makeSpec(root1)
	spec1.Outputs = []string{"test/pkg/out.txt"}

	runs := 0
	r1, err := c1.Run(context.Background(), spec1, func(_ context.Context) error {
		runs++
		return os.WriteFile(out1, []byte("built"), 0o644)
	})
	require.NoError(t, err, "Run c1")
	assert.False(t, r1.Hit, "c1: expected cache miss, got hit")
	assert.Equal(t, 1, runs, "c1: expected 1 fn call")

	// workspace 2: same sources, empty local cache → remote hit
	root2, c2 := openWithRemote(t)
	// Reproduce the same source content so the spec hash matches.
	writeMain(t, root2, "package main")
	out2 := touchOut(t, root2)
	spec2 := makeSpec(root2)
	spec2.Outputs = []string{"test/pkg/out.txt"}

	r2, err := c2.Run(context.Background(), spec2, func(_ context.Context) error {
		runs++ // must not be called
		return os.WriteFile(out2, []byte("built"), 0o644)
	})
	require.NoError(t, err, "Run c2")
	assert.True(t, r2.Hit, "c2: expected remote hit, got miss (fn ran again)")
	assert.Equal(t, 1, runs, "remote restore should have prevented a second build")

	got, err := os.ReadFile(out2)
	require.NoError(t, err, "c2: output not restored")
	assert.Equal(t, "built", string(got), "c2: output")
}

// staticBackend serves one pre-built entry for a single (project, hash) and
// discards pushes. It lets a test feed importArtifact a hand-crafted (here:
// poisoned) entry through the real fetch-and-replay path.
type staticBackend struct {
	project, hash string
	entry         []byte
}

func (b *staticBackend) Active(context.Context) bool { return true }

func (b *staticBackend) GetArtifact(_ context.Context, project, hash string) (io.ReadCloser, error) {
	if project != b.project || hash != b.hash {
		return nil, nil // miss
	}
	return io.NopCloser(bytes.NewReader(b.entry)), nil
}

func (b *staticBackend) PutArtifact(_ context.Context, _, _ string, r io.Reader) error {
	_, _ = io.Copy(io.Discard, r) // drain so the export pipe closes cleanly
	return nil
}

// flattenProject mirrors the cache's project→directory mapping for tar paths.
func flattenProject(p string) string {
	return strings.NewReplacer("/", "__", "\\", "__").Replace(p)
}

// buildEntry assembles a gzip-tar cache entry in the on-disk format exportArtifact
// produces: one manifest plus the named CAS blobs. casBlobs maps a blob name (the
// value the manifest references) to the bytes actually stored under it — so a test
// can store bytes that do *not* hash to their name, simulating a store that serves
// content not matching its content-address.
func buildEntry(t *testing.T, project, hash, outPath, manifestBlob string, casBlobs map[string][]byte) []byte {
	t.Helper()
	m := Manifest{
		ProjectPath: project,
		Hash:        hash,
		Outputs:     []OutputRecord{{Path: outPath, Blob: manifestBlob, Mode: 0o644, Size: 5}},
		CreatedAt:   time.Now().UTC(),
	}
	mb, err := json.Marshal(m)
	require.NoError(t, err, "marshal manifest")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, data []byte) {
		require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Size: int64(len(data)), Mode: 0o644}), "tar header %s", name)
		_, err := tw.Write(data)
		require.NoError(t, err, "tar write %s", name)
	}
	add("manifests/"+flattenProject(project)+"/"+hash+".json", mb)
	for name, data := range casBlobs {
		add("cas/"+name[:2]+"/"+name, data)
	}
	require.NoError(t, tw.Close(), "tar close")
	require.NoError(t, gz.Close(), "gzip close")
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// learnHash builds the canonical spec once in a throwaway local cache to discover
// the content hash the remote entry must be keyed under.
func learnHash(t *testing.T) (project, hash string) {
	t.Helper()
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}
	out := filepath.Join(root, "test", "pkg", "out.txt")
	r, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("built"), 0o644)
	})
	require.NoError(t, err, "learnHash run")
	return spec.ProjectPath, r.Hash
}

// runAgainst opens a fresh cache wired to backend, runs the canonical spec, and
// reports whether it hit and what the output ended up as. The fn writes "built",
// so a genuine rebuild yields "built" while an accepted poisoned entry would yield
// the poisoned bytes.
func runAgainst(t *testing.T, backend RemoteBackend) (hit bool, output string, ran bool) {
	t.Helper()
	root := t.TempDir()
	c, err := Open(filepath.Join(t.TempDir(), ".magus"),
		WithMutable(true), WithRemoteBackend(backend),
		WithInsecureRemote()) // integrity-only path: no trust set
	require.NoError(t, err, "cache.Open")
	writeMain(t, root, "package main")
	touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}
	out := filepath.Join(root, "test", "pkg", "out.txt")
	r, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		ran = true
		return os.WriteFile(out, []byte("built"), 0o644)
	})
	require.NoError(t, err, "run")
	data, err := os.ReadFile(out)
	require.NoError(t, err, "read output")
	return r.Hit, string(data), ran
}

// TestRemoteRejectsPoisonedBlob is the core supply-chain defense: a store that
// serves a blob whose bytes do not hash to its content-address must never be
// replayed. The entry's manifest references blob=sha256("built") but the stored
// blob contains "EVIL!" — importArtifact must reject it, so the build falls back to
// a local rebuild instead of writing the poisoned bytes into the tree.
func TestRemoteRejectsPoisonedBlob(t *testing.T) {
	project, hash := learnHash(t)
	goodBlob := sha256Hex([]byte("built"))
	entry := buildEntry(t, project, hash, "test/pkg/out.txt", goodBlob,
		map[string][]byte{goodBlob: []byte("EVIL!")}) // stored bytes ≠ name

	hit, output, ran := runAgainst(t, &staticBackend{project: project, hash: hash, entry: entry})
	assert.False(t, hit, "poisoned entry was replayed as a cache hit; the store was trusted")
	assert.True(t, ran, "expected a local rebuild after rejecting the poisoned entry")
	assert.Equal(t, "built", output, "poisoned bytes must never reach the tree")
}

// TestRemoteRejectsMissingBlob covers the completeness check: a manifest that
// references a blob absent from the entry must be rejected (never committed), so a
// later replay can't read a dangling or substituted CAS path.
func TestRemoteRejectsMissingBlob(t *testing.T) {
	project, hash := learnHash(t)
	referenced := sha256Hex([]byte("built"))
	// Ship a self-consistent but unrelated blob; the referenced one is absent.
	decoy := sha256Hex([]byte("decoy"))
	entry := buildEntry(t, project, hash, "test/pkg/out.txt", referenced,
		map[string][]byte{decoy: []byte("decoy")})

	hit, output, ran := runAgainst(t, &staticBackend{project: project, hash: hash, entry: entry})
	assert.False(t, hit, "entry with a missing referenced blob was replayed as a hit")
	assert.True(t, ran, "expected local rebuild after rejecting the entry")
	assert.Equal(t, "built", output, "expected local rebuild to built")
}

func genKeypair(t *testing.T) (pub []byte, seed []byte) {
	t.Helper()
	pk, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err, "generate key")
	return pk, priv.Seed()
}

// openSigned opens a fresh cache wired to remote, optionally with a signing seed
// and/or a trust set.
func openSigned(t *testing.T, remote RemoteBackend, seed []byte, trusted [][]byte) (root string, c *Cache) {
	t.Helper()
	root = t.TempDir()
	opts := []Option{WithMutable(true), WithRemoteBackend(remote)}
	if seed != nil {
		opts = append(opts, WithSigningKey(seed))
	}
	if trusted != nil {
		opts = append(opts, WithTrustedKeys(trusted))
	} else {
		opts = append(opts, WithInsecureRemote()) // no trust set: unsigned-producer case
	}
	c, err := Open(filepath.Join(t.TempDir(), ".magus"), opts...)
	require.NoError(t, err, "cache.Open")
	return root, c
}

// buildCanonical runs the canonical spec in c against workspace root, writing
// "built" on a miss. It reports the result and whether fn actually ran.
func buildCanonical(t *testing.T, root string, c *Cache) (Result, bool) {
	t.Helper()
	writeMain(t, root, "package main")
	touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}
	out := filepath.Join(root, "test", "pkg", "out.txt")
	ran := false
	r, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		ran = true
		return os.WriteFile(out, []byte("built"), 0o644)
	})
	require.NoError(t, err, "run")
	return r, ran
}

// TestRemoteSignedRoundTrip: an entry signed by a trusted key on push is accepted
// and replayed on a fresh machine that trusts that key.
func TestRemoteSignedRoundTrip(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err, "NewFSRemoteBackend")
	pub, seed := genKeypair(t)
	trusted := [][]byte{pub}

	// Producer: signs and pushes.
	root1, c1 := openSigned(t, remote, seed, trusted)
	r1, ran1 := buildCanonical(t, root1, c1)
	assert.False(t, r1.Hit, "producer: expected miss")
	assert.True(t, ran1, "producer: expected build")

	// Consumer: verifies and replays — fn must not run.
	root2, c2 := openSigned(t, remote, nil, trusted)
	r2, ran2 := buildCanonical(t, root2, c2)
	assert.True(t, r2.Hit, "consumer: expected a verified remote hit, got miss")
	assert.False(t, ran2, "consumer: fn ran; the signed entry should have replayed")
}

// TestRemoteRejectsUnsignedEntry: with a trust set configured, an unsigned entry
// (pushed by a producer with no signing key — e.g. a developer laptop) is refused
// and the consumer rebuilds locally.
func TestRemoteRejectsUnsignedEntry(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err, "NewFSRemoteBackend")
	pub, _ := genKeypair(t)

	// Producer without a signing key pushes an UNSIGNED entry.
	root1, c1 := openSigned(t, remote, nil, nil)
	_, ran := buildCanonical(t, root1, c1)
	require.True(t, ran, "producer: expected a build")

	// Consumer with a trust set must refuse the unsigned entry.
	root2, c2 := openSigned(t, remote, nil, [][]byte{pub})
	r2, ran2 := buildCanonical(t, root2, c2)
	assert.False(t, r2.Hit, "consumer replayed an unsigned entry despite a configured trust set")
	assert.True(t, ran2, "consumer: expected a local rebuild after refusing the unsigned entry")
}

// TestRemoteRejectsUntrustedSigner: an entry validly signed by key A is refused by
// a consumer that trusts only key B.
func TestRemoteRejectsUntrustedSigner(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err, "NewFSRemoteBackend")
	pubA, seedA := genKeypair(t)
	pubB, _ := genKeypair(t)

	// Producer signs with A.
	root1, c1 := openSigned(t, remote, seedA, [][]byte{pubA})
	_, ran := buildCanonical(t, root1, c1)
	require.True(t, ran, "producer: expected a build")

	// Consumer trusts only B → must refuse A's entry.
	root2, c2 := openSigned(t, remote, nil, [][]byte{pubB})
	r2, ran2 := buildCanonical(t, root2, c2)
	assert.False(t, r2.Hit, "consumer replayed an entry signed by an untrusted key")
	assert.True(t, ran2, "consumer: expected a local rebuild after refusing the untrusted entry")
}

// TestRemoteRequiresTrustSetOrOptOut: the cache package enforces its own trust
// boundary — a remote backend with no trust set is refused at Open unless the
// caller explicitly opts into insecure (unsigned) mode.
func TestRemoteRequiresTrustSetOrOptOut(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err, "NewFSRemoteBackend")
	_, err = Open(filepath.Join(t.TempDir(), ".magus"),
		WithMutable(true), WithRemoteBackend(remote))
	assert.Error(t, err, "Open accepted a remote backend with no trust set and no opt-out")
	_, err = Open(filepath.Join(t.TempDir(), ".magus"),
		WithMutable(true), WithRemoteBackend(remote),
		WithInsecureRemote())
	assert.NoError(t, err, "Open rejected remote + explicit opt-out")
}

type tarMember struct {
	name string
	data []byte
}

// buildRawEntry assembles a gzip-tar from an explicit member list, so a test can
// craft malformed archives (duplicates, reordering) the normal builder won't.
func buildRawEntry(t *testing.T, members []tarMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: m.name, Size: int64(len(m.data)), Mode: 0o644}), "tar header %s", m.name)
		_, err := tw.Write(m.data)
		require.NoError(t, err, "tar write %s", m.name)
	}
	require.NoError(t, tw.Close(), "tar close")
	require.NoError(t, gz.Close(), "gzip close")
	return buf.Bytes()
}

// TestRemoteRejectsDuplicateManifest: a tar carrying two manifest members is a
// malformed/hostile archive (it could shadow a signed manifest with a second one);
// importArtifact rejects it outright rather than letting the last one win.
func TestRemoteRejectsDuplicateManifest(t *testing.T) {
	project, hash := learnHash(t)
	good := sha256Hex([]byte("built"))
	mb, err := json.Marshal(Manifest{
		ProjectPath: project, Hash: hash,
		Outputs:   []OutputRecord{{Path: "test/pkg/out.txt", Blob: good, Mode: 0o644, Size: 5}},
		CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err, "marshal manifest")
	mPath := "manifests/" + flattenProject(project) + "/" + hash + ".json"
	entry := buildRawEntry(t, []tarMember{
		{mPath, mb},
		{"cas/" + good[:2] + "/" + good, []byte("built")},
		{mPath, mb}, // duplicate manifest — must be rejected
	})

	hit, output, ran := runAgainst(t, &staticBackend{project: project, hash: hash, entry: entry})
	assert.False(t, hit, "entry with a duplicate manifest was replayed")
	assert.True(t, ran, "expected a local rebuild")
	assert.Equal(t, "built", output, "expected a local rebuild to built")
}

// TestRemoteRejectsOversizedArchive: two blobs each under the import limit but
// together over it must be rejected — the cap is on the whole archive, not each
// member (a per-member cap would have let both through, the bomb this closes).
func TestRemoteRejectsOversizedArchive(t *testing.T) {
	project, hash := learnHash(t)
	blobA := bytes.Repeat([]byte("A"), 900)
	blobB := bytes.Repeat([]byte("B"), 900)
	hashA, hashB := sha256Hex(blobA), sha256Hex(blobB)
	mb, err := json.Marshal(Manifest{
		ProjectPath: project, Hash: hash,
		Outputs: []OutputRecord{
			{Path: "test/pkg/a", Blob: hashA, Mode: 0o644, Size: 900},
			{Path: "test/pkg/b", Blob: hashB, Mode: 0o644, Size: 900},
		},
		CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err, "marshal manifest")
	mPath := "manifests/" + flattenProject(project) + "/" + hash + ".json"
	entry := buildRawEntry(t, []tarMember{
		{mPath, mb},
		{"cas/" + hashA[:2] + "/" + hashA, blobA},
		{"cas/" + hashB[:2] + "/" + hashB, blobB},
	})

	root := t.TempDir()
	c, err := Open(filepath.Join(t.TempDir(), ".magus"),
		WithMutable(true),
		WithRemoteBackend(&staticBackend{project: project, hash: hash, entry: entry}),
		WithInsecureRemote(),
		WithMaxImportBytes(2000)) // fits manifest + one blob, not both
	require.NoError(t, err, "cache.Open")
	writeMain(t, root, "package main")
	touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}
	out := filepath.Join(root, "test", "pkg", "out.txt")
	ran := false
	r, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		ran = true
		return os.WriteFile(out, []byte("built"), 0o644)
	})
	require.NoError(t, err, "run")
	assert.False(t, r.Hit, "oversized archive was imported as a hit")
	assert.True(t, ran, "expected a local rebuild after rejecting the oversized archive")
}
