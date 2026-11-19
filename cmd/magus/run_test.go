package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus"
	internalci "github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"
)

// TestSkipFlag proves the hand-bound repeatable flag accumulates, splits a
// comma-separated value, and rejects an empty ref in either spelling.
func TestSkipFlag(t *testing.T) {
	var f skipFlag
	require.NoError(t, f.Set("docs"))
	require.NoError(t, f.Set("console"))
	require.Error(t, f.Set(""))
	assert.Equal(t, []string{"docs", "console"}, f.refs)
	assert.Equal(t, "docs,console", f.String())

	var c skipFlag
	require.NoError(t, c.Set("docs, console"))
	assert.Equal(t, []string{"docs", "console"}, c.refs)
	require.Error(t, c.Set("docs,"), "a trailing comma is an empty ref, not a silent no-op")
	require.Error(t, c.Set(",,"))
}

// TestClientCwd proves the client's cwd carried on ctx wins over the process os.Getwd,
// so an adopted run scopes and journals against where the client ran, not the server.
func TestClientCwd(t *testing.T) {
	t.Run("ctx cwd wins", func(t *testing.T) {
		ctx := proc.WithCurrentWorkingDirectory(context.Background(), "/client/here")
		assert.Equal(t, "/client/here", clientCurrentWorkingDirectory(ctx))
	})

	t.Run("no ctx cwd falls back to os.Getwd", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		got := clientCurrentWorkingDirectory(context.Background())
		// t.TempDir may hand back a /var symlink that Getwd resolves; compare suffix.
		require.NotEmpty(t, got)
		assert.True(t, strings.HasSuffix(got, dir) || strings.HasSuffix(dir, got) || got == dir,
			"fallback cwd %q should relate to %q", got, dir)
	})
}

// TestCrossProjectOutputReplaysFromCache is the execution half of cross-project outputs:
// a target declaring ctx.writesFiles([<sibling>.file(...)]) writes into ANOTHER project's tree,
// and magus snapshots that file like any other output, so a cache hit restores it after
// it is deleted. Getting this wrong is quiet: the second build reports success over a
// file that is no longer there.
//
// It lives here rather than beside the root package's cross-output tests because it needs
// a magusfile body to actually run, and the interpreter is a pack only cmd/magus wires in
// (packs_interp.go): the root test binary registers shim spells that collide with it.
func TestCrossProjectOutputReplaysFromCache(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root, write := w.Root(), w.Write
	write("magusfile.buzz", "")
	write("site/magusfile.buzz", "")
	// fs.writeFile, not a shelled-out redirect: what is under test is magus's handling of
	// a foreign-tree output, and a subprocess would drag /bin/sh in for nothing.
	//
	// The body also writes an UNDECLARED marker beside the output. That is what makes
	// replay observable: a cache miss re-runs the body and recreates both files, a cache
	// hit restores only the declared one. Without it the assertions below pass either way
	// and the test proves nothing. The marker sits in site/, not producer/, so creating it
	// does not perturb producer's own source hash and invalidate the entry under test.
	write("producer/magusfile.buzz", `import "magus";
import "fs";
import "project/../site";

export fun build(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles([site.file("generated.txt")]);
    fs\writeFile("../site/body-ran.marker", "1");
    fs\writeFile("../site/generated.txt", "hello");
}
`)

	ctx := context.Background()
	m, err := magus.Open(ctx, root, magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	// m.Root() is symlink-resolved by project.Discover; build the path on it so it shares
	// that canonical base (t.TempDir resolves through /private on macOS).
	generated := filepath.Join(m.Root(), "site", "generated.txt")
	marker := filepath.Join(m.Root(), "site", "body-ran.marker")
	targets := []types.Target{{Path: "producer", Name: "build"}}

	require.NoError(t, m.Run(ctx, targets), "first run")
	content, err := os.ReadFile(generated)
	require.NoError(t, err, "producer must write into the sibling tree")
	assert.Equal(t, "hello", string(content))
	require.FileExists(t, marker, "first run executes the body")

	// Remove both. Only the declared output may come back.
	require.NoError(t, os.Remove(generated))
	require.NoError(t, os.Remove(marker))

	require.NoError(t, m.Run(ctx, targets), "replay run")
	content, err = os.ReadFile(generated)
	require.NoError(t, err, "a cache hit must replay the output into the OWNING project's tree")
	assert.Equal(t, "hello", string(content))
	assert.NoFileExists(t, marker,
		"the marker is undeclared, so its absence is what proves the run was a cache hit and not a re-execution")
}

// TestCrossProjectOutputMustBeProduced covers the mismatch nothing else can catch: the
// path a target DECLARES and the path it WRITES are two independent strings, spelled
// differently by convention (the declaration is relative to the owner, the write to the
// writer), so a typo between them is invisible at authoring time.
//
// The producer here also declares an output of its own, which is what defeats the
// ordinary all-or-nothing snapshot check: that check passes on own.txt alone, the
// manifest quietly omits the foreign file, and every later cache hit would replay a
// partial output set into a tree this target does not own.
func TestCrossProjectOutputMustBeProduced(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root, write := w.Root(), w.Write
	write("magusfile.buzz", "")
	write("site/magusfile.buzz", "")
	write("producer/magusfile.buzz", `import "magus";
import "fs";
import "project/../site";

export fun build(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles(["own.txt", site.file("generated.txt")]);
    fs\writeFile("own.txt", "mine");
    fs\writeFile("../site/generated.text", "typo");
}
`)

	ctx := context.Background()
	m, err := magus.Open(ctx, root, magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	err = m.Run(ctx, []types.Target{{Path: "producer", Name: "build"}})
	require.Error(t, err, "a declared cross-project output that was never written must fail the run")
	assert.ErrorIs(t, err, types.CrossOutputNotProduced)
	assert.NoFileExists(t, filepath.Join(m.Root(), "site", "generated.txt"))
}

func TestEnvDefaultRewritesTheDefaultNotTheValue(t *testing.T) {
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.String("base", "origin/main", "")
		fs.Int("max", 4, "")
		return fs
	}

	fs := newFS()
	envDefault(fs, "base", "release")
	assert.Equal(t, "release", fs.Lookup("base").Value.String())
	assert.Equal(t, "release", fs.Lookup("base").DefValue)

	// An empty value is "nothing was set in the environment", not "set it to empty".
	fs = newFS()
	envDefault(fs, "base", "")
	assert.Equal(t, "origin/main", fs.Lookup("base").DefValue)

	// An unknown flag and an unparseable value are both no-ops rather than failures:
	// the environment is not the caller's invocation, so it must not abort one.
	// Unparseable is REPORTED to the caller (see the test below) but still applies
	// nothing here; which of the two the caller does is the caller's decision.
	fs = newFS()
	envDefault(fs, "nosuchflag", "x")
	envDefault(fs, "max", "not-a-number")
	assert.Equal(t, "4", fs.Lookup("max").DefValue)
}

// The dropped Set error left the flag at its zero with nothing said. For the shard
// pair that is a wrong ANSWER, not a lost convenience: NShards 0 means "do not
// shard", so every matrix job ran the whole selection and exited 0. An unknown flag
// stays silent: that is wiring, not something a user set.
func TestEnvDefaultReportsAValueTheFlagCannotParse(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.Int("max", 4, "")

	err := envDefault(fs, "max", "not-a-number")
	require.Error(t, err, "a malformed value must not be swallowed")
	assert.Contains(t, err.Error(), "not-a-number", "the error must name the value the user set")
	assert.Contains(t, err.Error(), "max", "the error must name the flag it could not seed")
	assert.Equal(t, "4", fs.Lookup("max").DefValue, "the flag keeps its default; the caller decides what to do")

	require.NoError(t, envDefault(fs, "nosuchflag", "x"), "an unknown flag is wiring, not user input")
	require.NoError(t, envDefault(fs, "max", ""), "an unset variable is not an error")
	require.NoError(t, envDefault(fs, "max", "8"))
}

// Not parallel: the prescan binds the global flags into globalCfg.
func TestRunReadsPlan(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "bare flag", args: []string{"--stdin"}, want: true},
		{name: "single dash, beside a shard", args: []string{"-stdin", "--shard", "1"}, want: true},
		{name: "after global flags and a target", args: []string{"--dry-run", "ci:gha", "--stdin"}, want: true},
		{name: "explicit true", args: []string{"build", "--stdin=true"}, want: true},
		{name: "explicit false", args: []string{"build", "--stdin=false"}},
		{name: "a value flag's value is not the flag", args: []string{"build", "--shard", "--stdin"}},
		{name: "forwarded args are not flags", args: []string{"build", "--", "--stdin"}},
		{name: "a project named stdin", args: []string{"build", "stdin"}},
		{name: "absent", args: []string{"build", "a", "--shard", "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, runReadsPlan(tt.args))
		})
	}
}

// A run reading a plan is never forwarded, since a forward does not carry stdin, and
// takes no records from its upstream, since stdin is its plan.
func TestRunReadingAPlanStaysInThisProcess(t *testing.T) {
	assert.False(t, resolveProfile("run", []string{"--stdin"}).NeedsForward)
	assert.True(t, resolveProfile("run", []string{"--stdin"}).SpawnsWork)
	assert.False(t, tradesRecords([]string{"run", "ci:gha", "--stdin"}))
	assert.True(t, tradesRecords([]string{"run", "ci:gha"}))
}

// ci.yaml publishes the plan with two templates rendered from one saved plan. They are
// executed here, from the workflow's own text, so a template that stops matching the plan
// document fails a test rather than a pull request's plan job.
func TestCIWorkflowRendersPlanOutputsAndSummary(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yaml"))
	require.NoError(t, err)
	workflow := string(body)
	assert.NotContains(t, workflow, "ci-shard")

	render := regexp.MustCompile(`magus run --stdin --shard all --dry-run -o '([^']*)' < "\$RUNNER_TEMP/plan\.json" >> "\$(GITHUB_OUTPUT|GITHUB_STEP_SUMMARY)"`)
	found := render.FindAllStringSubmatch(workflow, -1)
	require.Len(t, found, 2, "ci.yaml's plan step no longer renders the saved plan twice; repoint this test at what does")

	out := internalci.PlanOutput{Target: "ci", Count: 1, MaxParallel: 1,
		Matrix: []internalci.PlanShard{{Shard: "1", Projects: ". docs", JobName: ". docs", Digest: "d", Duties: "notes-verify"}}}
	out.Outputs, err = internalci.PlanOutputs(out)
	require.NoError(t, err)
	out.Summary = internalci.PlanSummaryMarkdown(out)

	want := map[string]string{
		"GITHUB_OUTPUT": `matrix={"include":[{"shard":"1","projects":". docs","job_name":". docs","digest":"d","duties":"notes-verify"}]}` + "\n" +
			"count=1\nmax_parallel=1\ninherit=false\n",
		"GITHUB_STEP_SUMMARY": out.Summary,
	}
	for _, m := range found {
		opts, err := output.Parse(m[1])
		require.NoError(t, err)
		var b strings.Builder
		require.NoError(t, writeFormatted(&b, opts, out))
		assert.Equal(t, want[m[2]], b.String(), "ci.yaml's render into $%s", m[2])
	}
}

// The merge queue replays main's signed cache only for a step main's CI keyed the same
// way, and charms are key lines: a gate running `ci` against shards that ran `ci:gha`
// missed every entry main stored for an unchanged base.
// The queue's box moves HOME,
// TMPDIR and the XDG directories (see boxEnv in internal/queue), none of which may key a
// step either.
func TestQueueGateKeysLikeTheCIShards(t *testing.T) {
	root := filepath.Join("..", "..")
	absRoot, err := filepath.Abs(root)
	require.NoError(t, err)
	// testkit removes the runner's trust list; keep this checkout's tool pins
	// resolvable when ComputeTargetKey probes them.
	t.Setenv("MISE_TRUSTED_CONFIG_PATHS", absRoot)
	t.Setenv("MISE_SAFE", "1")
	// The version probe runs go under the moved HOME; go's telemetry sidecar would outlive
	// it and race the TempDir cleanup (see testkit's goEnv).
	t.Setenv("TEST_TELEMETRY_DIR", filepath.Join(os.DevNull, "telemetry"))
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yaml"))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &workflow))
	var shard string
	for _, s := range workflow.Jobs["stage-1"].Steps {
		command := s.With["command"]
		// A shard gated in the queue's box names the command it runs after `-- magus`.
		if _, inner, boxed := strings.Cut(harnessCall.ReplaceAllLiteralString(command, " -- magus "), " -- magus "); boxed && strings.HasPrefix(command, "queue gate ") {
			command = inner
		}
		if s.Uses == "./.github/actions/magus" && strings.HasPrefix(command, "run ci") {
			// Rendered as main's run renders it: main's shards are the ones whose entries
			// the queue replays. The projects are dropped, as the gate's are.
			command = regexp.MustCompile(`\$\{\{ github\.event_name != 'pull_request' && '([^']*)' \|\| '' \}\}`).ReplaceAllString(command, "$1")
			shard = regexp.MustCompile(`\$\{\{[^}]*\}\}`).ReplaceAllString(command, "")
		}
	}
	require.NotEmpty(t, shard, "ci.yaml's shards run no `run ci` command")

	// candidateMagus puts `go run ./cmd/magus` in front of the literal.
	raw, err = os.ReadFile(filepath.Join(root, "hack", "ci", "merge-queue.buzz"))
	require.NoError(t, err)
	m := regexp.MustCompile(`final GATE = candidateMagus\("([^"]+)"\);`).FindSubmatch(raw)
	require.NotNil(t, m, "hack/ci/merge-queue.buzz declares no GATE")
	gate := strings.Replace(string(m[1]), harnessRunner+"gate", "run ci:gha --no-default-charms", 1)

	cfg, err := config.LoadFile(filepath.Join(root, "magus.yaml"), false)
	require.NoError(t, err)
	t.Setenv("MAGUS_CACHE_DIR", t.TempDir())
	t.Setenv("MAGUS_CACHE_REMOTE_TRUSTED_KEYS", "")
	ctx := withSecrets(context.Background())
	w, err := magus.Open(ctx, root, magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	// Flags outside the charm set key nothing. Forwarded args key every named step
	// verbatim, so both lines must forward the same ones.
	charms := func(command string) ([]string, []string) {
		argv := strings.Fields(command)
		require.GreaterOrEqual(t, len(argv), 2, command)
		require.Equal(t, "run", argv[0], command)
		var forwarded []string
		if i := slices.Index(argv, "--"); i >= 0 {
			argv, forwarded = argv[:i], argv[i+1:]
		}
		target, err := types.ParseTarget(argv[1])
		require.NoError(t, err, command)
		require.Equal(t, types.TargetCI, target.Name, command)
		return magus.CharmsForCI(withDefaultCharms(target.Charms, cfg.DefaultCharms, slices.Contains(argv, "--no-default-charms"))), forwarded
	}
	shardCharms, shardArgs := charms(shard)
	gateCharms, _ := charms(gate)
	// The shards name ci's members, and the gate reaches them by
	// ctx.needs, which hands a member no args. Neither keys one.
	assert.Empty(t, shardArgs, "main's ci.yaml shards %q forward args to the members of ci", shard)

	// test is one of ci's stages there, keyed with the invocation's charms, and keys GOOS.
	const project = "libs/diagnostics"
	targets := []string{"ci", "test"}
	type keyed struct {
		key   string
		lines []string
	}
	keyAll := func(charms []string) []keyed {
		var out []keyed
		for _, target := range targets {
			key, lines, err := w.ComputeTargetKey(ctx, project, target, charms)
			require.NoError(t, err)
			out = append(out, keyed{key, lines})
		}
		return out
	}
	shardKeys := keyAll(shardCharms)

	home, tmp := t.TempDir(), t.TempDir()
	for name, value := range map[string]string{
		"HOME":            home,
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"TMPDIR":          tmp,
		"GOTOOLCHAIN":     "local",
	} {
		t.Setenv(name, value)
	}
	gateKeys := keyAll(gateCharms)

	for i, target := range targets {
		if assert.Equal(t, shardKeys[i].lines, gateKeys[i].lines, "%s:%s keys differently under the queue gate %q than under ci.yaml's %q",
			project, target, gate, shard) {
			assert.Equal(t, shardKeys[i].key, gateKeys[i].key)
		}
	}
}

func TestParseTarget(t *testing.T) {
	t.Parallel()
	assertTarget := func(input, wantPack, wantName string) {
		t.Run(input, func(t *testing.T) {
			pack, target := parseTarget(input)
			assert.Equal(t, wantPack, pack)
			assert.Equal(t, wantName, target)
		})
	}

	assertTarget("build", "", "build")
	assertTarget("lint", "", "lint")
	assertTarget("typescript::lint", "typescript", "lint")
	assertTarget("go::test", "go", "test")
	assertTarget("rust::build", "rust", "build")
	assertTarget("::lint", "", "lint") // empty spell — treated as no filter
}

type RunAllFlagSuite struct{ suite.Suite }

func TestRunAllFlagSuite(t *testing.T) { suite.Run(t, new(RunAllFlagSuite)) }

func (s *RunAllFlagSuite) run(args ...string) error {
	return runTarget(context.Background(), s.T().TempDir(), runConfig{}, args)
}

func (s *RunAllFlagSuite) TestASlashOperandIsRefusedNamingAll() {
	err := s.run("lint", "/")
	var refusal *rootOperandError
	s.Require().ErrorAs(err, &refusal)
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "run lint --all")
}

func (s *RunAllFlagSuite) TestASlashAmongProjectsIsRefused() {
	var refusal *rootOperandError
	s.ErrorAs(s.run("lint", "web", "/"), &refusal)
}

func (s *RunAllFlagSuite) TestAllWithProjectsIsRefused() {
	err := s.run("lint", "--all", "web")
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "takes no project arguments")
}
