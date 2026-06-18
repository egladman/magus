package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/depgraph"
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
		assert.Equal(t, StatusWarn, got.Status, got.Message)
	})
	t.Run("all missing", func(t *testing.T) {
		got := r.checkLanguageCoverage([]*types.Project{{Spell: ""}, {Spell: ""}})
		assert.Equal(t, StatusWarn, got.Status, got.Message)
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
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("ci declared", func(t *testing.T) {
		got := (&runner{}).checkCITarget([]*types.Project{
			projectWith(map[string]string{"magusfile.buzz": "export fun ci(_a: [str]) > void {}\n"}),
		})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("ci declared (buzz, any casing)", func(t *testing.T) {
		got := (&runner{}).checkCITarget([]*types.Project{
			projectWith(map[string]string{"magusfile.buzz": "export fun CI(_a: [str]) > void {}\n"}),
		})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("ci declared in one of several projects", func(t *testing.T) {
		got := (&runner{}).checkCITarget([]*types.Project{
			projectWith(map[string]string{"magusfile.buzz": "export fun build(_a: [str]) > void {}\n"}),
			projectWith(map[string]string{"magusfile.buzz": "export fun ci(_a: [str]) > void {}\n"}),
		})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("no ci anywhere fails", func(t *testing.T) {
		got := (&runner{}).checkCITarget([]*types.Project{
			projectWith(map[string]string{"magusfile.buzz": "export fun build(_a: [str]) > void {}\n"}),
		})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
	t.Run("cipher is not ci", func(t *testing.T) {
		got := (&runner{}).checkCITarget([]*types.Project{
			projectWith(map[string]string{"magusfile.buzz": "export fun cipher(_a: [str]) > void {}\n"}),
		})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
}

// TestCheckCITarget_FailDetails pins that the failure points the user at how to
// define ci and references the MGS1001 doc.
func TestCheckCITarget_FailDetails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "magusfile.buzz"), "export fun build(_a: [str]) > void {}\n")
	got := (&runner{}).checkCITarget([]*types.Project{{Dir: dir}})
	require.Equal(t, StatusFail, got.Status)
	joined := strings.Join(got.Details, "\n")
	assert.Contains(t, joined, "magus.needs", "details should show how to define ci")
	assert.Contains(t, joined, string(types.NoCITarget), "details should reference the doc")
}

func TestCheckSpellDocs(t *testing.T) {
	// A local Buzz spell with one documented and one undocumented handler target.
	localMissing := types.NewSpell("local",
		types.WithTargets("build", "test"),
		types.WithTargetDocs(map[string]string{"build": "build compiles."}),
		types.WithDocRequiredTargets("build", "test"),
	)
	localComplete := types.NewSpell("local",
		types.WithTargets("build", "test"),
		types.WithTargetDocs(map[string]string{"build": "build compiles.", "test": "test runs the suite."}),
		types.WithDocRequiredTargets("build", "test"),
	)
	// A record-style target ("deploy" not in the doc-required set) is exempt even
	// when undocumented, alongside a documented handler target.
	recordStyle := types.NewSpell("local",
		types.WithTargets("build", "deploy"),
		types.WithTargetDocs(map[string]string{"build": "build compiles."}),
		types.WithDocRequiredTargets("build"),
	)
	// A spell that opts in nothing (built-in / Teal) is exempt even with no docs.
	exempt := types.NewSpell("builtin", types.WithTargets("build", "test"))

	r := &runner{}

	t.Run("no spells", func(t *testing.T) {
		got := r.checkSpellDocs(nil)
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("exempt spell with no docs", func(t *testing.T) {
		got := r.checkSpellDocs([]*types.Spell{exempt})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("local spell fully documented", func(t *testing.T) {
		got := r.checkSpellDocs([]*types.Spell{localComplete})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("local spell missing a doc", func(t *testing.T) {
		got := r.checkSpellDocs([]*types.Spell{localMissing})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
	t.Run("record-style target exempt", func(t *testing.T) {
		got := r.checkSpellDocs([]*types.Spell{recordStyle})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("exempt does not rescue local", func(t *testing.T) {
		got := r.checkSpellDocs([]*types.Spell{exempt, localMissing})
		assert.Equal(t, StatusFail, got.Status, got.Message)
	})
}

// TestCheckSpellDocs_Details pins that the failure lists the exact missing
// spell:target pairs, which is what tells the user what to document.
func TestCheckSpellDocs_Details(t *testing.T) {
	s := types.NewSpell("local",
		types.WithTargets("build", "lint", "test"),
		types.WithTargetDocs(map[string]string{"build": "build compiles."}),
		types.WithDocRequiredTargets("build", "lint", "test"),
	)
	got := (&runner{}).checkSpellDocs([]*types.Spell{s})
	require.Equal(t, StatusFail, got.Status)
	assert.Equal(t, []string{"local:lint", "local:test"}, got.Details)
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
		got := run(map[string]string{"magusfile.buzz": "export fun go_build(_a: [str]) > void {}\nexport fun go_test(_a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("neutral names only", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun build(_a: [str]) > void {}\nexport fun test(_a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("snake and camel mixed", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun go_build(_a: [str]) > void {}\nexport fun goTest(_a: [str]) > void {}\n"})
		assert.Equal(t, StatusWarn, got.Status, got.Message)
	})
	t.Run("mixed across magusfiles dir", func(t *testing.T) {
		got := run(map[string]string{
			"magusfiles/a.buzz": "export fun go_build(_a: [str]) > void {}\n",
			"magusfiles/b.buzz": "export fun GoTest(_a: [str]) > void {}\n",
		})
		assert.Equal(t, StatusWarn, got.Status, got.Message)
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
		got := run(map[string]string{"magusfile.buzz": "export fun ci(_a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})

	t.Run("embedding constructs are allowed", func(t *testing.T) {
		// Top-level host calls and statements are embedding-only constructs that
		// upstream-strict parsing rejects; magusfiles parse in embedded mode.
		got := run(map[string]string{"magusfile.buzz": "magus.needs(magus.target.literal(\"build\"));\nexport fun ci(_a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})

	t.Run("syntax error fails", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun ci(_a: [str]) > void {\n"})
		assert.Equal(t, StatusFail, got.Status, got.Message)
		assert.NotEmpty(t, got.Details, "expected the offending file in details")
	})

	t.Run("all magusfiles reported, not just the first", func(t *testing.T) {
		got := run(map[string]string{
			"magusfiles/a.buzz": "export fun a(_a: [str]) > void {\n", // broken
			"magusfiles/b.buzz": "export fun b(_a: [str]) > void {\n", // broken
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
		got := run(map[string]string{"magusfile.buzz": "export fun build(_a: [str]) > void {}\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("charm distinct from every target", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun build(_a: [str]) > void { magus.has_charm(\"container\"); }\n"})
		assert.Equal(t, StatusOK, got.Status, got.Message)
	})
	t.Run("body charm shares a target name", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun container(_a: [str]) > void {}\nexport fun build(_a: [str]) > void { magus.has_charm(\"container\"); }\n"})
		assert.Equal(t, StatusWarn, got.Status, got.Message)
	})
	t.Run("target named like a reserved charm", func(t *testing.T) {
		got := run(map[string]string{"magusfile.buzz": "export fun cd(_a: [str]) > void {}\n"})
		assert.Equal(t, StatusWarn, got.Status, got.Message)
	})
}

func TestShellCompletionInstalled(t *testing.T) {
	// run writes files under a fake $HOME and reports whether completion is detected.
	run := func(shell string, files map[string]string) bool {
		home := t.TempDir()
		for rel, body := range files {
			p := filepath.Join(home, rel)
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
		}
		return shellCompletionInstalled(shell, home)
	}

	t.Run("bash appended script", func(t *testing.T) {
		assert.True(t, run("bash", map[string]string{".bashrc": "alias x=y\ncomplete -F _magus_complete magus mgs\n"}))
	})
	t.Run("bash source-eval", func(t *testing.T) {
		assert.True(t, run("bash", map[string]string{".bashrc": "source <(magus completion bash)\n"}))
	})
	t.Run("bash absent", func(t *testing.T) {
		assert.False(t, run("bash", map[string]string{".bashrc": "export PATH=$PATH:/x\n"}))
	})
	t.Run("zsh present", func(t *testing.T) {
		assert.True(t, run("zsh", map[string]string{".zshrc": "source <(magus completion zsh)\n"}))
	})
	t.Run("fish completion file", func(t *testing.T) {
		assert.True(t, run("fish", map[string]string{".config/fish/completions/magus.fish": "complete -c magus\n"}))
	})
	t.Run("fish absent", func(t *testing.T) {
		assert.False(t, run("fish", map[string]string{".config/fish/config.fish": "set -g x y\n"}))
	})
}

func TestCheckBinaryTree(t *testing.T) {
	r := &runner{opts: options{}}

	r.opts.commit = ""
	assert.Equal(t, StatusOK, r.checkBinaryTree().Status, "empty commit")

	r.opts.commit = "abc1234"
	assert.Equal(t, StatusOK, r.checkBinaryTree().Status, "clean commit")

	r.opts.commit = "abc1234-dirty"
	assert.Equal(t, StatusWarn, r.checkBinaryTree().Status, "dirty commit")
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
		assert.Equal(t, StatusWarn, got.Status)
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
		assert.Equal(t, StatusOK, got.Status)
	})

	t.Run("valid HEAD ref", func(t *testing.T) {
		root := makeGitRepo(t)
		t.Setenv("MAGUS_VCS_BASE_REF", "HEAD")
		got := checkVCSBaseRef(root, types.VCSOptions{})
		assert.Equal(t, StatusOK, got.Status, got.Details)
	})

	t.Run("bogus ref warns", func(t *testing.T) {
		root := makeGitRepo(t)
		t.Setenv("MAGUS_VCS_BASE_REF", "refs/does/not/exist")
		got := checkVCSBaseRef(root, types.VCSOptions{})
		assert.Equal(t, StatusWarn, got.Status)
	})

	t.Run("detached HEAD warns", func(t *testing.T) {
		root := makeGitRepo(t)
		runCmd(t, root, "git", "checkout", "--detach", "HEAD")
		t.Setenv("MAGUS_VCS_BASE_REF", "HEAD")
		got := checkVCSBaseRef(root, types.VCSOptions{})
		assert.Equal(t, StatusWarn, got.Status)
		assert.Contains(t, got.Message, "detached")
	})
}

func TestCheckNearCycles(t *testing.T) {
	r := &runner{opts: options{cfg: config.Config{}}}

	t.Run("no near-cycles isolated", func(t *testing.T) {
		_, g := buildDocGraph(t, [][]string{
			{"standalone", "go"},
		})
		got := r.checkNearCycles(g)
		assert.Equal(t, StatusOK, got.Status)
	})

	t.Run("near-cycle detected", func(t *testing.T) {
		_, g := buildDocGraph(t, [][]string{
			{"a", "go", "b"},
			{"b", "go"},
		})
		got := r.checkNearCycles(g)
		assert.Equal(t, StatusWarn, got.Status)
		assert.NotEmpty(t, got.Details, "expected at least one detail line")
	})
}

func TestCheckFanOut(t *testing.T) {
	t.Run("below threshold", func(t *testing.T) {
		r := &runner{opts: options{cfg: config.Config{}}}
		projects := []*types.Project{
			{Path: "a", DependsOn: []string{"b", "c"}},
		}
		got := r.checkFanOut(projects)
		assert.Equal(t, StatusOK, got.Status)
	})

	t.Run("exceeds threshold", func(t *testing.T) {
		r := &runner{opts: options{cfg: config.Config{}}}
		// Build 21 dependencies to exceed the hardcoded threshold of 20.
		deps := make([]string, 21)
		for i := range deps {
			deps[i] = fmt.Sprintf("dep%02d", i)
		}
		projects := []*types.Project{
			{Path: "kitchen-sink", DependsOn: deps},
		}
		got := r.checkFanOut(projects)
		assert.Equal(t, StatusWarn, got.Status)
		assert.NotEmpty(t, got.Details, "expected at least one detail line")
	})
}

func TestCheckBlastRadius(t *testing.T) {
	t.Run("below threshold", func(t *testing.T) {
		// 6 isolated nodes: each rebuilds only itself → blast radius 1/6 ≈ 16.7% < 20%.
		r := &runner{opts: options{cfg: config.Config{}}}
		projects, g := buildDocGraph(t, [][]string{
			{"a", "go"},
			{"b", "go"},
			{"c", "go"},
			{"d", "go"},
			{"e", "go"},
			{"f", "go"},
		})
		got := r.checkBlastRadius(g, projects)
		assert.Equal(t, StatusOK, got.Status, "isolated nodes")
	})

	t.Run("exceeds threshold", func(t *testing.T) {
		// "shared" is a dependency of both a and b, giving it 100% blast radius — well above 20%.
		r := &runner{opts: options{cfg: config.Config{}}}
		projects, g := buildDocGraph(t, [][]string{
			{"a", "go", "shared"},
			{"b", "go", "shared"},
			{"shared", "go"},
		})
		got := r.checkBlastRadius(g, projects)
		assert.Equal(t, StatusWarn, got.Status, "shared rebuilds 100%")
		assert.NotEmpty(t, got.Details, "expected at least one detail line")
	})

	t.Run("exempt suppresses warning", func(t *testing.T) {
		// 6 projects: only "shared" exceeds 20% blast radius (3/6 = 50%).
		// With "shared" exempt, all remaining projects are below 20% (1/6 ≈ 16.7%).
		r := &runner{opts: options{cfg: config.Config{Health: config.Health{Exempt: []string{"shared"}}}}}
		projects, g := buildDocGraph(t, [][]string{
			{"a", "go", "shared"},
			{"b", "go", "shared"},
			{"shared", "go"},
			{"c", "go"},
			{"d", "go"},
			{"e", "go"},
		})
		got := r.checkBlastRadius(g, projects)
		assert.Equal(t, StatusOK, got.Status, "shared is exempt")
	})
}

func TestCheckDependencyTangle(t *testing.T) {
	t.Run("well-layered graph", func(t *testing.T) {
		// Star topology: all leaves depend on one center. NCCD is well below 2.0.
		r := &runner{opts: options{cfg: config.Config{}}}
		_, g := buildDocGraph(t, [][]string{
			{"center", "go"},
			{"leaf1", "go", "center"},
			{"leaf2", "go", "center"},
			{"leaf3", "go", "center"},
			{"leaf4", "go", "center"},
		})
		got := r.checkDependencyTangle(g)
		assert.Equal(t, StatusOK, got.Status, "star topology (low NCCD)")
	})
}

func buildDocGraph(t *testing.T, entries [][]string) ([]*types.Project, *types.Graph) {
	t.Helper()
	ws := &types.Workspace{
		Root:     "/fake",
		Projects: map[string]*types.Project{},
	}
	for _, e := range entries {
		path := e[0]
		lang := e[1]
		deps := e[2:]
		ws.Projects[path] = &types.Project{
			Path:      path,
			Dir:       "/fake/" + path,
			Spell:     lang,
			DependsOn: deps,
		}
	}
	g, err := depgraph.Build(ws)
	require.NoError(t, err, "depgraph.Build()")
	return ws.All(), g
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
