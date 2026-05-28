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
)

// ── checkLanguageCoverage ──────────────────────────────────────────────

func TestCheckLanguageCoverage(t *testing.T) {
	tests := []struct {
		name     string
		projects []*types.Project
		status   CheckStatus
	}{
		{"all have spell", []*types.Project{{Spell: "go"}, {Spell: "rust"}}, StatusOK},
		{"some missing", []*types.Project{{Spell: ""}, {Spell: "go"}}, StatusWarn},
		{"all missing", []*types.Project{{Spell: ""}, {Spell: ""}}, StatusWarn},
		{"empty list", []*types.Project{}, StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &runner{}
			got := r.checkLanguageCoverage(tt.projects)
			if got.Status != tt.status {
				t.Errorf("status = %q, want %q; message: %s", got.Status, tt.status, got.Message)
			}
		})
	}
}

// ── checkCITarget ──────────────────────────────────────────────────────

func TestCheckCITarget(t *testing.T) {
	// projectWith writes name→body magusfile(s) into a fresh dir and returns it.
	projectWith := func(files map[string]string) *types.Project {
		dir := t.TempDir()
		for name, body := range files {
			p := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, p, body)
		}
		return &types.Project{Dir: dir}
	}

	tests := []struct {
		name     string
		projects []*types.Project
		status   CheckStatus
	}{
		{"no projects skipped", nil, StatusOK},
		{
			"ci declared",
			[]*types.Project{projectWith(map[string]string{"magusfile.bzz": "export fun ci(_a: [str]) > void {}\n"})},
			StatusOK,
		},
		{
			"ci declared (buzz, any casing)",
			[]*types.Project{projectWith(map[string]string{"magusfile.bzz": "export fun CI(_a: [str]) > void {}\n"})},
			StatusOK,
		},
		{
			"ci declared in one of several projects",
			[]*types.Project{
				projectWith(map[string]string{"magusfile.bzz": "export fun build(_a: [str]) > void {}\n"}),
				projectWith(map[string]string{"magusfile.bzz": "export fun ci(_a: [str]) > void {}\n"}),
			},
			StatusOK,
		},
		{
			"no ci anywhere fails",
			[]*types.Project{projectWith(map[string]string{"magusfile.bzz": "export fun build(_a: [str]) > void {}\n"})},
			StatusFail,
		},
		{
			"cipher is not ci",
			[]*types.Project{projectWith(map[string]string{"magusfile.bzz": "export fun cipher(_a: [str]) > void {}\n"})},
			StatusFail,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (&runner{}).checkCITarget(tt.projects)
			if got.Status != tt.status {
				t.Errorf("status = %q, want %q; message: %s; details: %v", got.Status, tt.status, got.Message, got.Details)
			}
		})
	}
}

// TestCheckCITarget_FailDetails pins that the failure points the user at how to
// define ci and references the MGS1001 doc.
func TestCheckCITarget_FailDetails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "magusfile.bzz"), "export fun build(_a: [str]) > void {}\n")
	got := (&runner{}).checkCITarget([]*types.Project{{Dir: dir}})
	if got.Status != StatusFail {
		t.Fatalf("status = %q, want fail", got.Status)
	}
	joined := strings.Join(got.Details, "\n")
	if !strings.Contains(joined, "magus.depends_on") {
		t.Errorf("details should show how to define ci; got: %v", got.Details)
	}
	if !strings.Contains(joined, string(types.NoCITarget)) {
		t.Errorf("details should reference %s; got: %v", types.NoCITarget, got.Details)
	}
}

// ── checkSpellDocs ─────────────────────────────────────────────────────

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

	tests := []struct {
		name   string
		spells []*types.Spell
		status CheckStatus
	}{
		{"no spells", nil, StatusOK},
		{"exempt spell with no docs", []*types.Spell{exempt}, StatusOK},
		{"local spell fully documented", []*types.Spell{localComplete}, StatusOK},
		{"local spell missing a doc", []*types.Spell{localMissing}, StatusFail},
		{"record-style target exempt", []*types.Spell{recordStyle}, StatusOK},
		{"exempt does not rescue local", []*types.Spell{exempt, localMissing}, StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &runner{}
			got := r.checkSpellDocs(tt.spells)
			if got.Status != tt.status {
				t.Errorf("status = %q, want %q; message: %s, details: %v", got.Status, tt.status, got.Message, got.Details)
			}
		})
	}
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
	if got.Status != StatusFail {
		t.Fatalf("status = %q, want fail", got.Status)
	}
	want := []string{"local:lint", "local:test"}
	if strings.Join(got.Details, ",") != strings.Join(want, ",") {
		t.Errorf("details = %v, want %v", got.Details, want)
	}
}

// ── checkTargetNameConventions ─────────────────────────────────────────

func TestCheckTargetNameConventions(t *testing.T) {
	tests := []struct {
		name   string
		files  map[string]string // filename → body, written into one project dir
		status CheckStatus
	}{
		{
			"consistent snake_case",
			map[string]string{"magusfile.bzz": "export fun go_build(_a: [str]) > void {}\nexport fun go_test(_a: [str]) > void {}\n"},
			StatusOK,
		},
		{
			"neutral names only",
			map[string]string{"magusfile.bzz": "export fun build(_a: [str]) > void {}\nexport fun test(_a: [str]) > void {}\n"},
			StatusOK,
		},
		{
			"snake and camel mixed",
			map[string]string{"magusfile.bzz": "export fun go_build(_a: [str]) > void {}\nexport fun goTest(_a: [str]) > void {}\n"},
			StatusWarn,
		},
		{
			"mixed across magusfiles dir",
			map[string]string{
				"magusfiles/a.bzz": "export fun go_build(_a: [str]) > void {}\n",
				"magusfiles/b.bzz": "export fun GoTest(_a: [str]) > void {}\n",
			},
			StatusWarn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range tt.files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			r := &runner{root: root}
			got := r.checkTargetNameConventions([]*types.Project{{Path: ".", Dir: root}})
			if got.Status != tt.status {
				t.Errorf("status = %q, want %q; message: %s; details: %v", got.Status, tt.status, got.Message, got.Details)
			}
		})
	}
}

// ── checkCharmTargetCollision ──────────────────────────────────────────

func TestCheckCharmTargetCollision(t *testing.T) {
	tests := []struct {
		name   string
		files  map[string]string // filename → body, written into one project dir
		status CheckStatus
	}{
		{
			"no charms, no collision",
			map[string]string{"magusfile.bzz": "export fun build(_a: [str]) > void {}\n"},
			StatusOK,
		},
		{
			"charm distinct from every target",
			map[string]string{"magusfile.bzz": "export fun build(_a: [str]) > void { magus.has_charm(\"container\"); }\n"},
			StatusOK,
		},
		{
			"body charm shares a target name",
			map[string]string{"magusfile.bzz": "export fun container(_a: [str]) > void {}\nexport fun build(_a: [str]) > void { magus.has_charm(\"container\"); }\n"},
			StatusWarn,
		},
		{
			"target named like a reserved charm",
			map[string]string{"magusfile.bzz": "export fun cd(_a: [str]) > void {}\n"},
			StatusWarn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range tt.files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			r := &runner{root: root}
			got := r.checkCharmTargetCollision([]*types.Project{{Path: ".", Dir: root}})
			if got.Status != tt.status {
				t.Errorf("status = %q, want %q; message: %s; details: %v", got.Status, tt.status, got.Message, got.Details)
			}
		})
	}
}

// ── checkShellCompletion ───────────────────────────────────────────────

func TestShellCompletionInstalled(t *testing.T) {
	tests := []struct {
		name  string
		shell string
		// files written under a fake $HOME: path → contents
		files map[string]string
		want  bool
	}{
		{"bash appended script", "bash", map[string]string{".bashrc": "alias x=y\ncomplete -F _magus_complete magus mgs\n"}, true},
		{"bash source-eval", "bash", map[string]string{".bashrc": "source <(magus completion bash)\n"}, true},
		{"bash absent", "bash", map[string]string{".bashrc": "export PATH=$PATH:/x\n"}, false},
		{"zsh present", "zsh", map[string]string{".zshrc": "source <(magus completion zsh)\n"}, true},
		{"fish completion file", "fish", map[string]string{".config/fish/completions/magus.fish": "complete -c magus\n"}, true},
		{"fish absent", "fish", map[string]string{".config/fish/config.fish": "set -g x y\n"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			for rel, body := range tt.files {
				p := filepath.Join(home, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := shellCompletionInstalled(tt.shell, home); got != tt.want {
				t.Errorf("shellCompletionInstalled(%q) = %v, want %v", tt.shell, got, tt.want)
			}
		})
	}
}

// ── checkBinaryTree ───────────────────────────────────────────────────

func TestCheckBinaryTree(t *testing.T) {
	r := &runner{opts: options{}}

	r.opts.commit = ""
	if got := r.checkBinaryTree(); got.Status != StatusOK {
		t.Errorf("empty commit: status = %q, want ok", got.Status)
	}

	r.opts.commit = "abc1234"
	if got := r.checkBinaryTree(); got.Status != StatusOK {
		t.Errorf("clean commit: status = %q, want ok", got.Status)
	}

	r.opts.commit = "abc1234-dirty"
	if got := r.checkBinaryTree(); got.Status != StatusWarn {
		t.Errorf("dirty commit: status = %q, want warn", got.Status)
	}
}

// ── checkEnvVars ──────────────────────────────────────────────────────

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
		if got.Status != StatusOK {
			t.Errorf("status = %q, want ok; details: %v", got.Status, got.Details)
		}
	})

	t.Run("typo'd var", func(t *testing.T) {
		t.Setenv("MAGUS_CACHE_MOD", "auto")
		r := &runner{}
		got := r.checkEnvVars()
		if got.Status != StatusWarn {
			t.Errorf("status = %q, want warn", got.Status)
		}
		found := false
		for _, d := range got.Details {
			if d == "MAGUS_CACHE_MOD" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected MAGUS_CACHE_MOD in details, got %v", got.Details)
		}
	})
}

// ── checkConfigFile ───────────────────────────────────────────────────

func TestCheckConfigFile(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	t.Run("no config file", func(t *testing.T) {
		root := t.TempDir()
		r := &runner{root: root}
		got := r.checkConfigFile()
		if got.Status != StatusOK {
			t.Errorf("status = %q, want ok; message: %s", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, "defaults") {
			t.Errorf("message should mention defaults, got: %s", got.Message)
		}
	})

	t.Run("valid config", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "magus.yaml"), "log:\n  format: json\n")
		r := &runner{root: root}
		got := r.checkConfigFile()
		if got.Status != StatusOK {
			t.Errorf("status = %q, want ok; details: %v", got.Status, got.Details)
		}
	})

	t.Run("unknown key", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "magus.yaml"), "chace:\n  size_mb: 100\n")
		r := &runner{root: root}
		got := r.checkConfigFile()
		if got.Status != StatusFail {
			t.Errorf("status = %q, want fail", got.Status)
		}
		if len(got.Details) == 0 {
			t.Error("expected at least one detail line")
		}
	})

	t.Run("invalid enum value", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "magus.yaml"), "cache:\n  mode: turbo\n")
		r := &runner{root: root}
		got := r.checkConfigFile()
		if got.Status != StatusFail {
			t.Errorf("status = %q, want fail", got.Status)
		}
	})

	t.Run("dotted filename", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".magus.yaml"), "log:\n  format: text\n")
		r := &runner{root: root}
		got := r.checkConfigFile()
		if got.Status != StatusOK {
			t.Errorf("dotted filename: status = %q, want ok; details: %v", got.Status, got.Details)
		}
	})
}

// ── checkCacheWritable ────────────────────────────────────────────────

func TestCheckCacheWritable(t *testing.T) {
	t.Run("writable dir", func(t *testing.T) {
		root := t.TempDir()
		r := &runner{root: root, opts: options{cfg: config.Config{}}}
		got := r.checkCacheWritable()
		if got.Status != StatusOK {
			t.Errorf("status = %q, want ok; message: %s", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, root) {
			t.Errorf("message should contain root path, got: %s", got.Message)
		}
		if _, err := os.Stat(filepath.Join(root, ".magus")); err != nil {
			t.Errorf("cache dir not created: %v", err)
		}
	})

	t.Run("absolute cache dir override", func(t *testing.T) {
		root := t.TempDir()
		cacheDir := t.TempDir()
		r := &runner{root: root, opts: options{cfg: config.Config{Cache: config.Cache{Dir: cacheDir}}}}
		got := r.checkCacheWritable()
		if got.Status != StatusOK {
			t.Errorf("status = %q, want ok; message: %s", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, cacheDir) {
			t.Errorf("message should reference cache dir, got: %s", got.Message)
		}
	})

	t.Run("unwritable dir", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root can write anywhere; skipping permission test")
		}
		root := t.TempDir()
		cacheDir := filepath.Join(root, "locked")
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(cacheDir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(cacheDir, 0o755) })
		r := &runner{root: root, opts: options{cfg: config.Config{Cache: config.Cache{Dir: cacheDir}}}}
		got := r.checkCacheWritable()
		if got.Status != StatusFail {
			t.Errorf("status = %q, want fail; message: %s", got.Status, got.Message)
		}
	})
}

// ── checkVCSBaseRef ───────────────────────────────────────────────────

func TestCheckVCSBaseRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}

	t.Run("disabled", func(t *testing.T) {
		f := false
		got := checkVCSBaseRef(t.TempDir(), types.VCSOptions{Enabled: &f})
		if got.Status != StatusOK {
			t.Errorf("disabled: status = %q, want ok", got.Status)
		}
	})

	t.Run("valid HEAD ref", func(t *testing.T) {
		root := makeGitRepo(t)
		t.Setenv("MAGUS_VCS_BASE_REF", "HEAD")
		got := checkVCSBaseRef(root, types.VCSOptions{})
		if got.Status != StatusOK {
			t.Errorf("valid ref: status = %q, want ok; details: %v", got.Status, got.Details)
		}
	})

	t.Run("bogus ref warns", func(t *testing.T) {
		root := makeGitRepo(t)
		t.Setenv("MAGUS_VCS_BASE_REF", "refs/does/not/exist")
		got := checkVCSBaseRef(root, types.VCSOptions{})
		if got.Status != StatusWarn {
			t.Errorf("bogus ref: status = %q, want warn", got.Status)
		}
	})

	t.Run("detached HEAD warns", func(t *testing.T) {
		root := makeGitRepo(t)
		runCmd(t, root, "git", "checkout", "--detach", "HEAD")
		t.Setenv("MAGUS_VCS_BASE_REF", "HEAD")
		got := checkVCSBaseRef(root, types.VCSOptions{})
		if got.Status != StatusWarn {
			t.Errorf("detached HEAD: status = %q, want warn", got.Status)
		}
		if !strings.Contains(got.Message, "detached") {
			t.Errorf("expected 'detached' in message, got: %s", got.Message)
		}
	})
}

// ── checkNearCycles ───────────────────────────────────────────────────

func TestCheckNearCycles(t *testing.T) {
	r := &runner{opts: options{cfg: config.Config{}}}

	t.Run("no near-cycles isolated", func(t *testing.T) {
		_, g := buildDocGraph(t, [][]string{
			{"standalone", "go"},
		})
		got := r.checkNearCycles(g)
		if got.Status != StatusOK {
			t.Errorf("status = %q, want ok", got.Status)
		}
	})

	t.Run("near-cycle detected", func(t *testing.T) {
		_, g := buildDocGraph(t, [][]string{
			{"a", "go", "b"},
			{"b", "go"},
		})
		got := r.checkNearCycles(g)
		if got.Status != StatusWarn {
			t.Errorf("status = %q, want warn", got.Status)
		}
		if len(got.Details) == 0 {
			t.Error("expected at least one detail line")
		}
	})
}

// ── checkFanOut ───────────────────────────────────────────────────────

func TestCheckFanOut(t *testing.T) {
	t.Run("below threshold", func(t *testing.T) {
		r := &runner{opts: options{cfg: config.Config{}}}
		projects := []*types.Project{
			{Path: "a", DependsOn: []string{"b", "c"}},
		}
		got := r.checkFanOut(projects)
		if got.Status != StatusOK {
			t.Errorf("status = %q, want ok", got.Status)
		}
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
		if got.Status != StatusWarn {
			t.Errorf("status = %q, want warn", got.Status)
		}
		if len(got.Details) == 0 {
			t.Error("expected at least one detail line")
		}
	})
}

// ── checkBlastRadius ──────────────────────────────────────────────────

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
		if got.Status != StatusOK {
			t.Errorf("status = %q, want ok (isolated nodes)", got.Status)
		}
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
		if got.Status != StatusWarn {
			t.Errorf("status = %q, want warn (shared rebuilds 100%%)", got.Status)
		}
		if len(got.Details) == 0 {
			t.Error("expected at least one detail line")
		}
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
		if got.Status != StatusOK {
			t.Errorf("status = %q, want ok (shared is exempt)", got.Status)
		}
	})
}

// ── checkDependencyTangle ─────────────────────────────────────────────

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
		if got.Status != StatusOK {
			t.Errorf("status = %q, want ok for star topology (low NCCD)", got.Status)
		}
	})
}

// ── helpers ───────────────────────────────────────────────────────────

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
	if err != nil {
		t.Fatalf("depgraph.Build(): %v", err)
	}
	return ws.All(), g
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
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
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run %v: %v\n%s", args, err, out)
	}
}
