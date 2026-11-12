package guard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// The bootstrap the rules exempt is one environment prefix and a go command, which is what
// bootstrapParts splits it into; a ward.BootstrapCommand of another shape exempts nothing.
func TestBootstrapCommandIsOnePrefixAndAGoCommand(t *testing.T) {
	env, goArgv, ok := bootstrapParts()
	require.True(t, ok, ward.BootstrapCommand)
	assert.Equal(t, "GOEXPERIMENT=jsonv2", env)
	assert.Equal(t, "go", goArgv[0])
}

// checkoutFixture writes a go.mod declaring module, and a magus binary when withBinary.
func checkoutFixture(t *testing.T, module string, withBinary bool) string {
	t.Helper()
	w := testkit.NewWorkspace(t)
	w.Write("go.mod", fmt.Sprintf(`module %s

go 1.26
`, module))
	if withBinary {
		w.WriteExecutable("magus", "binary")
	}
	return w.Root()
}

func judgeOwnBuild(cwd, command string) ShellVerdict {
	return judgeOwnBuildWith(strict(testDependencies()), cwd, command)
}

func judgeOwnBuildWith(deps Dependencies, cwd, command string) ShellVerdict {
	d := effectiveDialect("")
	return rankOwnBuild(Evaluate(deps, command), ownBuildVerdict(context.Background(), deps, cwd, command, d))
}

// loadingAs is testDependencies with the workspace load answering err, counting each load.
func loadingAs(err error, loads *int) Dependencies {
	deps := strict(testDependencies())
	deps.Inspect = func(context.Context, string) (types.WorkspaceRepository, error) {
		*loads++
		return nil, err
	}
	return deps
}

func TestOwnModuleIsReadFromTheBinary(t *testing.T) {
	assert.Equal(t, "github.com/egladman/magus", ownModule)
}

// The exemption and every limit on it. A bootstrap row passes as a raw-tool advisory;
// every other row keeps the verdict the pure rules reached, or is denied outright.
func TestRankOwnBuild(t *testing.T) {
	fresh := checkoutFixture(t, ownModule, false)
	built := checkoutFixture(t, ownModule, true)
	foreign := checkoutFixture(t, "example.com/other", false)
	elsewhere := t.TempDir()

	tests := []struct {
		name, cwd, command string
		// advise is true for the exemption, relinks for the relink and regenerate;
		// otherwise deny is whether the line is refused.
		advise, relinks, deny bool
		// says is a fragment the verdict text must carry.
		says string
	}{
		{name: "bootstrap", cwd: fresh, command: ward.BootstrapCommand, advise: true, says: "Go's build cache stays on"},
		{name: "bare package operand", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath cmd/magus run go-build --no-cache .", advise: true},
		{name: "trailing slash", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus/ run go-build --no-cache .", advise: true},
		{name: "subcommand -C inside the workspace", cwd: filepath.Dir(fresh), command: fmt.Sprintf("GOEXPERIMENT=jsonv2 go run -C %s -trimpath ./cmd/magus run go-build --no-cache .", filepath.Base(fresh)), advise: true, says: fresh},

		{name: "binary exists", cwd: built, command: ward.BootstrapCommand, deny: true, says: "already has a magus binary"},
		{name: "a bare link with a binary", cwd: built, command: "go build -o magus ./cmd/magus", relinks: true},
		{name: "a bare link with no binary", cwd: fresh, command: "go build -o magus ./cmd/magus", relinks: true},
		{name: "a trimmed link", cwd: fresh, command: "go build -trimpath -o magus ./cmd/magus", relinks: true},
		{name: "without the prefix", cwd: fresh, command: "go run -trimpath ./cmd/magus run go-build --no-cache .", deny: true, says: "go -C " + fresh},
		{name: "another experiment", cwd: fresh, command: "GOEXPERIMENT=none go run -trimpath ./cmd/magus run go-build --no-cache .", deny: true, says: "go -C " + fresh},
		{name: "a second prefix", cwd: fresh, command: "CGO_ENABLED=0 " + ward.BootstrapCommand, deny: true, says: "go -C " + fresh},
		{name: "wrapped", cwd: fresh, command: "env " + ward.BootstrapCommand, deny: true, says: "go -C " + fresh},
		{name: "without --no-cache", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build .", deny: true, says: "go -C " + fresh},
		{name: "without -trimpath", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run ./cmd/magus run go-build --no-cache .", deny: true, says: "go -C " + fresh},
		{name: "another target", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run test --no-cache .", deny: true},
		{name: "another package", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus-docs run go-build --no-cache .", deny: true},
		{name: "go generate", cwd: fresh, command: "go generate ./...", relinks: true},
		{name: "chained", cwd: fresh, command: ward.BootstrapCommand + " && go vet ./...", deny: true, says: "alone on its line"},
		{name: "foreign module", cwd: foreign, command: ward.BootstrapCommand, deny: true},

		// A -C outside the workspace passes the pure rule, which has no way to know what is
		// there; a checkout of magus there is this repository's policy to judge.
		{name: "another magus checkout by -C", cwd: elsewhere, command: fmt.Sprintf("go -C %s test ./...", built)},
		{name: "a bootstrap into another checkout by -C", cwd: elsewhere, command: fmt.Sprintf("GOEXPERIMENT=jsonv2 go -C %s run -trimpath ./cmd/magus run go-build --no-cache .", fresh)},
		{name: "foreign tree by -C", cwd: elsewhere, command: fmt.Sprintf("go -C %s test ./...", foreign)},
		{name: "magus against another root", cwd: elsewhere, command: fmt.Sprintf("./magus --root %s run go-build .", fresh)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := judgeOwnBuild(tt.cwd, tt.command)
			switch {
			case tt.advise:
				assert.Empty(t, v.Deny)
				assert.Equal(t, denyRuleRawTool, v.Rule.Name)
				assert.Contains(t, v.Context, "bootstrap allowed")
				assert.Contains(t, fmt.Sprintf(`%s
%s`, v.Context, v.Why), tt.says)
			case tt.relinks:
				assert.Empty(t, v.Deny)
				assert.Contains(t, v.Context, "the relink and the regenerate run")
			case tt.deny:
				assert.Equal(t, denyRuleRawTool, v.Rule.Name)
				assert.Contains(t, v.Deny+hint.Render(v.Next, func(types.NextStep) string { return "" }), tt.says,
					"the verdict or the one command served with it")
			default:
				assert.Empty(t, v.Deny)
			}
		})
	}
}

// A bootstrap refused only for sharing its line names what to drop, each part with the
// operator that joins it, and serves the bootstrap alone.
func TestRankOwnBuildNamesWhatToDropBesideTheBootstrap(t *testing.T) {
	fresh := checkoutFixture(t, ownModule, false)
	for command, drop := range map[string]string{
		ward.BootstrapCommand + " | tail -5; ls -la magus": "drop `| tail -5` and `; ls -la magus`.",
		"cd x && " + ward.BootstrapCommand:                 "drop `cd x &&`.",
	} {
		v := judgeOwnBuild(fresh, command)
		assert.Equal(t, "magus workspace: the bootstrap runs only alone on its line; "+drop, v.Deny, command)
		require.Len(t, v.Next, 1, command)
		assert.Equal(t, fmt.Sprintf("GOEXPERIMENT=jsonv2 go -C %s run -trimpath ./cmd/magus run go-build --no-cache .", fresh), v.Next[0].Run, command)
	}
}

// A fresh checkout serves the bootstrap as the next command for any raw go call, and a
// checkout with a binary keeps the verdict the pure rules reached.
func TestRankOwnBuildServesTheBootstrap(t *testing.T) {
	fresh := checkoutFixture(t, ownModule, false)
	built := checkoutFixture(t, ownModule, true)
	for _, command := range []string{"go run ./cmd/magus", "go vet ./...", "go test ./..."} {
		v := judgeOwnBuild(fresh, command)
		require.Len(t, v.Next, 1, command)
		assert.Equal(t, types.NextStep{
			ID:   hint.DenyRemedyPrefix + string(denyRuleRawTool),
			Run:  fmt.Sprintf("GOEXPERIMENT=jsonv2 go -C %s run -trimpath ./cmd/magus run go-build --no-cache .", fresh),
			Argv: []string{"GOEXPERIMENT=jsonv2", "go", "-C", fresh, "run", "-trimpath", "./cmd/magus", "run", "go-build", "--no-cache", "."},
			Why:  bootstrapWhy,
		}, v.Next[0], command)
		assert.Equal(t, Evaluate(strict(testDependencies()), command), judgeOwnBuild(built, command), command)
	}
}

// Judge reads the envelope's cwd, so a worker's fresh checkout is judged as the worker
// sees it rather than as the hook process's directory.
func TestJudgeAllowsTheBootstrapBuildAtTheEnvelopeCwd(t *testing.T) {
	testkit.Isolate(t)
	fresh := checkoutFixture(t, ownModule, false)
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: fresh})
	envelope := fmt.Sprintf("{\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Bash\",\"cwd\":\"%s\",\"tool_input\":{\"command\":\"%s\"}}", fresh, ward.BootstrapCommand)

	v := Judge(ctx, strict(testDependencies()), Request{Input: envelope})

	assert.Equal(t, verdictWithRule("advise", string(denyRuleRawTool)), unworded(v))
	assert.Contains(t, v.Context, "use ./magus from then on")
}

// The forms recoversMagus admits, each alone on its line.
var recoveryForms = []string{
	"go build -o magus ./cmd/magus",
	"go build -trimpath -o magus ./cmd/magus",
	"go build -o magus cmd/magus",
	"GOEXPERIMENT=jsonv2 go build -trimpath -o magus ./cmd/magus",
	"go generate ./cmd/magus-utils",
	"go generate ./internal/spell/...",
	"go generate ./std/... ./internal/langservice",
	"GOEXPERIMENT=jsonv2 go generate ./internal/handler/mcp",
}


// The fixed recovery runs for any caller in its own checkout, with or
// without a binary, and loads nothing to decide it.
func TestRankOwnBuildAllowsTheFixedRecovery(t *testing.T) {
	for _, withBinary := range []bool{true, false} {
		root := checkoutFixture(t, ownModule, withBinary)
		for _, command := range recoveryForms {
			loads := 0
			v := judgeOwnBuildWith(loadingAs(nil, &loads), root, command)
			assert.Empty(t, v.Deny, command)
			assert.Equal(t, denyRuleRawTool, v.Rule.Name, command)
			assert.Contains(t, v.Context, "the relink and the regenerate run", command)
			assert.Zero(t, loads, command)
		}
	}
}

func TestRankOwnBuildDeniesEverythingButTheFixedRecovery(t *testing.T) {
	root := checkoutFixture(t, ownModule, true)
	for _, command := range []string{
		"go test ./...",
		"go vet ./...",
		"go build ./cmd/magus",
		"go build -o magus.new ./cmd/magus",
		"go build -o magus ./cmd/magus-docs",
		"go build -o magus -ldflags=-s ./cmd/magus",
		"go generate",
		"go generate -run bindings ./std",
		"go generate ../other",
		"go generate /tmp/elsewhere",
		"go generate net/http",
		"go generate github.com/example/pkg",
		"go generate ...",
		"go generate ./std/../../other",
		"go run ./cmd/magus-utils diffdemo",
		"go run ./cmd/magus-utils cut",
		"go run ./cmd/magus-utils release-index",
		"go run ./cmd/magus run go-build .",
		"CGO_ENABLED=1 go build -o magus ./cmd/magus",
		"GOFLAGS=-mod=mod go generate ./cmd/magus-utils",
		"GOEXPERIMENT=jsonv2 CGO_ENABLED=0 go build -o magus ./cmd/magus",
		"env GOEXPERIMENT=jsonv2 go build -o magus ./cmd/magus",
		"bash -c 'go generate ./cmd/magus-utils'",
		"go generate ./cmd/magus-utils && go vet ./...",
		"go generate ./cmd/magus-utils > generate.log",
	} {
		v := judgeOwnBuild(root, command)
		assert.Equal(t, denyRuleRawTool, v.Rule.Name, command)
		assert.NotEmpty(t, v.Deny, command)
	}
}

func TestJudgeAllowsTheRelinkForAnyCaller(t *testing.T) {
	testkit.Isolate(t)
	built := checkoutFixture(t, ownModule, true)
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: built})
	envelope := fmt.Sprintf("{\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Bash\",\"cwd\":\"%s\",\"tool_input\":{\"command\":\"%s\"}}", built, ward.RelinkAt(""))
	v := Judge(ctx, strict(testDependencies()), Request{Input: envelope})
	assert.Equal(t, verdictWithRule("advise", string(denyRuleRawTool)), unworded(v))
	assert.Contains(t, v.Context, "for every caller")

	deps := strict(testDependencies())
	deps.lease = "worker"
	assert.NotEqual(t, decisionDeny, Judge(ctx, deps, Request{Input: envelope}).Decision, "a worker")
}

// A worker relinks its own checkout only. Another checkout's ./magus
// is the other row's to replace.
func TestRelinksCheckoutNamesOnlyTheCheckoutItLinks(t *testing.T) {
	mine := checkoutFixture(t, ownModule, true)
	other := checkoutFixture(t, ownModule, true)
	d := effectiveDialect("")
	assert.Equal(t, filepath.Join(mine, "magus"), relinksCheckout(ward.RelinkAt(""), d, mine, mine))
	assert.Equal(t, filepath.Join(mine, "magus"), relinksCheckout(ward.RelinkAt(mine), d, other, mine))
	assert.Empty(t, relinksCheckout(ward.RelinkAt(other), d, mine, mine))
	assert.Empty(t, relinksCheckout(ward.RelinkAt("")+" && ls", d, mine, mine))
	assert.Empty(t, relinksCheckout("go generate ./internal/spell", d, mine, mine))
}

// A binary that exists keeps "not a bootstrap" whatever its load says:
// the relink replaces it, and the deny names that line.
func TestRankOwnBuildDeniesTheBootstrapOverABinary(t *testing.T) {
	built := checkoutFixture(t, ownModule, true)
	for name, loadErr := range map[string]error{
		"loads":        nil,
		"syntax error": errors.New("magusfile: syntax error"),
		"stale":        types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "out of date"),
	} {
		loads := 0
		v := judgeOwnBuildWith(loadingAs(loadErr, &loads), built, ward.BootstrapCommand)
		assert.Contains(t, v.Deny, "already has a magus binary", name)
		assert.Contains(t, v.Deny, ward.RelinkAt(built), name)
	}
}

func TestBootstrapRemedyNamesTheCheckout(t *testing.T) {
	fresh := filepath.Join(t.TempDir(), "landing $cache 'quoted'")
	require.NoError(t, os.Rename(checkoutFixture(t, ownModule, false), fresh))
	v := judgeOwnBuild(fresh, "go run ./cmd/magus")
	require.Len(t, v.Next, 1)
	assert.Equal(t, []string{"GOEXPERIMENT=jsonv2", "go", "-C", fresh, "run", "-trimpath", "./cmd/magus", "run", "go-build", "--no-cache", "."}, v.Next[0].Argv)
	commands, parsed := ParseCommandsDialect(v.Next[0].Run, effectiveDialect(""))
	require.True(t, parsed)
	require.Len(t, commands, 1)
	call, ok := readGoCall(commands[0])
	require.True(t, ok)
	assert.Equal(t, fresh, call.buildRoot(t.TempDir()))
	// A remedy copied into a different session still builds the named checkout.
	assert.Empty(t, judgeOwnBuild(t.TempDir(), v.Next[0].Run).Deny)
}

func TestRankOwnBuildKeepsAWorkersBootstrapDenied(t *testing.T) {
	checkout := checkoutFixture(t, ownModule, true)
	deps := strict(testDependencies())
	deps.lease = "worker"
	v := judgeOwnBuildWith(deps, checkout, ward.BootstrapCommand)
	assert.Equal(t, workerBuildVerdict, v.Deny)
}

func writeSource(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("package guard\n"), 0o644))
}

// testCheckoutHook is the checkout.bootstrap declareCheckoutHook declares.
const testCheckoutHook = "hack/dev/bootstrap-checkout.buzz"

// declareCheckoutHook writes a magus.yaml at root declaring testCheckoutHook.
func declareCheckoutHook(t *testing.T, root string) {
	t.Helper()
	body := "checkout:\n  bootstrap: " + testCheckoutHook + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(body), 0o644))
}

type CheckoutHookSuite struct {
	suite.Suite
	root string
}

func TestCheckoutHookSuite(t *testing.T) {
	suite.Run(t, new(CheckoutHookSuite))
}

func (s *CheckoutHookSuite) SetupTest() {
	s.root = s.T().TempDir()
}

func (s *CheckoutHookSuite) TestAWorkspaceDeclaringNoHookMatchesNoBootstrapLine() {
	cmds, ok := ParseCommandsDialect("./magus buzz "+testCheckoutHook+" -- --here", effectiveDialect(""))
	s.Require().True(ok)
	_, matched := checkoutBootstrapDir(cmds, s.root, checkoutHook(s.root))
	s.False(matched)
}

func (s *CheckoutHookSuite) TestTheDeclaredHookMatchesItsBootstrapLine() {
	declareCheckoutHook(s.T(), s.root)
	cmds, ok := ParseCommandsDialect("./magus buzz "+testCheckoutHook+" -- --here", effectiveDialect(""))
	s.Require().True(ok)
	dir, matched := checkoutBootstrapDir(cmds, s.root, checkoutHook(s.root))
	s.True(matched)
	s.Equal(s.root, dir)
}

func (s *CheckoutHookSuite) TestAMovedCheckoutWithNoHookAsksForAPlacement() {
	s.Equal("A row that moved the checkout it owns asks the root for a placement: no checkout.bootstrap declared.", movedCheckoutRebuild(""))
}

func (s *CheckoutHookSuite) TestAMovedCheckoutRunsTheHookHere() {
	s.Equal("A row that moved the checkout it owns finishes with `./magus buzz "+testCheckoutHook+" -- --here`.", movedCheckoutRebuild(testCheckoutHook))
}

// A checkout forked at a commit no placed binary matches gets one from the checkout
// bootstrap, which the root and the job holding that checkout run; any other lease is a
// worker building magus.
func TestRankOwnBuildLetsOnlyTheRootAndTheHolderBootstrapACheckout(t *testing.T) {
	checkout := t.TempDir()
	ctx, root := fleetFixture(t)
	holder := types.Job{ID: "pages-fix", State: types.StateRunning, Registered: 1, CheckoutRoot: checkout, Worktree: &types.JobWorktree{Root: checkout, Source: root}}
	other := types.Job{ID: "other", State: types.StateRunning, Registered: 1}
	ctx = job.WithSnapshot(ctx, job.Snapshot{Rows: []types.Job{holder, other}})
	declareCheckoutHook(t, root)
	line := job.CheckoutBootstrap(testCheckoutHook, checkout)

	for _, tt := range []struct {
		name, lease, cwd, command string
		deny                      bool
	}{
		{name: "the root", cwd: root, command: line},
		{name: "the holder", lease: holder.ID, cwd: checkout, command: line},
		{name: "the holder by a relative path", lease: holder.ID, cwd: filepath.Dir(checkout), command: "./magus buzz hack/dev/bootstrap-checkout.buzz -- --checkout=" + filepath.Base(checkout)},
		{name: "another lease", lease: other.ID, cwd: root, command: line, deny: true},
		{name: "another lease by the script's absolute path", lease: other.ID, cwd: root, command: fmt.Sprintf("./magus buzz %s -- --checkout %s", filepath.Join(root, testCheckoutHook), checkout), deny: true},
		{name: "the holder in its checkout with --here", lease: holder.ID, cwd: checkout, command: "./magus buzz hack/dev/bootstrap-checkout.buzz -- --here"},
		{name: "another lease with --here", lease: other.ID, cwd: root, command: "./magus buzz hack/dev/bootstrap-checkout.buzz -- --here", deny: true},
		{name: "another lease running another script", lease: other.ID, cwd: root, command: "./magus buzz hack/dev/ls-uncommitted.buzz"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			deps := strict(testDependencies())
			deps.lease = tt.lease
			deps.caller = job.Caller{Host: "claude", Session: "s1"}
			if tt.lease != "" {
				deps.caller.Agent = "agent-a1b2c3"
			}
			d := effectiveDialect("")
			v := rankOwnBuild(Evaluate(deps, tt.command), ownBuildVerdict(ctx, deps, tt.cwd, tt.command, d))
			if !tt.deny {
				assert.Empty(t, v.Deny)
				return
			}
			assert.Equal(t, denyRuleRawTool, v.Rule.Name)
			assert.Contains(t, v.Deny, "builds ./magus in a checkout lease other does not own")
			assert.Contains(t, v.Deny, "--checkout ", "names the command")
		})
	}
}
