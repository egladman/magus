package cache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

var testGoEnv = map[string]string{
	"GOVERSION": "go1.26.6", "GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v1",
	"GOEXPERIMENT": "jsonv2", "GOFLAGS": "-mod=mod", "CGO_ENABLED": "1",
}

var testGoSums = map[string][]byte{"go.sum": []byte("a v1 h1:x\n"), "libs/x/go.sum": []byte("b v2 h1:y\n")}

func testToolchainKey() ToolchainKey { return GoToolchainKey(testGoEnv, testGoSums) }

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

func sourceRoots(t *testing.T) []ToolchainRoot {
	t.Helper()
	src := t.TempDir()
	writeTree(t, filepath.Join(src, "gocache"), map[string]string{
		"ab/ab01-a":      "action",
		"ab/ab01-d":      "output",
		"README":         "not an entry",
		"trim.txt":       "123",
		"fuzz/x/corpus1": "fuzz",
	})
	writeTree(t, filepath.Join(src, "gomod"), map[string]string{
		"example.com/m@v1.0.0/m.go":                      "package m",
		"cache/download/example.com/m/@v/v1.0.0.mod":     "module example.com/m",
		"cache/download/example.com/m/@v/v1.0.0.ziphash": "h1:abc",
		"cache/download/example.com/m/@v/v1.0.0.zip":     "zip bytes",
		"cache/download/example.com/m/@v/v1.0.0.lock":    "",
		"cache/vcs/0123/HEAD":                            "ref",
	})
	return GoToolchainRoots(filepath.Join(src, "gocache"), filepath.Join(src, "gomod"))
}

func destRoots(t *testing.T) []ToolchainRoot {
	t.Helper()
	dst := t.TempDir()
	return GoToolchainRoots(filepath.Join(dst, "gocache"), filepath.Join(dst, "gomod"))
}

func signedBundle(t *testing.T, seed []byte, key ToolchainKey, roots []ToolchainRoot) []byte {
	t.Helper()
	s, err := newSigner(seed)
	require.NoError(t, err)
	var buf bytes.Buffer
	_, err = writeToolchainBundle(t.Context(), &buf, s, key, "", roots, 0, time.Now())
	require.NoError(t, err)
	return buf.Bytes()
}

func TestToolchainBundleRoundTrip(t *testing.T) {
	pub, seed := genKeypair(t)
	key := testToolchainKey()
	data := signedBundle(t, seed, key, sourceRoots(t))

	dst := destRoots(t)
	now := time.Now()
	v, err := newVerifier([][]byte{pub})
	require.NoError(t, err)
	stats, err := readToolchainBundle(t.Context(), bytes.NewReader(data), v, toolchainWant{key: key}, dst, defaultMaxImportBytes, now)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"ab/ab01-a": "action", "ab/ab01-d": "output"}, readTree(t, dst[0].Dir),
		"only cache shards travel; the staging directory is gone")
	assert.Equal(t, map[string]string{
		"example.com/m@v1.0.0/m.go":                      "package m",
		"cache/download/example.com/m/@v/v1.0.0.mod":     "module example.com/m",
		"cache/download/example.com/m/@v/v1.0.0.ziphash": "h1:abc",
	}, readTree(t, dst[1].Dir), "zips, locks and VCS clones stay out")
	assert.Equal(t, ToolchainStats{Files: 5, Bytes: int64(len("actionoutputpackage mmodule example.com/mh1:abc"))}, stats)

	info, err := os.Stat(filepath.Join(dst[1].Dir, "example.com/m@v1.0.0/m.go"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o444), info.Mode().Perm(), "module files are restored read-only, as Go keeps them")
	info, err = os.Stat(filepath.Join(dst[0].Dir, "ab/ab01-a"))
	require.NoError(t, err)
	assert.WithinDuration(t, now.Add(-restoredAge), info.ModTime(), time.Second,
		"restored entries are backdated so Go re-stamps the ones a run uses")

	again, err := readToolchainBundle(t.Context(), bytes.NewReader(data), v, toolchainWant{key: key}, dst, defaultMaxImportBytes, now)
	require.NoError(t, err)
	assert.Equal(t, ToolchainStats{Skipped: 5}, again, "files already present are left as they are")
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

func TestToolchainBundleRefusals(t *testing.T) {
	pub, seed := genKeypair(t)
	otherPub, otherSeed := genKeypair(t)
	key := testToolchainKey()
	good := signedBundle(t, seed, key, sourceRoots(t))

	otherToolchain := key
	otherToolchain.Toolchain = strings.Repeat("0", 64)

	cases := []struct {
		name    string
		bundle  []byte
		trusted []byte
		want    toolchainWant
		err     string
	}{
		{
			name: "a file swapped for bytes of the same size",
			bundle: retar(t, good, func(name string, body []byte) ([]byte, bool) {
				if name == "gocache/ab/ab01-a" {
					return []byte("ACTION"), true
				}
				return body, true
			}),
			trusted: pub, want: toolchainWant{key: key},
			err: "content does not match the signed index",
		},
		{
			name: "a dropped member",
			bundle: retar(t, good, func(name string, body []byte) ([]byte, bool) {
				return body, name != "gocache/ab/ab01-d"
			}),
			trusted: pub, want: toolchainWant{key: key},
			err: "truncated",
		},
		{
			name:    "a key outside the trust set",
			bundle:  good,
			trusted: otherPub, want: toolchainWant{key: key},
			err: "not in trust set",
		},
		{
			name: "an unsigned bundle",
			bundle: retar(t, good, func(name string, body []byte) ([]byte, bool) {
				return body, name != sigFileName
			}),
			trusted: pub, want: toolchainWant{key: key},
			err: "expected signature.json",
		},
		{
			name:    "another toolchain's bundle",
			bundle:  signedBundle(t, seed, otherToolchain, sourceRoots(t)),
			trusted: pub, want: toolchainWant{key: key},
			err: "is for toolchain",
		},
		{
			name:    "a bundle signed for another remote key",
			bundle:  good,
			trusted: pub, want: toolchainWant{key: key, name: "go-x-y-20260101"},
			err: "signed for key",
		},
		{
			name:    "a signature from a key the reader does not trust over a valid index",
			bundle:  signedBundle(t, otherSeed, key, sourceRoots(t)),
			trusted: pub, want: toolchainWant{key: key},
			err: "not in trust set",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := newVerifier([][]byte{tc.trusted})
			require.NoError(t, err)
			dst := destRoots(t)
			_, err = readToolchainBundle(t.Context(), bytes.NewReader(tc.bundle), v, tc.want, dst, defaultMaxImportBytes, time.Now())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)
			for _, root := range dst {
				assert.Empty(t, readTree(t, root.Dir), "a refused bundle places nothing")
			}
		})
	}

	t.Run("no trust set", func(t *testing.T) {
		_, err := readToolchainBundle(t.Context(), bytes.NewReader(good), nil, toolchainWant{key: key}, destRoots(t), defaultMaxImportBytes, time.Now())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no trust set")
	})

	t.Run("a member the index does not name", func(t *testing.T) {
		v, err := newVerifier([][]byte{pub})
		require.NoError(t, err)
		smuggled := appendMember(t, good, "gocache/cd/cd01-a", "planted")
		dst := destRoots(t)
		_, err = readToolchainBundle(t.Context(), bytes.NewReader(smuggled), v, toolchainWant{key: key}, dst, defaultMaxImportBytes, time.Now())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not in the signed index")
		assert.Empty(t, readTree(t, dst[0].Dir))
	})

	t.Run("writing without a signing key", func(t *testing.T) {
		_, err := writeToolchainBundle(t.Context(), io.Discard, nil, key, "", sourceRoots(t), 0, time.Now())
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

func TestGoToolchainKeyChangesWithEachInput(t *testing.T) {
	base := testToolchainKey()
	for _, v := range goKeyVars {
		env := map[string]string{}
		for k, val := range testGoEnv {
			env[k] = val
		}
		env[v] += "x"
		got := GoToolchainKey(env, testGoSums)
		assert.NotEqual(t, base.Toolchain, got.Toolchain, "%s is part of the toolchain key", v)
		assert.Equal(t, base.Modules, got.Modules, "%s is not part of the module key", v)
	}

	sums := map[string]map[string][]byte{
		"a go.sum's content": {"go.sum": []byte("a v1 h1:z\n"), "libs/x/go.sum": testGoSums["libs/x/go.sum"]},
		"a go.sum's path":    {"go.sum": testGoSums["go.sum"], "libs/y/go.sum": testGoSums["libs/x/go.sum"]},
		"an added go.sum":    {"go.sum": testGoSums["go.sum"], "libs/x/go.sum": testGoSums["libs/x/go.sum"], "tools/go.sum": nil},
		"a removed go.sum":   {"go.sum": testGoSums["go.sum"]},
	}
	for name, s := range sums {
		got := GoToolchainKey(testGoEnv, s)
		assert.NotEqual(t, base.Modules, got.Modules, "%s changes the module key", name)
		assert.Equal(t, base.Toolchain, got.Toolchain, "%s leaves the toolchain key", name)
	}
	assert.Equal(t, base, GoToolchainKey(testGoEnv, testGoSums), "the key is deterministic")

	day := time.Date(2026, 9, 26, 23, 0, 0, 0, time.UTC)
	assert.Equal(t, base.String()+"-20260926", base.remoteKey(day))
	assert.NotEqual(t, base.remoteKey(day), base.remoteKey(day.AddDate(0, 0, 1)), "each day is its own key")
}

func TestToolchainRemoteSaveAndRestore(t *testing.T) {
	remote, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err)
	pub, seed := genKeypair(t)
	trusted := [][]byte{pub}
	_, writer := openSigned(t, remote, seed, trusted)
	key := testToolchainKey()
	now := time.Now()

	saved, err := writer.saveToolchain(t.Context(), key, sourceRoots(t), 0, now)
	require.NoError(t, err)
	assert.Equal(t, key.remoteKey(now), saved.Key)
	assert.Equal(t, 5, saved.Files)
	assert.Positive(t, saved.Transferred)

	again, err := writer.saveToolchain(t.Context(), key, sourceRoots(t), 0, now)
	require.NoError(t, err)
	assert.True(t, again.Present, "the day's first bundle stands")
	assert.Zero(t, again.Files, "a stored key is not rebuilt")

	_, reader := openSigned(t, remote, nil, trusted)
	dst := destRoots(t)
	got, err := reader.restoreToolchain(t.Context(), key, dst, now)
	require.NoError(t, err)
	assert.Equal(t, saved.Key, got.Key)
	assert.True(t, got.Exact)
	assert.Equal(t, 5, got.Files)
	assert.Equal(t, "action", readTree(t, dst[0].Dir)["ab/ab01-a"])

	t.Run("another module set takes the toolchain's newest bundle", func(t *testing.T) {
		moved := key
		moved.Modules = strings.Repeat("1", 64)
		got, err := reader.restoreToolchain(t.Context(), moved, destRoots(t), now)
		require.NoError(t, err)
		assert.Equal(t, saved.Key, got.Key)
		assert.False(t, got.Exact)
	})

	t.Run("another toolchain finds nothing", func(t *testing.T) {
		other := key
		other.Toolchain = strings.Repeat("2", 64)
		got, err := reader.restoreToolchain(t.Context(), other, destRoots(t), now)
		require.NoError(t, err)
		assert.Empty(t, got.Key)
		assert.Empty(t, got.Refused)
	})

	t.Run("a pull request may not save", func(t *testing.T) {
		root := t.TempDir()
		pr, err := Open(t.Context(), filepath.Join(root, ".magus"), WithLocalWrite(true), WithRemoteBackend(remote),
			WithSigningKey(seed), WithTrustedKeys(trusted), WithRemoteWrite(false))
		require.NoError(t, err)
		_, err = pr.saveToolchain(t.Context(), key, sourceRoots(t), 0, now.AddDate(0, 0, 1))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "may not write (remote writes are off)")
	})

	t.Run("a tampered newest bundle is refused and an older verified one is taken", func(t *testing.T) {
		later := now.AddDate(0, 0, 1)
		_, err := writer.saveToolchain(t.Context(), key, sourceRoots(t), 0, later)
		require.NoError(t, err)
		stored := remote.artifactPath(toolchainNamespace, key.remoteKey(later))
		tampered := rewriteTarMember(t, stored, func(name string) bool { return name == "gocache/ab/ab01-d" }, []byte("OUTPUT"))
		require.NoError(t, os.WriteFile(stored, tampered, 0o644))

		dst := destRoots(t)
		got, err := reader.restoreToolchain(t.Context(), key, dst, later)
		require.NoError(t, err)
		require.Len(t, got.Refused, 1)
		assert.Contains(t, got.Refused[0], key.remoteKey(later))
		assert.Equal(t, key.remoteKey(now), got.Key, "the day before's verified bundle")
		assert.Equal(t, "output", readTree(t, dst[0].Dir)["ab/ab01-d"], "no tampered byte is restored")
	})
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

	key := testToolchainKey()
	var buf bytes.Buffer
	stats, err := WriteToolchainBundle(t.Context(), &buf, seed, key, GoToolchainRoots(gocache, gomod), time.Hour)
	require.NoError(t, err)
	assert.Positive(t, stats.Skipped, "encoding/json's entries went unused and are trimmed")

	restored := filepath.Join(work, "restored")
	_, err = ReadToolchainBundle(t.Context(), &buf, [][]byte{pub}, key, GoToolchainRoots(restored, gomod))
	require.NoError(t, err)
	out := build(t, restored, "app")
	assert.NotContains(t, out, "/compile ", "every package of the used build is a cache hit")
	assert.Contains(t, build(t, restored, "other"), "/compile ", "a trimmed entry really is gone")
}
