package interp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	goast "go/ast"
	"go/parser"
	"go/token"
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
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/observability/gen/observabilitytest"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/spell"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/libs/testkit"
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
			project, cwd := testkit.NewWorkspace(t), testkit.NewWorkspace(t)
			project.Magusfile(magusfile)
			if tc.projectHelper != "" {
				project.Write("helper.buzz", tc.projectHelper)
			}
			cwd.Write("helper.buzz", tc.cwdHelper)
			t.Chdir(cwd.Root())

			src, err := Find(project.Root())
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
	outer := testkit.NewWorkspace(t)
	root := outer.Path("ws")
	const module = "export fun touch() > void {}\n"
	above := outer.Write("x.buzz", module)
	outer.Write("ws/inside.buzz", module)

	ctx := types.WithWorkspace(t.Context(), rootWorkspace{root: root})
	oldJoin := strings.ReplaceAll(magusSearchPaths(ctx, root)[0], "?", "../x")
	require.Equal(t, root+string(filepath.Separator)+".."+string(filepath.Separator)+"x.buzz", oldJoin)
	require.Equal(t, above, filepath.Clean(oldJoin), "the old search resolves the planted module")

	load := func(t *testing.T, magusfile string) ([]string, error) {
		t.Helper()
		outer.Write("ws/magusfile.buzz", magusfile)
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
	w := testkit.NewWorkspace(t)
	root := w.Root()
	const magusfile = "import \"magus\";\nexport fun ci(ctx: magus\\Context, args: [str]) > void {}\n" +
		"export fun test(ctx: magus\\Context, args: [str]) > void {}\n"
	w.Magusfile(magusfile)
	granted := t.TempDir()
	probe := filepath.Join(granted, "stat")

	base, err := sandbox.FromConfig(root, "", "", config.SandboxConfig{}, nil)
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

	// TimeCall hands the provider the ctx a target body runs under, so the policy that ctx
	// carries is captured from each RecordBuzzExec.
	var policies []*sandbox.Policy
	rec := observabilitytest.NewMockProvider(t)
	rec.EXPECT().Enabled().Return(true).Maybe()
	rec.EXPECT().RecordBuzzJITRun(mock.Anything).Maybe()
	rec.EXPECT().RecordBuzzExec(mock.Anything, mock.Anything).
		Run(func(ctx context.Context, _ observability.BuzzExec) {
			policies = append(policies, sandbox.PolicyFromContext(ctx))
		}).
		Times(2)
	ctx := observability.WithProvider(types.WithWorkspace(sandbox.WithPolicy(t.Context(), base), ws), rec)
	parent := withDeclaredStep(ctx, root, "ci")
	_, err = load.Targets["test"](parent, nil)
	require.NoError(t, err)
	_, err = load.Targets["ci"](parent, nil)
	require.NoError(t, err)

	require.Len(t, policies, 2)
	require.NoError(t, policies[0].CheckRead(t.Context(), probe), "a composed target lost its own grant")
	assert.Error(t, policies[1].CheckRead(t.Context(), probe), "the parent gained its child's grant")
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
	w, cache := testkit.NewWorkspace(t), t.TempDir()
	root := w.Root()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("MAGUS_CACHE_DIR", "")
	w.Magusfile("export final marker = 1;\n")

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

// A load of approved sources trusts only the bytes its reader returns. A chunk a
// working-tree load stored, under the approved source's key or moved into the approved
// store over the approved chunk, is not one of them.
func TestGuardLoadOfApprovedSourcesIgnoresWorkingTreeChunks(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("MAGUS_CACHE_DIR", filepath.Join(cache, "workspace"))
	magusfile := filepath.Join(root, "magusfile.buzz")
	const approved = "export final approved = 1;\n"
	read := func(string) ([]byte, error) { return []byte(approved), nil }
	load := func(ctx context.Context, src string) ([]string, string) {
		t.Helper()
		before := storedChunks(t, cache)
		require.NoError(t, os.WriteFile(magusfile, []byte(src), 0o644))
		exports := guardLoad(t, ctx, root)
		for path := range storedChunks(t, cache) {
			if !before[path] {
				return exports, path
			}
		}
		t.Fatalf("no chunk stored for %q", src)
		return nil, ""
	}
	_, worktreeChunk := load(t.Context(), approved)
	_, plantedChunk := load(t.Context(), "export final planted = 1;\n")
	planted, err := os.ReadFile(plantedChunk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(worktreeChunk, planted, 0o600))

	exports, approvedChunk := load(WithApprovedSourceReader(t.Context(), read), "export final planted = 1;\n")
	assert.Equal(t, []string{"approved"}, exports)
	assert.NotEqual(t, filepath.Dir(worktreeChunk), filepath.Dir(approvedChunk), "the approved load keeps its own store")

	require.NoError(t, os.WriteFile(approvedChunk, planted, 0o600))
	assert.Equal(t, []string{"approved"}, guardLoad(t, WithApprovedSourceReader(t.Context(), read), root))
}

// A load of approved sources compiles once per content: the next load of the same bytes
// runs the stored chunk, whatever the working tree holds. A reader of any other kind keeps
// nothing.
func TestGuardLoadOfApprovedSourcesCompilesOnce(t *testing.T) {
	w, cache := testkit.NewWorkspace(t), t.TempDir()
	root := w.Root()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("MAGUS_CACHE_DIR", "")
	w.Magusfile("export final edited = 1;\n")
	read := func(string) ([]byte, error) { return []byte("export final approved = 1;\n"), nil }
	compiles := func(ctx context.Context) ([]string, bool) {
		profile := buzz.NewProfile()
		exports := guardLoad(t, buzz.WithProfile(ctx, profile), root)
		return exports, profile.Report() != ""
	}

	exports, compiled := compiles(WithSourceReader(t.Context(), read))
	assert.Equal(t, []any{[]string{"approved"}, true}, []any{exports, compiled})
	assert.Empty(t, storedChunks(t, cache), "a reader that is not the approved one keeps nothing")

	exports, compiled = compiles(WithApprovedSourceReader(t.Context(), read))
	assert.Equal(t, []any{[]string{"approved"}, true}, []any{exports, compiled})
	stored := storedChunks(t, cache)
	require.Len(t, stored, 1)
	for path := range stored {
		rel, err := filepath.Rel(filepath.Join(cache, "magus", "buzz-bytecode-approved"), path)
		require.NoError(t, err)
		assert.True(t, filepath.IsLocal(rel), "the chunk is kept apart from working-tree chunks: %s", path)
	}

	exports, compiled = compiles(WithApprovedSourceReader(t.Context(), read))
	assert.Equal(t, []any{[]string{"approved"}, false}, []any{exports, compiled}, "the second load compiles nothing")
	assert.Equal(t, stored, storedChunks(t, cache))
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

// An executable with no readable build ID has no name to store chunks under, so the load
// compiles every time and leaves nothing behind.
func TestGuardBytecodeStoreIsOffWithoutABuildID(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	old := compilerStamp
	compilerStamp = func() string { return "" }
	t.Cleanup(func() { compilerStamp = old })

	assert.Nil(t, guardBytecodeStore(t.TempDir()))
	assert.NoDirExists(t, filepath.Join(cache, "magus", "buzz-bytecode"))
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

// TestBinaryAnswersCoverEveryForkingMember holds binaryAnswers to std: every magus member
// whose Go implementation reaches runMagus (a nested magus) must fall under an entry, or
// a target calling it keys on nothing that moves when the binary does and replays the old
// binary's answer. A new forking member fails here rather than escaping the key.
//
// The bindings are read structurally: each std.Magus* call sits inside the closure
// registered under a string literal, in a file whose namespace bindingPrefix names.
func TestBinaryAnswersCoverEveryForkingMember(t *testing.T) {
	forks := stdForkingFuncs(t, filepath.Join("..", "..", "std"))
	require.Contains(t, forks, "MagusDescribeRule", "the fork walk must see runMagusJSON")
	require.Contains(t, forks, "MagusCmd", "the fork walk must see runMagusSub")

	bindingPrefix := map[string][]string{
		filepath.Join("bindings", "gen", "magus.go"): nil,
		filepath.Join("bindings", "describe.go"):     {"describe"},
		filepath.Join("bindings", "job.go"):          {"job"},
		filepath.Join("bindings", "magus_trail.go"):  {"trail"},
		filepath.Join("bindings", "magus_vcs.go"):    {"vcs"},
	}
	covered := func(path []string) bool {
		return slices.ContainsFunc(binaryAnswers(), func(entry []string) bool {
			return len(entry) <= len(path) && slices.Equal(entry, path[:len(entry)])
		})
	}
	seen := 0
	for _, file := range bindingFiles(t, "bindings") {
		prefix, known := bindingPrefix[file]
		for member, fn := range stdBindings(t, file) {
			if !known {
				t.Errorf("%s binds std.%s as %q but names no magus namespace here; add it to bindingPrefix", file, fn, member)
				continue
			}
			seen++
			path := append(slices.Clone(prefix), member)
			if forks[fn] {
				assert.True(t, covered(path), "magus\\%s forks a nested magus (std.%s) but binaryAnswers does not cover it",
					strings.Join(path, "."), fn)
			}
		}
	}
	require.Positive(t, seen, "no std.Magus bindings found")
}

// stdForkingFuncs returns the top-level functions of the Go package in dir that reach
// runMagus through calls within the package.
func stdForkingFuncs(t *testing.T, dir string) map[string]bool {
	t.Helper()
	calls := map[string][]string{}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			fd, ok := decl.(*goast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil {
				continue
			}
			goast.Inspect(fd.Body, func(n goast.Node) bool {
				call, ok := n.(*goast.CallExpr)
				if !ok {
					return true
				}
				fun := call.Fun
				switch x := fun.(type) {
				case *goast.IndexExpr:
					fun = x.X
				case *goast.IndexListExpr:
					fun = x.X
				}
				if id, ok := fun.(*goast.Ident); ok {
					calls[fd.Name.Name] = append(calls[fd.Name.Name], id.Name)
				}
				return true
			})
		}
	}
	forks := map[string]bool{"runMagus": true}
	for changed := true; changed; {
		changed = false
		for fn, callees := range calls {
			if !forks[fn] && slices.ContainsFunc(callees, func(c string) bool { return forks[c] }) {
				forks[fn], changed = true, true
			}
		}
	}
	return forks
}

// bindingFiles lists the non-test Go files under dir, relative to the package.
func bindingFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return err
	})
	require.NoError(t, err)
	return out
}

// stdBindings maps each member a binding file registers to the std.Magus* function its
// closure calls: the string literal opening the innermost enclosing registration,
// MapSet("cmd", ...) or bind("rule", ...), names the member.
func stdBindings(t *testing.T, file string) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	out := map[string]string{}
	var stack []*goast.CallExpr
	var walk func(n goast.Node) bool
	walk = func(n goast.Node) bool {
		call, ok := n.(*goast.CallExpr)
		if !ok {
			return true
		}
		if sel, isSel := call.Fun.(*goast.SelectorExpr); isSel {
			if pkg, isIdent := sel.X.(*goast.Ident); isIdent && pkg.Name == "std" && strings.HasPrefix(sel.Sel.Name, "Magus") {
				for i := len(stack) - 1; i >= 0; i-- {
					if !isRegistration(stack[i]) || len(stack[i].Args) == 0 {
						continue
					}
					if lit, isLit := stack[i].Args[0].(*goast.BasicLit); isLit && lit.Kind == token.STRING {
						name, err := strconv.Unquote(lit.Value)
						require.NoError(t, err)
						out[name] = sel.Sel.Name
						break
					}
				}
			}
		}
		stack = append(stack, call)
		for _, arg := range append([]goast.Expr{call.Fun}, call.Args...) {
			goast.Inspect(arg, walk)
		}
		stack = stack[:len(stack)-1]
		return false
	}
	goast.Inspect(f, walk)
	return out
}

func isRegistration(call *goast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *goast.SelectorExpr:
		return fun.Sel.Name == "MapSet"
	case *goast.Ident:
		return fun.Name == "bind"
	}
	return false
}

// An unaliased import binds both its basename and the namespace it declares
// (branch-stack.buzz declares `namespace branchstack`), so a call through either reaches
// the module. A helper on a recursion cycle still answers for a later target that calls it.
func TestBinaryAnswerTargetsFollowsHelpersAndImports(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.buzz"), []byte(`namespace lib;
import "magus";
export fun page() > str { return magus\describe.graphMarkdown(["."]); }
export fun plain() > void {}
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "branch-stack.buzz"), []byte(`namespace branchstack;
import "magus";
export fun render() > void { magus\run(["."], ["generate"]); }
`), 0o644))
	src := `import "magus";
import "./lib" as lib;
import "./branch-stack";
export fun direct(ctx: magus\Context, args: [str]) > void { final r = magus\describe.rule(); }
export fun viaHelper(ctx: magus\Context, args: [str]) > void { helper(); }
fun helper() > void { magus\cmd("doctor", []); }
export fun viaImport(ctx: magus\Context, args: [str]) > void { final p = lib\page(); }
export fun viaNamespace(ctx: magus\Context, args: [str]) > void { branchstack\render(); }
export fun recursive(ctx: magus\Context, args: [str]) > void { loop(); }
fun loop() > void { loop(); spin(); }
fun spin() > void { loop(); }
export fun afterCycle(ctx: magus\Context, args: [str]) > void { spin(); helper(); }
export fun plain(ctx: magus\Context, args: [str]) > void { lib\plain(); magus\log.info("x"); }
`
	got := BinaryAnswerTargets(src, []string{dir}, os.ReadFile)
	assert.Equal(t, map[string]bool{
		"direct": true, "via-helper": true, "via-import": true, "via-namespace": true, "after-cycle": true,
	}, got)
}
