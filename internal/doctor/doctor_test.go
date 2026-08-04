package doctor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckLanguageCoverage(t *testing.T) {
	r := &runner{}

	t.Run("all have spell", func(t *testing.T) {
		got := r.checkLanguageCoverage([]*types.Project{{Spell: "go"}, {Spell: "rust"}})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("some missing", func(t *testing.T) {
		got := r.checkLanguageCoverage([]*types.Project{{Spell: ""}, {Spell: "go"}})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
	t.Run("all missing", func(t *testing.T) {
		got := r.checkLanguageCoverage([]*types.Project{{Spell: ""}, {Spell: ""}})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
	t.Run("empty list", func(t *testing.T) {
		got := r.checkLanguageCoverage([]*types.Project{})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
}

func TestCheckCITarget(t *testing.T) {
	// projectWith writes name→body magusfile(s) into a fresh dir and returns it.
	projectWith := func(files map[string]string) *types.Project {
		dir := t.TempDir()
		for name, body := range files {
			p := filepath.Join(dir, name)
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			writeFile(t, p, body)
		}
		return &types.Project{Dir: dir}
	}

	t.Run("no projects skipped", func(t *testing.T) {
		got := (&runner{}).checkCITarget(nil)
		assert.Equal(t, StatusSkip, got.Status, got.Message)
	})
	t.Run("ci declared", func(t *testing.T) {
		got := (&runner{}).checkCITarget([]*types.Project{
			projectWith(map[string]string{"magusfile.buzz": "export fun ci(ctx: magus\\Context, _a: [str]) > void {}\n"}),
		})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("ci declared (buzz, any casing)", func(t *testing.T) {
		got := (&runner{}).checkCITarget([]*types.Project{
			projectWith(map[string]string{"magusfile.buzz": "export fun CI(ctx: magus\\Context, _a: [str]) > void {}\n"}),
		})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("ci declared in one of several projects", func(t *testing.T) {
		got := (&runner{}).checkCITarget([]*types.Project{
			projectWith(map[string]string{"magusfile.buzz": "export fun build(ctx: magus\\Context, _a: [str]) > void {}\n"}),
			projectWith(map[string]string{"magusfile.buzz": "export fun ci(ctx: magus\\Context, _a: [str]) > void {}\n"}),
		})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("no ci anywhere fails", func(t *testing.T) {
		got := (&runner{}).checkCITarget([]*types.Project{
			projectWith(map[string]string{"magusfile.buzz": "export fun build(ctx: magus\\Context, _a: [str]) > void {}\n"}),
		})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
	t.Run("cipher is not ci", func(t *testing.T) {
		got := (&runner{}).checkCITarget([]*types.Project{
			projectWith(map[string]string{"magusfile.buzz": "export fun cipher(ctx: magus\\Context, _a: [str]) > void {}\n"}),
		})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
}

// TestCheckCITarget_FailDetails pins that the failure points the user at how to
// define ci and references the MGS1001 doc.
func TestCheckCITarget_FailDetails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "magusfile.buzz"), "export fun build(ctx: magus\\Context, _a: [str]) > void {}\n")
	got := (&runner{}).checkCITarget([]*types.Project{{Dir: dir}})
	require.Equal(t, StatusFail, got.Status)
	joined := strings.Join(got.Details, "\n")
	assert.Contains(t, joined, "ctx.needs", "details should show how to define ci")
	assert.Contains(t, joined, string(types.NoCITarget), "details should reference the doc")
}

func TestCheckSpellDocs(t *testing.T) {
	// A local Buzz spell with one documented and one undocumented handler target.
	localMissing := spells.NewSpell("local",
		spells.WithTargets("build", "test"),
		spells.WithTargetDocs(map[string]string{"build": "build compiles."}),
		spells.WithDocRequiredTargets("build", "test"),
	)
	localComplete := spells.NewSpell("local",
		spells.WithTargets("build", "test"),
		spells.WithTargetDocs(map[string]string{"build": "build compiles.", "test": "test runs the suite."}),
		spells.WithDocRequiredTargets("build", "test"),
	)
	// A record-style target ("deploy" not in the doc-required set) is exempt even
	// when undocumented, alongside a documented handler target.
	recordStyle := spells.NewSpell("local",
		spells.WithTargets("build", "deploy"),
		spells.WithTargetDocs(map[string]string{"build": "build compiles."}),
		spells.WithDocRequiredTargets("build"),
	)
	// A spell that opts in nothing (built-in / Teal) is exempt even with no docs.
	exempt := spells.NewSpell("builtin", spells.WithTargets("build", "test"))

	r := &runner{}

	t.Run("no spells", func(t *testing.T) {
		got := r.checkSpellDocs(nil)
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("exempt spell with no docs", func(t *testing.T) {
		got := r.checkSpellDocs([]*spells.Spell{exempt})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("local spell fully documented", func(t *testing.T) {
		got := r.checkSpellDocs([]*spells.Spell{localComplete})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("local spell missing a doc", func(t *testing.T) {
		got := r.checkSpellDocs([]*spells.Spell{localMissing})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
	t.Run("record-style target exempt", func(t *testing.T) {
		got := r.checkSpellDocs([]*spells.Spell{recordStyle})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("exempt does not rescue local", func(t *testing.T) {
		got := r.checkSpellDocs([]*spells.Spell{exempt, localMissing})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
}

// TestCheckSpellDocs_Details pins that the failure lists the exact missing
// spell:target pairs, which is what tells the user what to document.
func TestCheckSpellDocs_Details(t *testing.T) {
	s := spells.NewSpell("local",
		spells.WithTargets("build", "lint", "test"),
		spells.WithTargetDocs(map[string]string{"build": "build compiles."}),
		spells.WithDocRequiredTargets("build", "lint", "test"),
	)
	got := (&runner{}).checkSpellDocs([]*spells.Spell{s})
	require.Equal(t, StatusFail, got.Status)
	assert.Equal(t, []string{"local:lint", "local:test"}, got.Details)
}

func TestProbeCoverageFindings(t *testing.T) {
	t.Run("bin covered by the primary probe", func(t *testing.T) {
		got := probeCoverageFindings(spells.Descriptor{
			Name:       "go",
			VersionCmd: []string{"go", "version"},
			Ops:        map[string]spells.Op{"go-build": {Command: spells.Command{Bin: "go", Args: []string{"build"}}}},
		})
		assert.Empty(t, got)
	})
	t.Run("bin covered by a named probe", func(t *testing.T) {
		got := probeCoverageFindings(spells.Descriptor{
			Name:        "go",
			VersionCmds: map[string][]string{"golangci-lint": {"golangci-lint", "--version"}},
			Ops:         map[string]spells.Op{"golangci-lint": {Command: spells.Command{Bin: "golangci-lint", Args: []string{"run"}}}},
		})
		assert.Empty(t, got)
	})
	t.Run("bin covered by a declared opt-out", func(t *testing.T) {
		got := probeCoverageFindings(spells.Descriptor{
			Name:         "go",
			VersionCmd:   []string{"go", "version"},
			UnprobedBins: map[string]string{"gofmt": "ships with the go toolchain"},
			Ops:          map[string]spells.Op{"go-fmt": {Command: spells.Command{Bin: "gofmt", Args: []string{"-l", "."}}}},
		})
		assert.Empty(t, got)
	})
	t.Run("sh wrapper is skipped", func(t *testing.T) {
		got := probeCoverageFindings(spells.Descriptor{
			Name: "buzz",
			Ops:  map[string]spells.Op{"buzz-check": {Command: spells.Command{Bin: "sh", Args: []string{"-c", "buzz --check"}}}},
		})
		assert.Empty(t, got)
	})
	t.Run("package-manager bins are skipped for a pm spell", func(t *testing.T) {
		got := probeCoverageFindings(spells.Descriptor{
			Name:              "typescript",
			PackageManagerBin: "pnpm",
			Ops: map[string]spells.Op{
				"eslint":    {Command: spells.Command{Bin: "pnpm", Args: []string{"exec", "eslint", "."}}},
				"node-test": {Command: spells.Command{Bin: "node", Args: []string{"--test"}}},
			},
		})
		assert.Empty(t, got)
	})
	t.Run("uncovered bin is a finding naming spell, op, bin, and both fixes", func(t *testing.T) {
		got := probeCoverageFindings(spells.Descriptor{
			Name:       "go",
			VersionCmd: []string{"go", "version"},
			Ops:        map[string]spells.Op{"go-fmt": {Command: spells.Command{Bin: "gofmt", Args: []string{"-l", "."}}}},
		})
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "spell go op go-fmt")
		assert.Contains(t, got[0], `bin "gofmt"`)
		assert.Contains(t, got[0], "mgs_getVersionCommands")
		assert.Contains(t, got[0], "mgs_listUnprobedBins")
	})
}

// TestBuiltinsProbeCoverage is the rail itself: every built-in ships with each op
// bin either probed or opted out with a reason. A failure here means a built-in
// gained an op whose bin no cache key can see; fix the SPELL (add the probe or
// the declared opt-out), never this test.
func TestBuiltinsProbeCoverage(t *testing.T) {
	for name, spec := range spellruntime.Builtins() {
		assert.Emptyf(t, probeCoverageFindings(spec), "built-in %q has unprobed op bins", name)
	}
}

func TestCheckSpellProbeCoverage(t *testing.T) {
	r := &runner{}

	t.Run("no projects is clean", func(t *testing.T) {
		got := r.checkSpellProbeCoverage(nil)
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("workspace-local spell is out of scope", func(t *testing.T) {
		got := r.checkSpellProbeCoverage([]*types.Project{{Spell: "my-local-spell"}})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("all bound built-ins are clean", func(t *testing.T) {
		// The zero-findings assertion over the real registry, reached through the
		// check itself: bind every built-in and expect silence.
		var projects []*types.Project
		for name := range spellruntime.Builtins() {
			projects = append(projects, &types.Project{Spell: name})
		}
		got := r.checkSpellProbeCoverage(projects)
		assert.Equal(t, StatusOK, got.Status, strings.Join(got.Details, "\n"))
	})
}

func TestCheckTargetNameConventions(t *testing.T) {
	// run writes files into a fresh project dir and returns the check result.
	run := func(files map[string]string) Check {
		root := t.TempDir()
		for name, body := range files {
			path := filepath.Join(root, name)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		}
		r := &runner{root: root}
		return r.checkTargetNameConventions([]*types.Project{{Path: ".", Dir: root}})
	}

	t.Run("consistent snake_case", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun go_build(ctx: magus\\Context, _a: [str]) > void {}\nexport fun go_test(ctx: magus\\Context, _a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("neutral names only", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun build(ctx: magus\\Context, _a: [str]) > void {}\nexport fun test(ctx: magus\\Context, _a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("snake and camel mixed", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun go_build(ctx: magus\\Context, _a: [str]) > void {}\nexport fun goTest(ctx: magus\\Context, _a: [str]) > void {}\n"})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
	t.Run("mixed across magusfiles dir", func(t *testing.T) {
		got := run(map[string]string{
			"magusfiles/a.buzz": "export fun go_build(ctx: magus\\Context, _a: [str]) > void {}\n",
			"magusfiles/b.buzz": "export fun GoTest(ctx: magus\\Context, _a: [str]) > void {}\n",
		})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
}

func TestCheckBespokePhaseFragmentTargets(t *testing.T) {
	// run writes files into a fresh project dir and returns the check result.
	run := func(files map[string]string) Check {
		root := t.TempDir()
		for name, body := range files {
			path := filepath.Join(root, name)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		}
		r := &runner{root: root}
		return r.checkBespokePhaseFragmentTargets([]*types.Project{{Path: ".", Dir: root}})
	}

	t.Run("canonical names only", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun build(ctx: magus\\Context, _a: [str]) > void {}\nexport fun lint(ctx: magus\\Context, _a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("typecheck flagged", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun typecheck(ctx: magus\\Context, _a: [str]) > void {}\n"})
		require.Equal(t, StatusFail, got.Status, got.Message)
		assert.Contains(t, got.Details[0], "typecheck")
	})
	t.Run("camelCase typeCheck normalizes to type-check and is flagged", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun typeCheck(ctx: magus\\Context, _a: [str]) > void {}\n"})
		require.Equal(t, StatusFail, got.Status, got.Message)
	})
	t.Run("vet audit style prettify all flagged", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun vet(ctx: magus\\Context, _a: [str]) > void {}\n" +
			"export fun audit(ctx: magus\\Context, _a: [str]) > void {}\n" +
			"export fun style(ctx: magus\\Context, _a: [str]) > void {}\n" +
			"export fun prettify(ctx: magus\\Context, _a: [str]) > void {}\n"})
		require.Equal(t, StatusFail, got.Status, got.Message)
		assert.Len(t, got.Details, 4)
	})
	t.Run("security is canonical, not flagged", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun security(ctx: magus\\Context, _a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
}

func TestCheckUnreachedFootprintDecls(t *testing.T) {
	run := func(magusfile string) Check {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))
		r := &runner{root: root}
		return r.checkUnreachedFootprintDecls([]*types.Project{{Path: ".", Dir: root}})
	}

	t.Run("reachable declaration is clean", func(t *testing.T) {
		got := run("export fun build(ctx: magus\\Context, _a: [str]) > void { ctx.readsFiles(\"src/**\"); }\n")
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("orphan in uncalled helper is flagged", func(t *testing.T) {
		got := run("export fun build(ctx: magus\\Context, _a: [str]) > void {}\nfun dead() > void { ctx.writesFiles(\"dist/**\"); }\n")
		require.Equal(t, StatusFail, got.Status, got.Message)
		assert.Contains(t, got.Details[0], "ctx.writesFiles")
	})
}

func TestCheckRedundantFootprintGlobs(t *testing.T) {
	r := &runner{root: t.TempDir()}
	t.Run("no redundancy is clean", func(t *testing.T) {
		p := &types.Project{Path: ".", Sources: []string{"**/*.go"},
			TargetInputs: map[string][]types.InputRef{"build": {{Project: ".", Glob: "src/**"}}}}
		got := r.checkRedundantFootprintGlobs([]*types.Project{p})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("explicit input duplicating a project source is clean", func(t *testing.T) {
		p := &types.Project{Path: ".", Sources: []string{"src/**"},
			TargetInputs: map[string][]types.InputRef{"build": {{Project: ".", Glob: "src/**"}}}}
		got := r.checkRedundantFootprintGlobs([]*types.Project{p})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("cross-project input is never flagged redundant", func(t *testing.T) {
		// A cross input's Rel is relative to the OTHER project, so it must not be
		// compared against this project's sources even when the strings coincide.
		p := &types.Project{Path: "consumer", Sources: []string{"go.mod"},
			TargetInputs: map[string][]types.InputRef{"build": {{Project: "lib", Glob: "go.mod"}}}}
		got := r.checkRedundantFootprintGlobs([]*types.Project{p})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
}

func TestCheckMagusfileSyntax(t *testing.T) {
	// run writes files into a fresh project dir and returns the check result.
	run := func(files map[string]string) Check {
		root := t.TempDir()
		for name, body := range files {
			path := filepath.Join(root, name)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		}
		r := &runner{root: root}
		return r.checkMagusfileSyntax([]*types.Project{{Path: ".", Dir: root}})
	}

	t.Run("clean magusfile", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun ci(ctx: magus\\Context, _a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})

	t.Run("embedding constructs are allowed", func(t *testing.T) {
		// Top-level host calls and statements are embedding-only constructs that
		// upstream-strict parsing rejects; magusfiles parse in embedded mode.
		got := run(map[string]string{"magusfile.buzz": "magus.info(\"hi\");\nexport fun ci(ctx: magus\\Context, _a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})

	t.Run("syntax error fails", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun ci(ctx: magus\\Context, _a: [str]) > void {\n"})
		assert.Equal(t, StatusFail, got.Status, got.Message)
		assert.NotEmpty(t, got.Details, "expected the offending file in details")
	})

	t.Run("all magusfiles reported, not just the first", func(t *testing.T) {
		got := run(map[string]string{
			"magusfiles/a.buzz": "export fun a(ctx: magus\\Context, _a: [str]) > void {\n", // broken
			"magusfiles/b.buzz": "export fun b(ctx: magus\\Context, _a: [str]) > void {\n", // broken
		})
		require.Equal(t, StatusFail, got.Status, got.Message)
		assert.Len(t, got.Details, 2, "both broken magusfiles should be reported in one pass")
	})

	t.Run("no projects ok", func(t *testing.T) {
		got := (&runner{}).checkMagusfileSyntax(nil)
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
}

func TestCheckCharmTargetCollision(t *testing.T) {
	// run writes files into a fresh project dir and returns the check result.
	run := func(files map[string]string) Check {
		root := t.TempDir()
		for name, body := range files {
			require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
		}
		r := &runner{root: root}
		return r.checkCharmTargetCollision([]*types.Project{{Path: ".", Dir: root}})
	}

	t.Run("no charms, no collision", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun build(ctx: magus\\Context, _a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("charm distinct from every target", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun build(ctx: magus\\Context, _a: [str]) > void { ctx.has_charm(\"container\"); }\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("body charm shares a target name", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun container(ctx: magus\\Context, _a: [str]) > void {}\nexport fun build(ctx: magus\\Context, _a: [str]) > void { ctx.has_charm(\"container\"); }\n"})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
	t.Run("target named like a reserved charm", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun cd(ctx: magus\\Context, _a: [str]) > void {}\n"})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
}

func TestCheckHasCharmTypos(t *testing.T) {
	run := func(body string) Check {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(body), 0o644))
		r := &runner{root: root}
		return r.checkHasCharmTypos([]*types.Project{{Path: ".", Dir: root}})
	}

	t.Run("no has_charm reads", func(t *testing.T) {
		assert.Equal(t, StatusOK, run("export fun build(ctx: magus\\Context, _a: [str]) > void {}\n").Status)
	})
	t.Run("live read of a reserved charm", func(t *testing.T) {
		assert.Equal(t, StatusOK, run("export fun b(ctx: magus\\Context, _a: [str]) > void { ctx.has_charm(\"rw\"); }\n").Status)
	})
	t.Run("separator variant of a real charm is live, not a typo", func(t *testing.T) {
		// has_charm("rw_") normalizes to "rw", so the branch is live and must not flag.
		assert.Equal(t, StatusOK, run("export fun b(ctx: magus\\Context, _a: [str]) > void { ctx.has_charm(\"rw_\"); }\n").Status)
	})
	t.Run("novel undeclared charm has no near match, so no flag", func(t *testing.T) {
		assert.Equal(t, StatusOK, run("export fun b(ctx: magus\\Context, _a: [str]) > void { ctx.has_charm(\"container\"); }\n").Status)
	})
	t.Run("misspelling of a real charm is flagged", func(t *testing.T) {
		got := run("export fun b(ctx: magus\\Context, _a: [str]) > void { ctx.has_charm(\"rww\"); }\n")
		assert.Equal(t, StatusFail, got.Status, got.Message)
		require.Len(t, got.Details, 1)
		assert.Contains(t, got.Details[0], "rww")
		assert.Contains(t, got.Details[0], "rw")
	})
}

func TestCheckEnvVars(t *testing.T) {
	t.Run("no unknown vars", func(t *testing.T) {
		for _, kv := range os.Environ() {
			if strings.HasPrefix(kv, "MAGUS_") {
				k := strings.SplitN(kv, "=", 2)[0]
				t.Setenv(k, "")
			}
		}
		r := &runner{}
		got := r.checkEnvVars()
		assert.Equal(t, StatusOK, got.Status, got.Details)
	})

	t.Run("typo'd var", func(t *testing.T) {
		t.Setenv("MAGUS_CACHE_MOD", "auto")
		r := &runner{}
		got := r.checkEnvVars()
		assert.Equal(t, StatusFail, got.Status)
		assert.Contains(t, got.Details, "MAGUS_CACHE_MOD")
	})
}

func TestCheckConfigFile(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	t.Run("no config file", func(t *testing.T) {
		root := t.TempDir()
		r := &runner{root: root}
		got := r.checkConfigFile()
		assert.Equal(t, StatusOK, got.Status, got.Message)
		assert.Contains(t, got.Message, "defaults")
	})

	t.Run("valid config", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "magus.yaml"), "log:\n  format: json\n")
		r := &runner{root: root}
		got := r.checkConfigFile()
		assert.Equal(t, StatusOK, got.Status, got.Details)
	})

	t.Run("unknown key", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "magus.yaml"), "chace:\n  size_mb: 100\n")
		r := &runner{root: root}
		got := r.checkConfigFile()
		assert.Equal(t, StatusFail, got.Status)
		assert.NotEmpty(t, got.Details, "expected at least one detail line")
	})

	t.Run("invalid enum value", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "magus.yaml"), "cache:\n  mode: turbo\n")
		r := &runner{root: root}
		got := r.checkConfigFile()
		assert.Equal(t, StatusFail, got.Status)
	})

	t.Run("dotted filename", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".magus.yaml"), "log:\n  format: text\n")
		r := &runner{root: root}
		got := r.checkConfigFile()
		assert.Equal(t, StatusOK, got.Status, got.Details)
	})
}

func TestCheckCacheWritable(t *testing.T) {
	t.Run("writable dir", func(t *testing.T) {
		root := t.TempDir()
		r := &runner{root: root, opts: options{cfg: config.Config{}}}
		got := r.checkCacheWritable()
		assert.Equal(t, StatusOK, got.Status, got.Message)
		assert.Contains(t, got.Message, root)
		_, err := os.Stat(filepath.Join(root, ".magus"))
		assert.NoError(t, err, "cache dir not created")
	})

	t.Run("absolute cache dir override", func(t *testing.T) {
		root := t.TempDir()
		cacheDir := t.TempDir()
		r := &runner{root: root, opts: options{cfg: config.Config{Cache: config.Cache{Dir: cacheDir}}}}
		got := r.checkCacheWritable()
		assert.Equal(t, StatusOK, got.Status, got.Message)
		assert.Contains(t, got.Message, cacheDir)
	})

	t.Run("unwritable dir", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root can write anywhere; skipping permission test")
		}
		root := t.TempDir()
		cacheDir := filepath.Join(root, "locked")
		require.NoError(t, os.MkdirAll(cacheDir, 0o755))
		require.NoError(t, os.Chmod(cacheDir, 0o555))
		t.Cleanup(func() { _ = os.Chmod(cacheDir, 0o755) })
		r := &runner{root: root, opts: options{cfg: config.Config{Cache: config.Cache{Dir: cacheDir}}}}
		got := r.checkCacheWritable()
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
}

func TestCheckVCSBaseRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}

	t.Run("disabled", func(t *testing.T) {
		f := false
		got := checkVCSBaseRef(t.TempDir(), types.VCSOptions{Enabled: &f})
		assert.Equal(t, StatusSkip, got.Status)
	})

	t.Run("valid HEAD ref", func(t *testing.T) {
		root := makeGitRepo(t)
		t.Setenv("MAGUS_VCS_BASE_REF", "HEAD")
		got := checkVCSBaseRef(root, types.VCSOptions{})
		assert.Equal(t, StatusOK, got.Status, got.Details)
	})

	t.Run("bogus ref fails", func(t *testing.T) {
		root := makeGitRepo(t)
		t.Setenv("MAGUS_VCS_BASE_REF", "refs/does/not/exist")
		got := checkVCSBaseRef(root, types.VCSOptions{})
		assert.Equal(t, StatusFail, got.Status)
	})

	t.Run("detached HEAD ok when base_ref resolves", func(t *testing.T) {
		root := makeGitRepo(t)
		runCmd(t, root, "git", "checkout", "--detach", "HEAD")
		t.Setenv("MAGUS_VCS_BASE_REF", "HEAD")
		got := checkVCSBaseRef(root, types.VCSOptions{})
		assert.Equal(t, StatusOK, got.Status)
	})
}

// tally recounts a report's checks independently of Summary, so a test can assert
// the summary against what was actually appended rather than against itself.
func tally(checks []Check) Summary {
	var s Summary
	for _, c := range checks {
		switch c.Status {
		case StatusOK:
			s.OK++
		case StatusFail:
			s.Fail++
		case StatusSkip:
			s.Skip++
		}
	}
	return s
}

// TestRunSummaryCountsEveryCheckOnWorkspaceError is the regression test for a
// summary that disagreed with the checks printed above it. The counting loop sat
// after the early return taken when the workspace fails to load, so the three
// pre-workspace checks were rendered and never summed: a run showing two passes
// and two failures reported "0 ok, 1 fail", silently dropping a real failure from
// anything reading report.summary instead of report.checks.
func TestRunSummaryCountsEveryCheckOnWorkspaceError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	got := Run(t.TempDir(), nil, errors.New(`workspace://evals: unknown option "no_language"`))

	require.Greater(t, len(got.Checks), 1,
		"the pre-workspace checks should be present alongside the workspace failure")
	assert.Equal(t, tally(got.Checks), got.Summary, "the summary must match the checks it was built from")
	assert.Equal(t, len(got.Checks), got.Summary.OK+got.Summary.Fail+got.Summary.Skip,
		"every appended check must land in exactly one bucket")
	assert.GreaterOrEqual(t, got.Summary.Fail, 1, "the workspace failure itself must be counted")
}

// TestRunSummaryCountsEveryCheckOnFullRun pins the same invariant on the normal
// path, so the two exits cannot drift apart again.
func TestRunSummaryCountsEveryCheckOnFullRun(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	got := Run(root, stubWorkspace{root: root}, nil)

	assert.Equal(t, tally(got.Checks), got.Summary)
	assert.Equal(t, len(got.Checks), got.Summary.OK+got.Summary.Fail+got.Summary.Skip)
}

// TestBridgeReachabilitySkipsWithoutADaemon pins that the unmet-precondition
// branches of the console check report as skipped. Reporting them ok made a
// stopped daemon - the normal state for a CLI-first tool - look like a verified
// bridge.
func TestBridgeReachabilitySkipsWithoutADaemon(t *testing.T) {
	t.Run("no daemon info", func(t *testing.T) {
		assert.Equal(t, StatusSkip, probeBridgeReachability(nil).Status)
	})
	t.Run("bridge disabled", func(t *testing.T) {
		got := probeBridgeReachability(&DaemonInfo{BridgeEnabled: false})
		assert.Equal(t, StatusSkip, got.Status, got.Message)
	})
	t.Run("daemon not reachable", func(t *testing.T) {
		got := probeBridgeReachability(&DaemonInfo{BridgeEnabled: true, Reachable: false})
		assert.Equal(t, StatusSkip, got.Status, got.Message)
		assert.NotEmpty(t, got.Details, "a skip should still say how to make the check run")
	})
	t.Run("mcp address unknown", func(t *testing.T) {
		got := probeBridgeReachability(&DaemonInfo{BridgeEnabled: true, Reachable: true})
		assert.Equal(t, StatusSkip, got.Status, got.Message)
	})
}

// TestCheckGraphCyclesReportsWhatItChecked: an empty graph passes trivially, so it
// skips; a real one reports its size rather than a fixed string that reads the same
// either way.
func TestCheckGraphCyclesReportsWhatItChecked(t *testing.T) {
	t.Run("empty graph skips", func(t *testing.T) {
		got := (&runner{ws: stubWorkspace{root: t.TempDir()}}).checkGraphCycles()
		assert.Equal(t, StatusSkip, got.Status, got.Message)
	})

	t.Run("real graph names what it examined", func(t *testing.T) {
		ws := stubWorkspace{root: t.TempDir(), projects: []*types.Project{
			{Path: "api", DependsOn: []string{"lib"}},
			{Path: "lib"},
		}}
		got := (&runner{ws: ws}).checkGraphCycles()
		require.Equal(t, StatusOK, got.Status, got.Message)
		assert.Contains(t, got.Message, "2 project(s)")
		assert.Contains(t, got.Message, "1 dependency edge(s)")
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoErrorf(t, os.WriteFile(path, []byte(content), 0o644), "writeFile %s", path)
}

func makeGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runCmd(t, dir, "git", "init", "-b", "main")
	runCmd(t, dir, "git", "config", "user.email", "test@example.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")
	runCmd(t, dir, "git", "config", "commit.gpgsign", "false")
	runCmd(t, dir, "git", "config", "tag.gpgsign", "false")
	writeFile(t, filepath.Join(dir, "README"), "hello")
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "initial")
	return dir
}

func runCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "run %v\n%s", args, out)
}

func TestCheckSymlinks(t *testing.T) {
	t.Run("no symlinks", func(t *testing.T) {
		root := canonicalTempDir(t)
		mustMkdir(t, filepath.Join(root, "api"))
		got := checkSymlinks(root)
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})

	t.Run("in-tree symlink is ok", func(t *testing.T) {
		root := canonicalTempDir(t)
		mustMkdir(t, filepath.Join(root, "api"))
		mustSymlink(t, "api", filepath.Join(root, "alias"))
		got := checkSymlinks(root)
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})

	t.Run("escaping symlink fails", func(t *testing.T) {
		root := canonicalTempDir(t)
		outside := canonicalTempDir(t)
		mustSymlink(t, outside, filepath.Join(root, "escape"))
		got := checkSymlinks(root)
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})

	t.Run("dangling symlink to outside fails", func(t *testing.T) {
		root := canonicalTempDir(t)
		mustSymlink(t, "../../nonexistent", filepath.Join(root, "escape"))
		got := checkSymlinks(root)
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})

	t.Run("symlinks inside ignore dirs are skipped", func(t *testing.T) {
		root := canonicalTempDir(t)
		outside := canonicalTempDir(t)
		gitDir := filepath.Join(root, ".git")
		mustMkdir(t, gitDir)
		mustSymlink(t, outside, filepath.Join(gitDir, "escape"))
		got := checkSymlinks(root)
		assert.Equal(t, StatusOK, got.Status, "ignore dir not scanned: "+got.Message)
	})
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err, "eval-symlinks temp dir")
	return dir
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	require.NoErrorf(t, os.MkdirAll(p, 0o755), "mkdir %s", p)
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	require.NoErrorf(t, os.Symlink(target, link), "symlink %s -> %s", link, target)
}

func TestCheckNearDuplicateServices(t *testing.T) {
	dbProject := func(path string, args ...string) *types.Project {
		spell := spells.NewSpell("docker",
			spells.WithTargets("db"),
			spells.WithServiceTargets("db"),
			spells.WithCommandRenderer(func(target string, _ []string) (string, []string, bool, error) {
				return "docker", append([]string{"run"}, args...), true, nil
			}),
		)
		return &types.Project{Path: path, ResolvedSpells: []*spells.Spell{spell}}
	}

	t.Run("clean when no services", func(t *testing.T) {
		got := (&runner{}).checkNearDuplicateServices(nil)
		assert.Equal(t, StatusOK, got.Status)
	})

	t.Run("flags near-duplicates", func(t *testing.T) {
		got := (&runner{}).checkNearDuplicateServices([]*types.Project{
			dbProject("web", "-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15"),
			dbProject("billing", "-e", "POSTGRES_DB=billing", "-p", "5432:5432", "postgres:15"),
		})
		assert.Equal(t, StatusFail, got.Status)
		assert.Contains(t, strings.Join(got.Details, "\n"), "MGS5001")
	})

	t.Run("silent for identical services", func(t *testing.T) {
		got := (&runner{}).checkNearDuplicateServices([]*types.Project{
			dbProject("a", "-p", "5432:5432", "postgres:15"),
			dbProject("b", "-p", "5432:5432", "postgres:15"),
		})
		assert.Equal(t, StatusOK, got.Status)
	})
}

// TestCheckGraphBounds pins the sibling of the escaping-symlink check: the committed
// graph must not name a location outside the workspace. A real graph carried 93 such
// nodes, put there by a language indexer reporting document paths for the dependencies it
// resolved.
func TestCheckGraphBounds(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "gen"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "gen", "knowledge-graph.json"), []byte(body), 0o644))
		return root
	}

	t.Run("no committed graph is a skip, not a pass", func(t *testing.T) {
		got := checkGraphBounds(t.TempDir())
		assert.Equal(t, StatusSkip, got.Status)
	})

	t.Run("in-workspace nodes pass", func(t *testing.T) {
		root := write(t, `{"nodes":[
			{"id":"file:cmd/magus/vcs.go","kind":"file","label":"cmd/magus/vcs.go","source":"cmd/magus/vcs.go"},
			{"id":"dir:cmd/magus","kind":"dir","label":"cmd/magus","source":"cmd/magus"}]}`)
		got := checkGraphBounds(root)
		assert.Equal(t, StatusOK, got.Status)
	})

	t.Run("escaping dir node fails", func(t *testing.T) {
		root := write(t, `{"nodes":[
			{"id":"dir:../../../../../Library/Caches/go-build/01","kind":"dir","label":"../../../../../Library/Caches/go-build/01","source":"../../../../../Library/Caches/go-build/01"}]}`)
		got := checkGraphBounds(root)
		assert.Equal(t, StatusFail, got.Status)
		assert.Contains(t, got.Details, "dir:../../../../../Library/Caches/go-build/01")
	})

	t.Run("path carried only in the label is still caught", func(t *testing.T) {
		// The overlay shards (@coverage, @vcs) mint partial nodes with no Source, so a
		// check reading Source alone would wave these through.
		root := write(t, `{"nodes":[{"id":"file:../escape.go","kind":"file","label":"../escape.go"}]}`)
		got := checkGraphBounds(root)
		assert.Equal(t, StatusFail, got.Status)
	})

	t.Run("import specifiers are exempt", func(t *testing.T) {
		// An import node's ID is the specifier a source file literally wrote, so it
		// records what the code says rather than a path magus resolved.
		root := write(t, `{"nodes":[{"id":"import:../../badge","kind":"import","label":"../../badge"}]}`)
		got := checkGraphBounds(root)
		assert.Equal(t, StatusOK, got.Status)
	})

	t.Run("unreadable graph fails loudly", func(t *testing.T) {
		got := checkGraphBounds(write(t, "not json"))
		assert.Equal(t, StatusFail, got.Status)
	})
}
