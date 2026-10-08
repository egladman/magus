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
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTools = []string{"platform:linux/amd64", "go:go:1.26.6", "go:golangci-lint:2.1.0"}

var testLocks = map[string][]byte{"go.sum": []byte("a v1 h1:x\n"), "libs/x/go.sum": []byte("b v2 h1:y\n")}

func testSpellCacheKey() SpellCacheKey { return NewSpellCacheKey("go", testTools, testLocks) }

const testBundleName = "go-test-20260926"

// goRoots are the roots the go spell's declaration resolves to with its caches in
// gocache and gomod.
func goRoots(gocache, gomod string) []SpellCacheRoot {
	return []SpellCacheRoot{
		{Name: "GOCACHE", Dir: gocache, StampsUse: true, Skip: []string{"README", "trim.txt", "fuzz/**"}},
		{Name: "GOMODCACHE", Dir: gomod, Skip: []string{"cache/vcs/**", "cache/download/**/*.zip", "**/*.lock", "**/*.partial", "**/*.tmp"}},
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
}

// readTree returns every regular file under root, skipping none, so a test sees stray
// staging directories as well as restored files.
func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return filepath.SkipDir
		}
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		data, err := os.ReadFile(p)
		got[filepath.ToSlash(rel)] = string(data)
		return err
	})
	require.NoError(t, err)
	return got
}

func sourceRoots(t *testing.T) []SpellCacheRoot {
	t.Helper()
	src := t.TempDir()
	writeTree(t, filepath.Join(src, "gocache"), map[string]string{
		"ab/ab01-a":      "action",
		"ab/ab01-d":      "output",
		"README":         "not an entry",
		"trim.txt":       "123",
		"fuzz/x/a1b2c3d": "fuzz",
	})
	writeTree(t, filepath.Join(src, "gomod"), map[string]string{
		"example.com/m@v1.0.0/m.go":                      "package m",
		"cache/download/example.com/m/@v/v1.0.0.mod":     "module example.com/m",
		"cache/download/example.com/m/@v/v1.0.0.ziphash": "h1:abc",
		"cache/download/example.com/m/@v/v1.0.0.zip":     "zip bytes",
		"cache/download/example.com/m/@v/v1.0.0.lock":    "",
		"cache/vcs/0123/HEAD":                            "ref",
	})
	require.NoError(t, os.Chmod(filepath.Join(src, "gomod", "example.com", "m@v1.0.0", "m.go"), 0o444))
	return goRoots(filepath.Join(src, "gocache"), filepath.Join(src, "gomod"))
}

func destRoots(t *testing.T) []SpellCacheRoot {
	t.Helper()
	dst := t.TempDir()
	return goRoots(filepath.Join(dst, "gocache"), filepath.Join(dst, "gomod"))
}

func signedBundle(t *testing.T, seed []byte, key SpellCacheKey, roots []SpellCacheRoot) []byte {
	t.Helper()
	s, err := newSigner(seed)
	require.NoError(t, err)
	var buf bytes.Buffer
	_, err = writeSpellCacheBundle(t.Context(), &buf, s, key, testBundleName, roots, time.Now())
	require.NoError(t, err)
	return buf.Bytes()
}

func TestSpellCacheBundleRoundTrip(t *testing.T) {
	pub, seed := genKeypair(t)
	key := testSpellCacheKey()
	data := signedBundle(t, seed, key, sourceRoots(t))

	dst := destRoots(t)
	now := time.Now()
	v, err := newVerifier([][]byte{pub})
	require.NoError(t, err)
	want := spellCacheWant{key: key, name: testBundleName}
	stats, err := readSpellCacheBundle(t.Context(), bytes.NewReader(data), v, want, dst, defaultMaxImportBytes, now)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"ab/ab01-a": "action", "ab/ab01-d": "output"}, readTree(t, dst[0].Dir),
		"what the spell skips stays out; the staging directory is gone")
	assert.Equal(t, map[string]string{
		"example.com/m@v1.0.0/m.go":                      "package m",
		"cache/download/example.com/m/@v/v1.0.0.mod":     "module example.com/m",
		"cache/download/example.com/m/@v/v1.0.0.ziphash": "h1:abc",
	}, readTree(t, dst[1].Dir), "zips, locks and VCS clones stay out")
	assert.Equal(t, SpellCacheStats{Files: 5, Bytes: int64(len("actionoutputpackage mmodule example.com/mh1:abc"))}, stats)

	info, err := os.Stat(filepath.Join(dst[1].Dir, "example.com/m@v1.0.0/m.go"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o444), info.Mode().Perm(), "a file is restored in the mode it was saved in")
	info, err = os.Stat(filepath.Join(dst[0].Dir, "ab/ab01-a"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	assert.WithinDuration(t, now.Add(-restoredAge), info.ModTime(), time.Second,
		"restored entries are backdated so the tool re-stamps the ones a run uses")

	again, err := readSpellCacheBundle(t.Context(), bytes.NewReader(data), v, want, dst, defaultMaxImportBytes, now)
	require.NoError(t, err)
	assert.Equal(t, SpellCacheStats{Skipped: 5}, again, "files already present are left as they are")
}

// A save keeps only what a use-stamping cache dated since the restore: an entry still
// at its restored stamp went unused. A cache that stamps nothing keeps every entry.
func TestSpellCacheSaveLeavesOutEntriesNoRunUsed(t *testing.T) {
	roots := sourceRoots(t)
	now := time.Now()
	stale := now.Add(-restoredAge)
	for _, p := range []string{filepath.Join(roots[0].Dir, "ab", "ab01-d"), filepath.Join(roots[1].Dir, "cache/download/example.com/m/@v/v1.0.0.mod")} {
		require.NoError(t, os.Chtimes(p, stale, stale))
	}
	files, stats, err := collectSpellCacheFiles(t.Context(), roots, now)
	require.NoError(t, err)
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	assert.ElementsMatch(t, []string{
		"GOCACHE/ab/ab01-a",
		"GOMODCACHE/example.com/m@v1.0.0/m.go",
		"GOMODCACHE/cache/download/example.com/m/@v/v1.0.0.mod",
		"GOMODCACHE/cache/download/example.com/m/@v/v1.0.0.ziphash",
	}, paths)
	assert.Equal(t, 1, stats.Skipped)
}

// retar rewrites a bundle member by member; edit returns the new body, or keep=false
// to drop the member.
func retar(t *testing.T, raw []byte, edit func(name string, body []byte) (out []byte, keep bool)) []byte {
	t.Helper()
	gzr, err := gzip.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	var out bytes.Buffer
	gzw := gzip.NewWriter(&out)
	tw := tar.NewWriter(gzw)
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		body, keep := edit(hdr.Name, body)
		if !keep {
			continue
		}
		hdr.Size = int64(len(body))
		require.NoError(t, tw.WriteHeader(hdr))
		_, err = tw.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gzw.Close())
	return out.Bytes()
}

func TestSpellCacheBundleRefusals(t *testing.T) {
	pub, seed := genKeypair(t)
	otherPub, otherSeed := genKeypair(t)
	key := testSpellCacheKey()
	good := signedBundle(t, seed, key, sourceRoots(t))
	want := spellCacheWant{key: key, name: testBundleName}

	otherTools := key
	otherTools.Tools = strings.Repeat("0", 64)
	otherSpell := key
	otherSpell.Spell = "rust"

	cases := []struct {
		name    string
		bundle  []byte
		trusted []byte
		want    spellCacheWant
		err     string
	}{
		{
			name: "a file swapped for bytes of the same size",
			bundle: retar(t, good, func(name string, body []byte) ([]byte, bool) {
				if name == "GOCACHE/ab/ab01-a" {
					return []byte("ACTION"), true
				}
				return body, true
			}),
			trusted: pub, want: want,
			err: "content does not match the signed index",
		},
		{
			name: "a dropped member",
			bundle: retar(t, good, func(name string, body []byte) ([]byte, bool) {
				return body, name != "GOCACHE/ab/ab01-d"
			}),
			trusted: pub, want: want,
			err: "truncated",
		},
		{
			name:    "a key outside the trust set",
			bundle:  good,
			trusted: otherPub, want: want,
			err: "not in trust set",
		},
		{
			name: "an unsigned bundle",
			bundle: retar(t, good, func(name string, body []byte) ([]byte, bool) {
				return body, name != sigFileName
			}),
			trusted: pub, want: want,
			err: "expected signature.json",
		},
		{
			name:    "a bundle of other tools",
			bundle:  signedBundle(t, seed, otherTools, sourceRoots(t)),
			trusted: pub, want: want,
			err: "bundle is for go-",
		},
		{
			name:    "another spell's bundle",
			bundle:  signedBundle(t, seed, otherSpell, sourceRoots(t)),
			trusted: pub, want: want,
			err: "bundle is for rust-",
		},
		{
			name:    "a bundle signed for another remote key",
			bundle:  good,
			trusted: pub, want: spellCacheWant{key: key, name: "go-x-y-20260101"},
			err: "signed for key",
		},
		{
			name:    "a signature from a key the reader does not trust over a valid index",
			bundle:  signedBundle(t, otherSeed, key, sourceRoots(t)),
			trusted: pub, want: want,
			err: "not in trust set",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := newVerifier([][]byte{tc.trusted})
			require.NoError(t, err)
			dst := destRoots(t)
			_, err = readSpellCacheBundle(t.Context(), bytes.NewReader(tc.bundle), v, tc.want, dst, defaultMaxImportBytes, time.Now())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)
			for _, root := range dst {
				assert.Empty(t, readTree(t, root.Dir), "a refused bundle places nothing")
			}
		})
	}

	t.Run("no trust set", func(t *testing.T) {
		_, err := readSpellCacheBundle(t.Context(), bytes.NewReader(good), nil, want, destRoots(t), defaultMaxImportBytes, time.Now())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no trust set")
	})

	t.Run("a member the index does not name", func(t *testing.T) {
		v, err := newVerifier([][]byte{pub})
		require.NoError(t, err)
		smuggled := appendMember(t, good, "GOCACHE/cd/cd01-a", "planted")
		dst := destRoots(t)
		_, err = readSpellCacheBundle(t.Context(), bytes.NewReader(smuggled), v, want, dst, defaultMaxImportBytes, time.Now())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not in the signed index")
		assert.Empty(t, readTree(t, dst[0].Dir))
	})

	t.Run("writing without a signing key", func(t *testing.T) {
		_, err := writeSpellCacheBundle(t.Context(), io.Discard, nil, key, testBundleName, sourceRoots(t), time.Now())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "signing key")
	})
}

func appendMember(t *testing.T, raw []byte, name, body string) []byte {
	t.Helper()
	gzr, err := gzip.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		require.NoError(t, tw.WriteHeader(hdr))
		_, err = tw.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Size: int64(len(body)), Mode: 0o644}))
	_, err = tw.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gzw.Close())
	return buf.Bytes()
}

func TestSpellCacheKeyChangesWithEachInput(t *testing.T) {
	base := testSpellCacheKey()
	for i := range testTools {
		tools := append([]string(nil), testTools...)
		tools[i] += "x"
		got := NewSpellCacheKey("go", tools, testLocks)
		assert.NotEqual(t, base.Tools, got.Tools, "%s is part of the tools key", testTools[i])
		assert.Equal(t, base.Locks, got.Locks, "%s is not part of the lockfile key", testTools[i])
	}
	reordered := []string{testTools[2], testTools[0], testTools[1], testTools[0]}
	assert.Equal(t, base, NewSpellCacheKey("go", reordered, testLocks), "order and repeats of the tool lines do not matter")

	locks := map[string]map[string][]byte{
		"a lockfile's content": {"go.sum": []byte("a v1 h1:z\n"), "libs/x/go.sum": testLocks["libs/x/go.sum"]},
		"a lockfile's path":    {"go.sum": testLocks["go.sum"], "libs/y/go.sum": testLocks["libs/x/go.sum"]},
		"an added lockfile":    {"go.sum": testLocks["go.sum"], "libs/x/go.sum": testLocks["libs/x/go.sum"], "tools/go.sum": nil},
		"a removed lockfile":   {"go.sum": testLocks["go.sum"]},
	}
	for name, l := range locks {
		got := NewSpellCacheKey("go", testTools, l)
		assert.NotEqual(t, base.Locks, got.Locks, "%s changes the lockfile key", name)
		assert.Equal(t, base.Tools, got.Tools, "%s leaves the tools key", name)
	}
	assert.Equal(t, base, NewSpellCacheKey("go", testTools, testLocks), "the key is deterministic")

	day := time.Date(2026, 9, 26, 23, 0, 0, 0, time.UTC)
	assert.Equal(t, base.String()+"-20260926", base.remoteKey(day))
	assert.True(t, strings.HasPrefix(base.remoteKey(day), "go-"), "a bundle is filed under its spell")
	assert.NotEqual(t, base.remoteKey(day), base.remoteKey(day.AddDate(0, 0, 1)), "each day is its own key")
}

func TestSpellCacheRemoteSaveAndRestore(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err)
	pub, seed := genKeypair(t)
	trusted := [][]byte{pub}
	_, writer := openSigned(t, remote, seed, trusted)
	key := testSpellCacheKey()
	now := time.Now()

	saved, err := writer.saveSpellCache(t.Context(), key, sourceRoots(t), now)
	require.NoError(t, err)
	assert.Equal(t, key.remoteKey(now), saved.Key)
	assert.Equal(t, 5, saved.Files)
	assert.Positive(t, saved.Transferred)

	again, err := writer.saveSpellCache(t.Context(), key, sourceRoots(t), now)
	require.NoError(t, err)
	assert.True(t, again.Present, "the day's first bundle stands")
	assert.Zero(t, again.Files, "a stored key is not rebuilt")

	_, reader := openSigned(t, remote, nil, trusted)
	dst := destRoots(t)
	got, err := reader.restoreSpellCache(t.Context(), key, dst, now)
	require.NoError(t, err)
	assert.Equal(t, saved.Key, got.Key)
	assert.True(t, got.Exact)
	assert.Equal(t, 5, got.Files)
	assert.Equal(t, "action", readTree(t, dst[0].Dir)["ab/ab01-a"])

	t.Run("other lockfiles take the tools' newest bundle", func(t *testing.T) {
		moved := key
		moved.Locks = strings.Repeat("1", 64)
		got, err := reader.restoreSpellCache(t.Context(), moved, destRoots(t), now)
		require.NoError(t, err)
		assert.Equal(t, saved.Key, got.Key)
		assert.False(t, got.Exact)
	})

	t.Run("other tools find nothing", func(t *testing.T) {
		other := key
		other.Tools = strings.Repeat("2", 64)
		got, err := reader.restoreSpellCache(t.Context(), other, destRoots(t), now)
		require.NoError(t, err)
		assert.Empty(t, got.Key)
		assert.Empty(t, got.Refused)
	})

	t.Run("a pull request may not save", func(t *testing.T) {
		root := t.TempDir()
		pr, err := Open(t.Context(), filepath.Join(root, ".magus"), WithLocalWrite(true), WithRemoteBackend(remote),
			WithSigningKey(seed), WithTrustedKeys(trusted), WithRemoteWrite(false))
		require.NoError(t, err)
		_, err = pr.saveSpellCache(t.Context(), key, sourceRoots(t), now.AddDate(0, 0, 1))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "may not write (remote writes are off)")
	})

	t.Run("a tampered newest bundle is refused and an older verified one is taken", func(t *testing.T) {
		later := now.AddDate(0, 0, 1)
		_, err := writer.saveSpellCache(t.Context(), key, sourceRoots(t), later)
		require.NoError(t, err)
		stored := remote.artifactPath(spellCacheNamespace, key.remoteKey(later))
		tampered := rewriteTarMember(t, stored, func(name string) bool { return name == "GOCACHE/ab/ab01-d" }, []byte("OUTPUT"))
		require.NoError(t, os.WriteFile(stored, tampered, 0o644))

		dst := destRoots(t)
		got, err := reader.restoreSpellCache(t.Context(), key, dst, later)
		require.NoError(t, err)
		require.Len(t, got.Refused, 1)
		assert.Contains(t, got.Refused[0], key.remoteKey(later))
		assert.Equal(t, key.remoteKey(now), got.Key, "the day before's verified bundle")
		assert.Equal(t, "output", readTree(t, dst[0].Dir)["ab/ab01-d"], "no tampered byte is restored")
	})
}

// unreachableBackend answers every GetArtifact with err and counts the calls.
type unreachableBackend struct {
	err   error
	calls int
}

func (b *unreachableBackend) Name() string                { return "unreachable" }
func (b *unreachableBackend) Active(context.Context) bool { return true }
func (b *unreachableBackend) GetArtifact(context.Context, string, string) (io.ReadCloser, error) {
	b.calls++
	return nil, b.err
}
func (b *unreachableBackend) PutArtifact(context.Context, string, string, io.Reader) error {
	return errors.ErrUnsupported
}
func (b *unreachableBackend) HasArtifact(context.Context, string, string) (bool, error) {
	return false, errors.ErrUnsupported
}

func TestSpellCacheRestoreBuildsColdWhenTheStoreCannotAnswer(t *testing.T) {
	pub, _ := genKeypair(t)
	backend := &unreachableBackend{err: errors.New(`http.download: Get "https://blob.example.com/x": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`)}
	_, c := openSigned(t, backend, nil, [][]byte{pub})

	got, err := c.restoreSpellCache(t.Context(), testSpellCacheKey(), destRoots(t), time.Now())
	require.NoError(t, err, "a store that cannot answer builds cold rather than failing the run")
	assert.Equal(t, SpellCacheResult{}, got, "nothing restored and nothing refused")
	assert.Equal(t, 1, backend.calls, "an older key would wait out the same timeout against the same store")
}

func TestSpellCacheRestoreStopsWhenTheCallerCancels(t *testing.T) {
	pub, _ := genKeypair(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, c := openSigned(t, &unreachableBackend{err: errors.New("request canceled")}, nil, [][]byte{pub})

	_, err := c.restoreSpellCache(ctx, testSpellCacheKey(), destRoots(t), time.Now())
	require.ErrorIs(t, err, context.Canceled, "a cancelled run stops rather than building cold")
}

// The local tier's archive never carries the caches a box keeps beside it, and an
// archive that names one is refused: those caches travel only as signed bundles.
func TestLocalTierArchiveLeavesSpellCachesOut(t *testing.T) {
	src, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"), WithLocalWrite(true))
	require.NoError(t, err)
	writeTree(t, SpellCacheDir(src.dir, "go", "GOCACHE"), map[string]string{"ab/ab01-a": "action"})
	writeTree(t, src.dir, map[string]string{"logs/run.log": "ran"})

	var buf bytes.Buffer
	require.NoError(t, src.Export(t.Context(), &buf))
	var names []string
	for name := range tarMembers(t, buf.Bytes()) {
		names = append(names, name)
	}
	assert.Contains(t, names, "logs/run.log")
	for _, name := range names {
		assert.False(t, strings.HasPrefix(name, spellCachesDir), "%s is a spell's cache", name)
	}

	dst, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"), WithLocalWrite(true))
	require.NoError(t, err)
	planted := appendMember(t, buf.Bytes(), spellCachesDir+"/go/GOCACHE/ab/ab01-a", "planted")
	err = dst.Import(t.Context(), bytes.NewReader(planted))
	require.ErrorContains(t, err, "restored only from a signed bundle")
	assert.NoFileExists(t, filepath.Join(SpellCacheDir(dst.dir, "go", "GOCACHE"), "ab", "ab01-a"))
}

func tarMembers(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	gzr, err := gzip.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	tr := tar.NewReader(gzr)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		require.NoError(t, err)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		out[hdr.Name] = body
	}
}

// TestTrimmedGoBundleBuildsWarm drives the real Go toolchain: a bundle trimmed to the
// entries one build used must still make that build a pure cache hit elsewhere.
func TestTrimmedGoBundleBuildsWarm(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles with the Go toolchain")
	}
	goBin, err := osexec.LookPath("go")
	if err != nil {
		t.Skip("no go on PATH")
	}
	pub, seed := genKeypair(t)
	work := t.TempDir()
	writeTree(t, filepath.Join(work, "app"), map[string]string{
		"go.mod":  "module app\n\ngo 1.21\n",
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hi\") }\n",
	})
	writeTree(t, filepath.Join(work, "other"), map[string]string{
		"go.mod":  "module other\n\ngo 1.21\n",
		"main.go": "package main\n\nimport \"encoding/json\"\n\nfunc main() { _, _ = json.Marshal(1) }\n",
	})
	gocache := filepath.Join(work, "gocache")
	gomod := filepath.Join(work, "gomod")
	build := func(t *testing.T, cache, dir string) string {
		t.Helper()
		cmd := osexec.CommandContext(t.Context(), goBin, "build", "-x", "-o", os.DevNull, ".")
		cmd.Dir = filepath.Join(work, dir)
		cmd.Env = append(os.Environ(), "GOCACHE="+cache, "GOMODCACHE="+gomod, "GOTOOLCHAIN=local", "GOFLAGS=")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	build(t, gocache, "app")
	build(t, gocache, "other")
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, filepath.WalkDir(gocache, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return os.Chtimes(p, old, old)
	}))
	build(t, gocache, "app")

	key := testSpellCacheKey()
	s, err := newSigner(seed)
	require.NoError(t, err)
	var buf bytes.Buffer
	stats, err := writeSpellCacheBundle(t.Context(), &buf, s, key, testBundleName, goRoots(gocache, gomod), time.Now())
	require.NoError(t, err)
	assert.Positive(t, stats.Skipped, "encoding/json's entries went unused and are left out")

	v, err := newVerifier([][]byte{pub})
	require.NoError(t, err)
	restored := filepath.Join(work, "restored")
	_, err = readSpellCacheBundle(t.Context(), &buf, v, spellCacheWant{key: key, name: testBundleName}, goRoots(restored, gomod), defaultMaxImportBytes, time.Now())
	require.NoError(t, err)
	out := build(t, restored, "app")
	assert.NotContains(t, out, "/compile ", "every package of the used build is a cache hit")
	assert.Contains(t, build(t, restored, "other"), "/compile ", "a left-out entry really is gone")
}
