package interp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/spell"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

type rootWorkspace struct {
	types.WorkspaceRepository
	root string
}

func (w rootWorkspace) Root() string { return w.root }

// An overlay entry is the more specific answer, so it outranks the reader; a path neither
// covers still comes from the disk only when no reader was supplied.
func TestReadSourcePrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte("disk"), 0o644))

	got, err := readSource(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, "disk", string(got))

	read := func(string) ([]byte, error) { return []byte("revision"), nil }
	ctx := WithSourceReader(context.Background(), read)
	got, err = readSource(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, "revision", string(got))

	got, err = readSource(WithOverlay(ctx, map[string]string{path: "overlay"}), path)
	require.NoError(t, err)
	assert.Equal(t, "overlay", string(got))
}

// The formula must not change, or every id already on the trail stops naming the bytes it
// was recorded for. These are the ids git gives the same bytes.
func TestContentIDMatchesGit(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "ce013625030ba8dba906f756967f9e9ca394464a", ContentID([]byte("hello\n")))
	assert.Equal(t, "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391", ContentID(nil))
}

// A load's log records what the reader actually returned, so a revision's bytes are
// logged under the revision's id rather than the disk's.
func TestSourceLogRecordsWhatTheLoadRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte("hello\n"), 0o644))

	var log SourceLog
	_, err := readSource(WithSourceLog(context.Background(), &log), path)
	require.NoError(t, err)
	assert.Equal(t, []SourceFile{{Path: path, ContentID: "ce013625030ba8dba906f756967f9e9ca394464a"}}, log.Files())

	var revised SourceLog
	ctx := WithSourceLog(WithSourceReader(context.Background(), func(string) ([]byte, error) { return nil, nil }), &revised)
	_, err = readSource(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, []SourceFile{{Path: path, ContentID: "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"}}, revised.Files())
}

func TestMagusSearchPaths(t *testing.T) {
	t.Parallel()
	j := filepath.Join
	root := j("/", "w")
	project := j(root, "api")
	templatesUnder := func(dir string) []string {
		return []string{
			j(dir, "?.buzz"),
			j(dir, "?", "main.buzz"),
			j(dir, "?", "src", "main.buzz"),
			j(dir, "?", "src", "?.buzz"),
			j(dir, "magusfiles", "?.buzz"),
		}
	}

	t.Run("project then workspace root", func(t *testing.T) {
		ctx := types.WithWorkspace(context.Background(), rootWorkspace{root: root})
		want := append(templatesUnder(project), templatesUnder(root)...)
		assert.Equal(t, want, magusSearchPaths(ctx, project))
	})
	t.Run("project at the root is searched once", func(t *testing.T) {
		ctx := types.WithWorkspace(context.Background(), rootWorkspace{root: root})
		assert.Equal(t, templatesUnder(root), magusSearchPaths(ctx, root))
	})
	t.Run("no workspace searches only the project", func(t *testing.T) {
		assert.Equal(t, templatesUnder(project), magusSearchPaths(context.Background(), project))
	})
}

// A magusfile run via --root from another checkout must load its own modules,
// never the caller's.
func TestMagusfileImportIgnoresCwd(t *testing.T) {
	const magusfile = "import \"helper\";\nhelper\\touch();\n"
	const helper = "export fun touch() > void {}\n"

	cases := []struct {
		name          string
		projectHelper string
		cwdHelper     string
		wantErr       string
	}{
		{
			name:          "project module wins over a broken cwd module",
			projectHelper: helper,
			cwdHelper:     "this is not buzz\n",
		},
		{
			name:      "cwd module is not a fallback",
			cwdHelper: helper,
			wantErr:   `buzz: import "helper": module not found`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project, cwd := t.TempDir(), t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(project, "magusfile.buzz"), []byte(magusfile), 0o644))
			if tc.projectHelper != "" {
				require.NoError(t, os.WriteFile(filepath.Join(project, "helper.buzz"), []byte(tc.projectHelper), 0o644))
			}
			require.NoError(t, os.WriteFile(filepath.Join(cwd, "helper.buzz"), []byte(tc.cwdHelper), 0o644))
			t.Chdir(cwd)

			src, err := Find(project)
			require.NoError(t, err)
			load, err := execBuzzSrc(context.Background(), src, true)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() { _ = load.Session.Close() })
		})
	}
}

// The search joins `../x` onto the root as root/../x.buzz, a file beside the workspace,
// which from a worktree is a sibling checkout. It must be refused and never read.
func TestModuleImportStaysInsideWorkspaceRoot(t *testing.T) {
	outer := t.TempDir()
	root := filepath.Join(outer, "ws")
	require.NoError(t, os.MkdirAll(root, 0o755))
	const module = "export fun touch() > void {}\n"
	above := filepath.Join(outer, "x.buzz")
	require.NoError(t, os.WriteFile(above, []byte(module), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "inside.buzz"), []byte(module), 0o644))

	ctx := types.WithWorkspace(t.Context(), rootWorkspace{root: root})
	oldJoin := strings.ReplaceAll(magusSearchPaths(ctx, root)[0], "?", "../x")
	require.Equal(t, root+string(filepath.Separator)+".."+string(filepath.Separator)+"x.buzz", oldJoin)
	require.Equal(t, above, filepath.Clean(oldJoin), "the old search resolves the planted module")

	load := func(t *testing.T, magusfile string) ([]string, error) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))
		var reads []string
		ctx := WithSourceReader(ctx, func(path string) ([]byte, error) {
			reads = append(reads, path)
			return os.ReadFile(path)
		})
		src, err := Find(root)
		require.NoError(t, err)
		load, err := execBuzzSrc(ctx, src, true)
		if err == nil {
			t.Cleanup(func() { _ = load.Session.Close() })
		}
		return reads, err
	}

	t.Run("a module above the root is refused unread", func(t *testing.T) {
		reads, err := load(t, "import \"../x\";\nx\\touch();\n")
		require.ErrorIs(t, err, types.SpellImportEscapesWorkspace)
		assert.NotContains(t, reads, above)
		assert.NotContains(t, reads, oldJoin)
	})
	t.Run("a module inside the root loads", func(t *testing.T) {
		reads, err := load(t, "import \"inside\";\ninside\\touch();\n")
		require.NoError(t, err)
		assert.Contains(t, reads, filepath.Join(root, "inside.buzz"))
	})
}

// importsWorkspace is a workspace carrying resolved spell imports, the way Magus does.
type importsWorkspace struct {
	rootWorkspace
	imports *remotespell.Imports
}

func (w importsWorkspace) SpellImports() *remotespell.Imports { return w.imports }

// A registry-path import magus.yaml does not declare stops the load before Exec with the
// entry to add. A declared one was resolved when the workspace loaded, so the check
// reads only the declarations; the pull is internal/spell/remote's to test.
func TestCheckRemoteSpellImports(t *testing.T) {
	t.Parallel()
	const lint = "ghcr.io/team/spells/lint"
	root := t.TempDir()
	vendor := filepath.Join(root, "vendor", "lint")
	require.NoError(t, os.MkdirAll(vendor, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(vendor, "spell.buzz"), []byte("export fun mgs_getName() > str { return \"lint\"; }\n"), 0o644))
	im, err := remotespell.LoadImports(t.Context(), root, config.SpellsConfig{
		Imports: map[string]config.SpellImport{lint: {Path: "vendor/lint"}},
	}, remotespell.LoadOptions{})
	require.NoError(t, err)
	declared := types.WithWorkspace(t.Context(), importsWorkspace{rootWorkspace{root: root}, im})

	check := func(ctx context.Context, src string) error {
		return checkRemoteSpellImports(ctx, remoteImportPaths(src))
	}
	require.NoError(t, check(t.Context(), `import "spells/local";`))
	require.NoError(t, check(declared, `import "`+lint+`";`))
	require.NoError(t, check(t.Context(), `// import "ghcr.io/team/spells/fmt";`+"\n"), "a comment imports nothing")

	err = check(t.Context(), `import "`+lint+`";`)
	require.ErrorIs(t, err, types.RemoteSpellUndeclared, "no workspace declares anything")

	err = check(declared, "import \""+lint+"\";\nimport \"ghcr.io/team/spells/fmt\" as fmt;\n")
	require.ErrorIs(t, err, types.RemoteSpellUndeclared)
	require.ErrorContains(t, err, `"ghcr.io/team/spells/fmt"`)
}

func TestMentionsRemoteImport(t *testing.T) {
	t.Parallel()
	assert.True(t, mentionsRemoteImport(`import "ghcr.io/team/spells/lint";`))
	assert.True(t, mentionsRemoteImport("import \"magus\";\nimport \"localhost:5000/team/lint\" as lint;"))
	assert.False(t, mentionsRemoteImport(`import "spells/harness/cursor" as cursor;`))
	assert.False(t, mentionsRemoteImport(`import "magus/spell/go";`))
	assert.False(t, mentionsRemoteImport(`final url = "ghcr.io/team/spells/lint";`))
}

// A module resolver has no error channel, so a failure it reports reaches the load
// that is collecting; with none collecting, the caller is told to log instead.
func TestReportImportError(t *testing.T) {
	t.Parallel()
	assert.False(t, ReportImportError(t.Context(), assert.AnError))

	sink := &importErrors{}
	ctx := context.WithValue(t.Context(), importErrorsKey{}, sink)
	assert.True(t, ReportImportError(ctx, assert.AnError))
	require.ErrorIs(t, sink.take(), assert.AnError)
	require.NoError(t, sink.take(), "take drains the sink")
}

// stepProbe records the policy a target body's ctx carries; TimeCall hands it the
// ctx the body runs under.
type stepProbe struct {
	observability.Provider
	policies []*sandbox.Policy
}

func (p *stepProbe) Enabled() bool { return true }

func (p *stepProbe) RecordBuzzExec(ctx context.Context, _ float64, _, _ string) {
	p.policies = append(p.policies, sandbox.PolicyFromContext(ctx))
}

func (p *stepProbe) RecordBuzzJITRun(context.Context) {}

// A ctx.needs child never passes through runTarget, so its body must scope its own
// step: here ci is the caller, as when ci composes test, and only test declares the
// grant, as the root magusfile's test declares /proc.
func TestComposedTargetRunsUnderItsOwnDeclaration(t *testing.T) {
	prev := buzzHostBindingsFn
	buzzHostBindingsFn = func(_ context.Context, sess *buzz.Session, _ map[string]vm.Callable, _ map[string]vm.Value, _ bool) {
		sess.SetNativeModule("magus", vm.NewMap())
		spell.DeclareMagusTypes(sess, nil)
	}
	t.Cleanup(func() { buzzHostBindingsFn = prev })
	root := t.TempDir()
	const magusfile = "import \"magus\";\nexport fun ci(ctx: magus\\Context, args: [str]) > void {}\n" +
		"export fun test(ctx: magus\\Context, args: [str]) > void {}\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))
	granted := t.TempDir()
	probe := filepath.Join(granted, "stat")

	base, err := sandbox.FromConfig(root, "", config.SandboxConfig{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(base.TempDir) })
	require.Error(t, base.CheckRead(t.Context(), probe), "the grant must be the test's alone to prove")

	ws := &ceilingWorkspace{projects: []*types.Project{{
		Dir: root,
		TargetPolicies: map[string]types.Target{
			"ci":   {},
			"test": {Sandbox: &spells.Sandbox{Allow: []spells.SandboxAllow{{Path: granted, Mode: spells.SandboxAccessRO}}}},
		},
	}}}
	src, err := Find(root)
	require.NoError(t, err)
	load, err := execBuzzSrc(t.Context(), src, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = load.Session.Close() })

	rec := &stepProbe{}
	ctx := observability.WithProvider(types.WithWorkspace(sandbox.WithPolicy(t.Context(), base), ws), rec)
	parent := withDeclaredStep(ctx, root, "ci")
	_, err = load.Targets["test"](parent, nil)
	require.NoError(t, err)
	_, err = load.Targets["ci"](parent, nil)
	require.NoError(t, err)

	require.Len(t, rec.policies, 2)
	require.NoError(t, rec.policies[0].CheckRead(t.Context(), probe), "a composed target lost its own grant")
	assert.Error(t, rec.policies[1].CheckRead(t.Context(), probe), "the parent gained its child's grant")
	assert.Error(t, sandbox.PolicyFromContext(parent).CheckRead(t.Context(), probe), "the child's step leaked into its caller's ctx")
}

// storedChunks lists every file under dir, which holds nothing but the stores, less
// the magusfile facts kept beside the chunks.
func storedChunks(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && !strings.HasPrefix(d.Name(), "facts-") {
			out[path] = true
		}
		return err
	}))
	return out
}

// guardLoad loads root's magusfile as a guard hook does and returns what it exported.
func guardLoad(t *testing.T, ctx context.Context, root string) []string {
	t.Helper()
	src, err := Find(root)
	require.NoError(t, err)
	load, err := execBuzzSrc(WithGuardRules(ctx), src, true)
	require.NoError(t, err)
	t.Cleanup(func() { _ = load.Session.Close() })
	return slices.Sorted(maps.Keys(load.Session.Exports()))
}

// A guard load of the working tree keeps its chunks in the user's cache, never
// the workspace, where the agents it judges can write.
func TestGuardBytecodeStoreIsOutsideTheWorkspace(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("MAGUS_CACHE_DIR", "")
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("export final marker = 1;\n"), 0o644))

	assert.Equal(t, []string{"marker"}, guardLoad(t, t.Context(), root))
	assert.Equal(t, map[string]bool{filepath.Join(root, "magusfile.buzz"): true}, storedChunks(t, root),
		"nothing is written under the workspace")
	chunks := storedChunks(t, filepath.Join(cache, "magus", "buzz-bytecode"))
	require.Len(t, chunks, 1)
	for path := range chunks {
		rel, err := filepath.Rel(filepath.Join(cache, "magus", "buzz-bytecode"), path)
		require.NoError(t, err)
		sum := sha256.Sum256([]byte(root))
		assert.Equal(t, []string{compilerStamp(), hex.EncodeToString(sum[:])}, strings.Split(filepath.Dir(rel), string(filepath.Separator)))
		facts, err := filepath.Glob(filepath.Join(filepath.Dir(path), "facts-*"))
		require.NoError(t, err)
		assert.Len(t, facts, 1, "the magusfile's facts sit beside its chunk")
	}
}

// A load of approved sources trusts only the bytes its reader returns. A chunk
// stored under the approved source's key, whoever wrote it, is not one of them.
func TestGuardLoadOfApprovedSourcesIgnoresTheStore(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("MAGUS_CACHE_DIR", filepath.Join(cache, "workspace"))
	magusfile := filepath.Join(root, "magusfile.buzz")
	const approved = "export final approved = 1;\n"
	write := func(src string) string {
		t.Helper()
		before := storedChunks(t, cache)
		require.NoError(t, os.WriteFile(magusfile, []byte(src), 0o644))
		guardLoad(t, t.Context(), root)
		for path := range storedChunks(t, cache) {
			if !before[path] {
				return path
			}
		}
		t.Fatalf("no chunk stored for %q", src)
		return ""
	}
	approvedChunk := write(approved)
	planted, err := os.ReadFile(write("export final planted = 1;\n"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(approvedChunk, planted, 0o600))

	read := func(string) ([]byte, error) { return []byte(approved), nil }
	assert.Equal(t, []string{"approved"}, guardLoad(t, WithSourceReader(t.Context(), read), root))
}

// A new build's directory replaces the ones no build has used for a day.
func TestGuardBytecodeStorePrunesStaleStamps(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stamps := filepath.Join(cache, "magus", "buzz-bytecode")
	old, recent := filepath.Join(stamps, "old"), filepath.Join(stamps, "recent")
	for _, dir := range []string{old, recent} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "root"), 0o700))
	}
	long := time.Now().Add(-2 * staleStampAge)
	require.NoError(t, os.Chtimes(old, long, long))

	require.NotNil(t, guardBytecodeStore(t.TempDir()))
	assert.NoDirExists(t, old)
	assert.DirExists(t, recent)
	assert.DirExists(t, filepath.Join(stamps, compilerStamp()))
}

func TestCompilerStampIsStable(t *testing.T) {
	stamp := compilerStamp()
	assert.Len(t, stamp, 16)
	assert.Equal(t, stamp, compilerStamp())
}

// CI builds a fresh binary on every run: same bytes, new file, new mtime. Its chunks
// are the ones the last run compiled.
func TestCompilerStampIsTheBuildsContentNotItsFile(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	body, err := os.ReadFile(exe)
	require.NoError(t, err)
	fresh := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.WriteFile(fresh, body, 0o755))
	long := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(fresh, long, long))

	stamp := readCompilerStamp(exe)
	require.NotEmpty(t, stamp)
	assert.Equal(t, stamp, readCompilerStamp(fresh))

	id := goBuildID(exe)
	require.NotEmpty(t, id, "the test binary carries a Go build ID")
	replacement := []byte(id)
	replacement[0] ^= 1
	other := bytes.ReplaceAll(body, []byte(id), replacement)
	require.False(t, bytes.Equal(body, other), "the test must change the stored build ID")
	rebuilt := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.WriteFile(rebuilt, other, 0o755))
	assert.Equal(t, string(replacement), goBuildID(rebuilt))
	assert.NotEqual(t, stamp, readCompilerStamp(rebuilt), "another build is another compiler")
}

// A chunk is code the next load runs. One that does not verify is compiled over,
// whoever wrote it: here, a valid chunk of other source moved to this source's key.
func TestGuardLoadRefusesATamperedChunk(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("MAGUS_CACHE_DIR", filepath.Join(cache, "workspace"))
	magusfile := filepath.Join(root, "magusfile.buzz")
	load := func(src string) string {
		t.Helper()
		before := storedChunks(t, cache)
		require.NoError(t, os.WriteFile(magusfile, []byte(src), 0o644))
		guardLoad(t, t.Context(), root)
		for path := range storedChunks(t, cache) {
			if !before[path] {
				return path
			}
		}
		t.Fatalf("no chunk stored for %q", src)
		return ""
	}
	const marker = "export final marker = 1;\n"
	markerChunk := load(marker)
	planted, err := os.ReadFile(load("export final planted = 1;\n"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(markerChunk, planted, 0o600))

	require.NoError(t, os.WriteFile(magusfile, []byte(marker), 0o644))
	assert.Equal(t, []string{"marker"}, guardLoad(t, t.Context(), root))
	recompiled, err := os.ReadFile(markerChunk)
	require.NoError(t, err)
	assert.NotEqual(t, planted, recompiled, "the refused chunk is replaced by the compile")
	assert.Equal(t, []string{"marker"}, guardLoad(t, t.Context(), root))
}

// A guard hook reads a stored copy of what the checks before Exec learn from the
// magusfile's syntax, and the copy says what a parse says.
func TestRuntimeStoredMagusfileFactsMatchParsed(t *testing.T) {
	t.Parallel()
	const code = `import "magus";
import "magus/spell/go";
import "lib/x" as helper;
import "fs";
import "ghcr.io/team/spells/lint" as lint;

export fun build(ctx: magus\Context, args: [str]) > void {
    magus.needs(build);
}

export fun plain(args: [str]) > void {}
`
	want := magusfileFacts{
		Imports:       map[string]string{"magus": "magus", "go": "magus/spell/go", "helper": "lib/x", "fs": "fs", "lint": "ghcr.io/team/spells/lint"},
		CtxForm:       []string{"build"},
		SpellHandles:  []string{"go"},
		RemoteImports: []string{"ghcr.io/team/spells/lint"},
		RemovedCall:   "magus.needs",
		Replacement:   "call ctx.needs(<target>)",
	}
	assert.Equal(t, want, loadMagusfileFacts(nil, code))

	store := buzz.NewDiskBytecodeStore(t.TempDir())
	assert.Equal(t, want, loadMagusfileFacts(store, code), "computed on a miss")
	assert.Equal(t, want, loadMagusfileFacts(store, code), "read back on a hit")
}

// A stored copy that does not decode is recomputed, never trusted as empty.
func TestRuntimeMagusfileFactsIgnoreAnUnreadableCopy(t *testing.T) {
	t.Parallel()
	const code = `import "magus/spell/go";` + "\n"
	store := buzz.NewDiskBytecodeStore(t.TempDir())
	sum := sha256.Sum256([]byte(code))
	require.NoError(t, store.Store("facts-"+hex.EncodeToString(sum[:]), []byte("{not json")))
	assert.Equal(t, []string{"go"}, loadMagusfileFacts(store, code).SpellHandles)
}

// BenchmarkMagusfileFacts is the guard hook's pre-Exec cost for this repo's root
// magusfile. reparse varies the source per iteration, as a fresh hook process starts
// with an empty parse cache.
func BenchmarkMagusfileFacts(b *testing.B) {
	data, err := os.ReadFile("../../magusfile.buzz")
	require.NoError(b, err)
	code := string(data)
	b.Run("reparse", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			i++
			loadMagusfileFacts(nil, code+"\n// "+strconv.Itoa(i)+"\n")
		}
	})
	b.Run("stored", func(b *testing.B) {
		b.ReportAllocs()
		store := buzz.NewDiskBytecodeStore(b.TempDir())
		loadMagusfileFacts(store, code)
		for b.Loop() {
			loadMagusfileFacts(store, code)
		}
	})
}
