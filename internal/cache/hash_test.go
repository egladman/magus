package cache

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestHashFileKnownVector pins hashFile to the stdlib SHA256 of a known input.
// magus hashes with crypto/sha256 (not BLAKE3), so the digest of "hello world"
// must match the canonical sha256 hex.
func TestHashFileKnownVector(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.txt")
	require.NoError(t, os.WriteFile(p, []byte("hello world"), 0o644))
	got, err := hashFile(p)
	require.NoError(t, err)
	assert.Equal(t, "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9", got)
}

// TestHashFileStableAcrossRuns verifies the same content yields the same digest
// on repeated calls, and different content yields a different digest.
func TestHashFileStableAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	require.NoError(t, os.WriteFile(a, []byte("content"), 0o644))
	h1, err := hashFile(a)
	require.NoError(t, err)
	h2, err := hashFile(a)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "hash must be stable across runs")

	require.NoError(t, os.WriteFile(a, []byte("different"), 0o644))
	h3, err := hashFile(a)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h3, "changed content must change the hash")
}

// TestHashFileMissing verifies the os.Open error path is wrapped with the path.
func TestHashFileMissing(t *testing.T) {
	_, err := hashFile(filepath.Join(t.TempDir(), "absent"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hashFile")
}

// TestHashFileWithMtimeReusesStoredHash verifies the mtime fast-path: the
// first call hashes and records the fingerprint; a second call with an
// unchanged file returns the stored hash without re-hashing (proved by
// corrupting the file content while keeping the stored fingerprint valid).
func TestHashFileWithMtimeReusesStoredHash(t *testing.T) {
	c := newBareCache(t)
	c.mtimes.load(context.Background()) // hashFileWithMtime.set needs initialised shards
	dir := t.TempDir()
	p := filepath.Join(dir, "src.go")
	require.NoError(t, os.WriteFile(p, []byte("v1"), 0o644))

	h1, err := c.hashFileWithMtime(p)
	require.NoError(t, err)
	require.NotEmpty(t, h1)

	// Overwrite content but restore the exact prior (mtime,size) fingerprint so
	// the store still matches. A correct fast-path returns the stale stored hash
	// rather than re-hashing the new bytes; this proves the store was consulted.
	info, err := os.Stat(p)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, []byte("v2"), 0o644)) // same 2-byte size
	require.NoError(t, os.Chtimes(p, info.ModTime(), info.ModTime()))

	h2, err := c.hashFileWithMtime(p)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "matching fingerprint must return the stored hash")
}

// TestHashFileWithMtimeRehashesOnChange verifies that a genuine size change
// invalidates the fingerprint and triggers a re-hash producing a new digest.
func TestHashFileWithMtimeRehashesOnChange(t *testing.T) {
	c := newBareCache(t)
	c.mtimes.load(context.Background()) // hashFileWithMtime.set needs initialised shards
	p := filepath.Join(t.TempDir(), "src.go")
	require.NoError(t, os.WriteFile(p, []byte("short"), 0o644))
	h1, err := c.hashFileWithMtime(p)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(p, []byte("a much longer body"), 0o644))
	h2, err := c.hashFileWithMtime(p)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "size change must invalidate the fast-path and re-hash")
}

// TestHashFileWithMtimeMissing verifies the os.Stat error path.
func TestHashFileWithMtimeMissing(t *testing.T) {
	c := newBareCache(t)
	_, err := c.hashFileWithMtime(filepath.Join(t.TempDir(), "absent"))
	assert.Error(t, err)
}

// TestExpandSourcesSkipsSymlinks verifies that a symlink matching a source glob
// is not recorded (symlinked sources are skipped to avoid following escapes).
func TestExpandSourcesSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is restricted on Windows")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real.go")
	require.NoError(t, os.WriteFile(real, []byte("package main"), 0o644))
	require.NoError(t, os.Symlink("real.go", filepath.Join(root, "alias.go")))

	out, err := expandSources([]string{"*.go"}, root, nil, nil)
	require.NoError(t, err)

	var rels []string
	for _, ra := range out {
		rels = append(rels, ra.rel)
	}
	assert.Equal(t, []string{"real.go"}, rels, "symlinked source must be skipped")
}

// TestExpandSourcesExcludesOutputs verifies that files under an output glob are
// pruned from the source set (the output tree is never an input).
func TestExpandSourcesExcludesOutputs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dist"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.js"), []byte("k"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dist", "bundle.js"), []byte("b"), 0o644))

	out, err := expandSources([]string{"**/*.js"}, root, []string{"dist/**"}, nil)
	require.NoError(t, err)

	var rels []string
	for _, ra := range out {
		rels = append(rels, ra.rel)
	}
	assert.Equal(t, []string{"keep.js"}, rels, "files under an output glob must be excluded")
}

// TestExpandSourcesPrunesSpellDirs verifies that a directory named by a spell's
// declared IgnoreDirs is pruned from the walk even when it holds files matching a
// source glob - the mechanism that lets a language spell (not the cache) decide its
// ecosystem's non-source dirs. A dir NOT in the spell set (nor the core set) is walked.
func TestExpandSourcesPrunesSpellDirs(t *testing.T) {
	root := t.TempDir()
	// zig-cache stands in for a spell-declared dir the core project.IgnoreDirs does
	// not know about; src is a normal source dir that must survive.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "zig-cache"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "main.zig"), []byte("m"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "zig-cache", "stale.zig"), []byte("s"), 0o644))

	// Without the spell dir declared, both .zig files are hashed.
	out, err := expandSources([]string{"**/*.zig"}, root, nil, nil)
	require.NoError(t, err)
	var rels []string
	for _, ra := range out {
		rels = append(rels, ra.rel)
	}
	assert.Equal(t, []string{"src/main.zig", "zig-cache/stale.zig"}, rels, "no spell dirs: both hashed")

	// With zig-cache declared, only the real source survives.
	out, err = expandSources([]string{"**/*.zig"}, root, nil, []string{"zig-cache"})
	require.NoError(t, err)
	rels = nil
	for _, ra := range out {
		rels = append(rels, ra.rel)
	}
	assert.Equal(t, []string{"src/main.zig"}, rels, "spell-declared dir must be pruned from the walk")
}

// TestIsIgnoreDir pins the prune predicate: the narrow VCS/metadata dot set, the shared
// project.IgnoreDirs defaults, and per-project spell-declared dirs all skip; a normal
// source dir and an unrelated dot-dir (which the broad discovery rule would skip but the
// hash walk deliberately does not, so **/*.md under .github still hashes) do not.
func TestIsIgnoreDir(t *testing.T) {
	spellDirs := []string{"node_modules"}
	assert.True(t, isIgnoreDir(".git", spellDirs), "narrow metadata dot-dir")
	assert.True(t, isIgnoreDir("vendor", spellDirs), "project.IgnoreDirs default")
	assert.True(t, isIgnoreDir("node_modules", spellDirs), "spell-declared dir")
	assert.False(t, isIgnoreDir("internal", spellDirs), "normal source dir is walked")
	assert.False(t, isIgnoreDir(".github", spellDirs), "non-metadata dot-dir is deliberately walked by the hash set")
}

// TestExpandSourcesEmptyGlobs verifies the len(globs) == 0 early return.
func TestExpandSourcesEmptyGlobs(t *testing.T) {
	out, err := expandSources(nil, t.TempDir(), nil, nil)
	require.NoError(t, err)
	assert.Nil(t, out)
}

// TestHashFilesEmpty verifies the len(files) == 0 fast path returns nils.
func TestHashFilesEmpty(t *testing.T) {
	c := newBareCache(t)
	hashes, modes, err := c.hashFiles(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, hashes)
	assert.Nil(t, modes)
}

// TestHashStepIgnoreDirsKeyStability is the byte-stability contract for spell-declared
// ignore dirs, asserted at the cache KEY (not just the file set): pruning a dir that
// holds no matching source leaves the key untouched - the reason node_modules/__pycache__
// never invalidate a Go project - while pruning a dir that DOES hold matching source moves
// the key, proving the prune actually drops those files. A future change that folded
// Step.IgnoreDirs into the key directly, or stopped pruning, would fail one of these.
func TestHashStepIgnoreDirsKeyStability(t *testing.T) {
	c := newBareCache(t)
	ctx := context.Background()

	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	write("src/main.go", "package main")            // real source, always hashed
	write("noise/data.txt", "x")                    // no **/*.go match - pruning is key-neutral
	write("shadow/vendored.go", "package vendored") // matches **/*.go - pruning drops it from the key

	key := func(ignore ...string) string {
		h, err := c.hashStep(ctx, &Step{
			ProjectPath:   ".",
			WorkspaceRoot: root,
			Sources:       []string{"**/*.go"},
			IgnoreDirs:    ignore,
		})
		require.NoError(t, err)
		return h
	}

	base := key()
	assert.Equal(t, base, key("noise"),
		"pruning a dir with no matching source must not change the cache key (byte-stability)")
	assert.NotEqual(t, base, key("shadow"),
		"pruning a dir that holds matching source must change the key (prune is effective)")
}

// TestStepKeyMemoReusesSourceExpansion is SourceMemo's own contract, asserted
// directly rather than through IdentifyRef's end-to-end tests: a repeated
// (WorkspaceRoot, Sources, Outputs, IgnoreDirs) tuple is expanded and hashed
// ONCE, and a second StepKeyMemo call with the same memo reuses that result
// instead of re-walking and re-hashing. Reuse is proved, not just equal
// output - if the second call actually re-walked, it would either find src.go
// gone (WalkDir enumerates nothing under that glob, so the key would drop the
// "src:" line and change) or fail outright, since the file is removed between
// calls. A memo that silently stopped reusing would still produce a first key
// equal to a comparison of two independently-computed identical steps, so an
// assertion that only compared keys would pass with SourceMemo deleted -
// deleting the source file between calls is what makes the second call's
// result trustworthy evidence of reuse rather than a coincidence.
func TestStepKeyMemoReusesSourceExpansion(t *testing.T) {
	c := newBareCache(t)
	ctx := context.Background()

	root := t.TempDir()
	src := filepath.Join(root, "src.go")
	require.NoError(t, os.WriteFile(src, []byte("package main"), 0o644))

	step := &Step{
		ProjectPath:   ".",
		WorkspaceRoot: root,
		Sources:       []string{"**/*.go"},
	}

	memo := NewSourceMemo()
	key1, lines1, err := c.StepKeyMemo(ctx, step, memo)
	require.NoError(t, err, "StepKeyMemo (first call)")
	require.Contains(t, lines1, fmt.Sprintf("src:src.go:%s:0", mustHashFile(t, src)),
		"test setup: the first call must have actually hashed src.go")

	// The tuple this memo entry is keyed on never changes between calls, so a
	// real memo hit does not care that the file backing it is now gone.
	require.NoError(t, os.Remove(src))

	key2, lines2, err := c.StepKeyMemo(ctx, step, memo)
	require.NoError(t, err, "StepKeyMemo (second call, memo hit)")
	assert.Equal(t, key1, key2, "a memo hit must reuse the first call's expansion, not re-walk a tree that no longer has the file")
	assert.Equal(t, lines1, lines2, "a memo hit must reuse the first call's key lines verbatim")

	// Control: without a memo, the same step now hashes to something else,
	// proving the disk state really did change and the equality above is not
	// an accident of an unaffected key.
	keyNoMemo, _, err := c.StepKeyMemo(ctx, step, nil)
	require.NoError(t, err, "StepKeyMemo (no memo, post-removal)")
	assert.NotEqual(t, key1, keyNoMemo, "test setup: removing src.go must change the key when nothing memoizes the expansion")
}

// mustHashFile is a small helper for pinning a "src:" key line's expected
// digest to the file's real content hash, without hardcoding a sha256 literal
// that would need updating if the fixture content ever changes.
func mustHashFile(t *testing.T, path string) string {
	t.Helper()
	h, err := hashFile(path)
	require.NoError(t, err)
	return h
}

// TestStaticDirPrefix covers the metacharacter, no-slash, and no-metacharacter
// branches of staticDirPrefix.
func TestStaticDirPrefix(t *testing.T) {
	cases := []struct {
		glob string
		want string
	}{
		{"dist/**", "dist"},
		{"build/js/*.map", "build/js"},
		{"*.js", ""},       // metacharacter with no preceding slash
		{"exact/path", ""}, // no metacharacter at all
		{"a/b/c/**/*.o", "a/b/c"},
	}
	for _, tc := range cases {
		assert.Equalf(t, tc.want, staticDirPrefix(tc.glob), "staticDirPrefix(%q)", tc.glob)
	}
}

// TestShortHash covers both branches: short input returned as-is, long input
// truncated to 8 hex chars.
func TestShortHash(t *testing.T) {
	assert.Equal(t, "abc", shortHash("abc"), "input <= 8 chars returned unchanged")
	assert.Equal(t, "deadbeef", shortHash("deadbeefcafef00d"), "long input truncated to 8")
}

// TestDerivedOverridesChangeTheKey is the correctness property behind ctx.derive: a
// per-op execution override changes what the tool does, so two otherwise identical
// steps must not share a cache entry. Without it, go["go-test"](ctx.derive({env:
// {"CGO_ENABLED": "0"}})) would replay a result computed with CGO enabled.
//
// Two Steps differing ONLY in Derived, so the assertion cannot pass for an unrelated
// reason (a different target name or source set would move the key by itself).
// TestDeclaredEnvVariablesChangeTheKey is the correctness property behind
// ctx.envVariables, and the complement of the ExecOverrides test below: an override's
// value is written in the magusfile and hashed directly, while a declared variable's
// value is only knowable at run time, so the NAME is declared and the VALUE is read
// when the key is computed. Without that read, a target whose behavior depends on an
// environment variable replays a result computed under a different value.
func TestDeclaredEnvVariablesChangeTheKey(t *testing.T) {
	root := t.TempDir()
	c := &Cache{}
	ctx := context.Background()

	base := Step{ProjectPath: ".", WorkspaceRoot: root}
	declared := base
	declared.EnvAllow = []string{"MAGUS_TEST_TOOLCHAIN"}

	t.Setenv("MAGUS_TEST_TOOLCHAIN", "a")
	keyA, err := c.hashStep(ctx, &declared)
	require.NoError(t, err, "hashStep(declared, value a)")

	// Declaring a variable must not be inert: the key has to move with the value.
	t.Setenv("MAGUS_TEST_TOOLCHAIN", "b")
	keyB, err := c.hashStep(ctx, &declared)
	require.NoError(t, err, "hashStep(declared, value b)")
	assert.NotEqual(t, keyA, keyB,
		"a declared env variable's VALUE must reach the key, or the target replays across a toolchain change")

	// And an undeclared variable must stay out of it, so the key does not drift with
	// every unrelated variable in the caller's environment.
	plainKey, err := c.hashStep(ctx, &base)
	require.NoError(t, err, "hashStep(base)")
	t.Setenv("MAGUS_TEST_TOOLCHAIN", "c")
	plainAgain, err := c.hashStep(ctx, &base)
	require.NoError(t, err, "hashStep(base again)")
	assert.Equal(t, plainKey, plainAgain,
		"an undeclared variable must not move the key of a step that never declared it")
}

func TestDerivedOverridesChangeTheKey(t *testing.T) {
	root := t.TempDir()
	c := &Cache{}
	ctx := context.Background()

	base := Step{ProjectPath: ".", WorkspaceRoot: root}
	withDerive := base
	withDerive.ExecOverrides = []string{"env:CGO_ENABLED=0"}

	plainKey, err := c.hashStep(ctx, &base)
	require.NoError(t, err, "hashStep(base)")
	derivedKey, err := c.hashStep(ctx, &withDerive)
	require.NoError(t, err, "hashStep(derived)")

	assert.NotEqual(t, plainKey, derivedKey,
		"a ctx.derive override must change the cache key, or a derived run replays an undecorated result")

	// And it is the VALUE that matters, not merely the presence of an override.
	other := base
	other.ExecOverrides = []string{"env:CGO_ENABLED=1"}
	otherKey, err := c.hashStep(ctx, &other)
	require.NoError(t, err, "hashStep(other)")
	assert.NotEqual(t, derivedKey, otherKey, "two different derived values must key differently")
}

// buildSyntheticJSWorkspace creates nProjects JS project directories under a
// temp root, each with nFilesEach source files (.ts, .js, package.json,
// pnpm-lock.yaml). Used to measure glob expansion under realistic JS monorepo
// conditions.
func buildSyntheticJSWorkspace(b *testing.B, nProjects, nFilesEach int) string {
	b.Helper()
	root := b.TempDir()
	for i := 0; i < nProjects; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg-%03d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"pkg"}`), 0o644); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: '6.0'\n"), 0o644); err != nil {
			b.Fatal(err)
		}
		for j := 0; j < nFilesEach; j++ {
			ext := "ts"
			if j%5 == 0 {
				ext = "js"
			}
			name := filepath.Join(dir, fmt.Sprintf("src/mod-%04d.%s", j, ext))
			if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(name, []byte("export const x = 1\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	return root
}

// BenchmarkHashFilesBatch measures hashFiles for varying batch sizes around
// the io_uring threshold (currently 32 in hash_iouring_linux.go). On Linux
// with io_uring available, sub-benchmarks ≥32 take the io_uring fast path;
// sub-benchmarks <32 fall through to the goroutine-pool tier. Files are
// freshly touched each iteration so the mtime fast-path never hits.
func BenchmarkHashFilesBatch(b *testing.B) {
	for _, n := range []int{8, 16, 32, 64, 128} {
		n := n
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			files := make([]relAbs, n)
			for i := range files {
				rel := fmt.Sprintf("f-%04d.txt", i)
				abs := filepath.Join(dir, rel)
				buf := make([]byte, 1024)
				for j := range buf {
					buf[j] = byte((i*7 + j) & 0xff)
				}
				if err := os.WriteFile(abs, buf, 0o644); err != nil {
					b.Fatal(err)
				}
				files[i] = relAbs{rel: rel, abs: abs}
			}

			c, err := Open(b.Context(),
				b.TempDir(),
				WithMutable(true),
				WithLogger(slog.New(slog.DiscardHandler)),
			)
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				stamp := time.Now().Add(time.Duration(i+1) * time.Second)
				for _, f := range files {
					_ = os.Chtimes(f.abs, stamp, stamp)
				}
				b.StartTimer()
				if _, _, err := c.hashFiles(ctx, files); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkHashStep isolates cache-key serialization: no sources (so neither the
// glob walk nor file hashing run), but many charms/env/deps/tools so the per-field
// serialization dominates. This is the gate for the fmt.Fprintf → direct-write
// optimization on the key path.
func BenchmarkHashStep(b *testing.B) {
	c, err := Open(b.Context(),
		b.TempDir(),
		WithMutable(true),
		WithLogger(slog.New(slog.DiscardHandler)),
	)
	if err != nil {
		b.Fatal(err)
	}
	deps := make([]string, 200)
	for i := range deps {
		deps[i] = fmt.Sprintf("project/pkg-%03d:deadbeefcafef00d%03d", i, i)
	}
	env := make([]string, 40)
	for i := range env {
		env[i] = fmt.Sprintf("MAGUS_VAR_%02d", i)
	}
	tools := make([]string, 20)
	for i := range tools {
		tools[i] = fmt.Sprintf("tool-%02d:1.%d.0", i, i)
	}
	step := &Step{
		ProjectPath:     "pkg/example/service",
		Target:          "build",
		Charms:          []string{"race", "verbose", "coverage", "write"},
		EnvAllow:        env,
		Deps:            deps,
		ToolVersions:    tools,
		SpellDefVersion: "abc123def456",
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := c.hashStep(ctx, step); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExpandSourcesJSWorkspace measures expandSources on a synthetic
// 50-project JS workspace (each project has ~100 source files). This
// baseline captures the per-glob-walk cost before the single-walk
// optimisation (Opt 2/3) is applied.
func BenchmarkExpandSourcesJSWorkspace(b *testing.B) {
	root := buildSyntheticJSWorkspace(b, 50, 100)
	// Scoped globs matching the javascript spell's declared Sources
	// after joinGlob scoping (workspaceRoot-relative).
	globs := make([]string, 0, 50*8)
	for i := 0; i < 50; i++ {
		proj := fmt.Sprintf("pkg-%03d", i)
		globs = append(
			globs,
			proj+"/**/*.js",
			proj+"/**/*.mjs",
			proj+"/**/*.cjs",
			proj+"/**/*.jsx",
			proj+"/package.json",
			proj+"/.npmrc",
			proj+"/pnpm-lock.yaml",
			proj+"/package-lock.json",
		)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = expandSources(globs, root, nil, nil)
	}
	b.Logf("target: <15 ms/op (measured: ~15 ms after single-walk+compiled-glob opt)")
}

// BenchmarkIsIgnoreDir measures the per-directory prune check the source walk runs
// on every directory entry. It replaced a hardcoded switch with a project.IgnoreDirs +
// spell-declared lookup; this pins that the check stays single-digit nanoseconds - far
// below the per-directory syscall cost of WalkDir - so making the ignore set spell-driven
// does not touch the walk's hot path. The miss case (a normal source dir, the overwhelming
// majority of entries) is the one that must stay cheap.
func BenchmarkIsIgnoreDir(b *testing.B) {
	spellDirs := []string{"node_modules"} // a typical single-spell project's declared set
	cases := []struct {
		name string
		dir  string
	}{
		{"miss", "internal"},      // common: a real source dir, scans both lists and returns false
		{"dot", ".git"},           // narrow dot-set hit, first switch case
		{"core", "vendor"},        // project.IgnoreDirs hit
		{"spell", "node_modules"}, // spell-declared hit
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			var sink bool
			for b.Loop() {
				sink = isIgnoreDir(tc.dir, spellDirs)
			}
			_ = sink
		})
	}
}

// BenchmarkCompiledGlobMatchHot measures the hot-path cost of matching a
// fixed set of paths against pre-compiled glob patterns. On the fast paths
// (extension globs → HasSuffix, exact paths → string compare) the match is
// zero-alloc. The hard alloc gate is enforced by TestCompiledGlobAllocsBudget.
func BenchmarkCompiledGlobMatchHot(b *testing.B) {
	pats := compileGlobs([]string{
		"web/studio/**/*.ts",
		"web/studio/**/*.tsx",
		"web/studio/**/*.js",
		"web/studio/package.json",
		"web/studio/pnpm-lock.yaml",
	})
	paths := []string{
		"web/studio/src/components/Button.tsx",
		"web/studio/src/lib/utils.ts",
		"web/studio/package.json",
		"web/studio/pnpm-lock.yaml",
		"web/api/server.go", // no match expected
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, path := range paths {
			for _, p := range pats {
				_ = p.Match(path)
			}
		}
	}
	b.Logf("target: <200 ns/op (informational)")
}

// FuzzHashStep verifies that hashStep never panics on arbitrary Step
// field values and always returns a non-empty hex string on success.
// The seed corpus covers the common shapes: empty step, single source
// glob, env vars, and upstream dependency paths.
func FuzzHashStep(f *testing.F) {
	f.Add("api", "build", "", "")
	f.Add("web/studio", "test", "*.ts", "GOPATH")
	f.Add(".", "lint", "**/*.go", "HOME")
	f.Add("extensions/drape", "ci", "src/**/*.rs", "CARGO_HOME")

	f.Fuzz(func(t *testing.T, projectPath, target, source, envKey string) {
		s := &Step{
			ProjectPath:   projectPath,
			WorkspaceRoot: t.TempDir(),
			Target:        target,
		}
		if source != "" {
			s.Sources = []string{source}
		}
		if envKey != "" {
			s.EnvAllow = []string{envKey}
		}

		c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}
		h, err := c.hashStep(context.Background(), s)
		if err != nil {
			// Errors are expected when source globs resolve to nothing or
			// the workspace root is invalid — not a bug.
			return
		}
		if len(h) == 0 {
			t.Errorf("hashStep returned empty hash for step %+v", s)
		}
	})
}
