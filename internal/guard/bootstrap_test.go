package guard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		// advise is true for the exemption; otherwise deny is whether the line is refused.
		advise, deny bool
		// says is a fragment the verdict text must carry.
		says string
	}{
		{name: "bootstrap", cwd: fresh, command: ward.BootstrapCommand, advise: true, says: "Go's build cache stays on"},
		{name: "bare package operand", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath cmd/magus run go-build --no-cache .", advise: true},
		{name: "trailing slash", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus/ run go-build --no-cache .", advise: true},
		{name: "subcommand -C inside the workspace", cwd: filepath.Dir(fresh), command: fmt.Sprintf("GOEXPERIMENT=jsonv2 go run -C %s -trimpath ./cmd/magus run go-build --no-cache .", filepath.Base(fresh)), advise: true, says: fresh},

		{name: "binary exists", cwd: built, command: ward.BootstrapCommand, deny: true, says: "already has a magus binary"},
		{name: "a bare link with a binary", cwd: built, command: "go build -o magus ./cmd/magus", deny: true, says: "already has a magus binary"},
		{name: "a bare link is served the bootstrap", cwd: fresh, command: "go build -o magus ./cmd/magus", deny: true, says: ward.BootstrapCommand},
		{name: "a trimmed link is served the bootstrap", cwd: fresh, command: "go build -trimpath -o magus ./cmd/magus", deny: true, says: ward.BootstrapCommand},
		{name: "without the prefix", cwd: fresh, command: "go run -trimpath ./cmd/magus run go-build --no-cache .", deny: true, says: ward.BootstrapCommand},
		{name: "another experiment", cwd: fresh, command: "GOEXPERIMENT=none go run -trimpath ./cmd/magus run go-build --no-cache .", deny: true, says: ward.BootstrapCommand},
		{name: "a second prefix", cwd: fresh, command: "CGO_ENABLED=0 " + ward.BootstrapCommand, deny: true, says: ward.BootstrapCommand},
		{name: "wrapped", cwd: fresh, command: "env " + ward.BootstrapCommand, deny: true, says: ward.BootstrapCommand},
		{name: "without --no-cache", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build .", deny: true, says: ward.BootstrapCommand},
		{name: "without -trimpath", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run ./cmd/magus run go-build --no-cache .", deny: true, says: ward.BootstrapCommand},
		{name: "another target", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run test --no-cache .", deny: true},
		{name: "another package", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus-docs run go-build --no-cache .", deny: true},
		{name: "go generate", cwd: fresh, command: "go generate ./...", deny: true, says: ward.BootstrapCommand},
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
		assert.Equal(t, ward.BootstrapCommand, v.Next[0].Run, command)
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
			Run:  ward.BootstrapCommand,
			Argv: bootstrapArgv(),
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
	"go run ./cmd/magus-utils jobschema -out internal/job/gen",
}

// A checkout that cannot load its own sources gets the relink and the generators, with or
// without a binary, and nothing else.
func TestRankOwnBuildAllowsRecoveryWhileTheWorkspaceCannotLoad(t *testing.T) {
	stale := types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "this build does not provide that name")
	for _, withBinary := range []bool{true, false} {
		root := checkoutFixture(t, ownModule, withBinary)
		for _, command := range recoveryForms {
			loads := 0
			v := judgeOwnBuildWith(loadingAs(stale, &loads), root, command)
			assert.Empty(t, v.Deny, command)
			assert.Equal(t, denyRuleRawTool, v.Rule.Name, command)
			assert.Contains(t, v.Context, "recovery allowed", command)
			assert.Equal(t, 1, loads, command)
		}
	}
}

func TestRankOwnBuildDeniesEverythingElseWhileTheWorkspaceCannotLoad(t *testing.T) {
	stale := types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "out of date")
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
		loads := 0
		v := judgeOwnBuildWith(loadingAs(stale, &loads), root, command)
		assert.Equal(t, denyRuleRawTool, v.Rule.Name, command)
		assert.NotEmpty(t, v.Deny, command)
	}
}

// Only a line that could pass pays for the workspace load.
func TestRankOwnBuildLoadsTheWorkspaceOnlyForARecoveryForm(t *testing.T) {
	root := checkoutFixture(t, ownModule, true)
	loads := 0
	deps := loadingAs(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "out of date"), &loads)
	for _, command := range []string{"go test ./...", "go vet ./...", "go run ./cmd/magus-utils cut"} {
		judgeOwnBuildWith(deps, root, command)
	}
	assert.Zero(t, loads)
}

// A workspace that loads, or fails for any reason but MGS1021, keeps every recovery form
// denied.
func TestRankOwnBuildDeniesRecoveryWhenTheWorkspaceLoads(t *testing.T) {
	built := checkoutFixture(t, ownModule, true)
	fresh := checkoutFixture(t, ownModule, false)
	for name, loadErr := range map[string]error{
		"loads":        nil,
		"syntax error": errors.New("magusfile: syntax error"),
		"other code":   types.DiagnosticErrorf(types.NoWorkspaceRoot, "no root"),
	} {
		for _, command := range recoveryForms {
			loads := 0
			v := judgeOwnBuildWith(loadingAs(loadErr, &loads), built, command)
			assert.Equal(t, denyRuleRawTool, v.Rule.Name, fmt.Sprintf("%s: %s", name, command))
			assert.NotEmpty(t, v.Deny, fmt.Sprintf("%s: %s", name, command))
			assert.NotContains(t, v.Context, "recovery allowed", fmt.Sprintf("%s: %s", name, command))

			v = judgeOwnBuildWith(loadingAs(loadErr, &loads), fresh, command)
			assert.Contains(t, v.Deny, "has no magus binary yet", fmt.Sprintf("%s without a binary: %s", name, command))
			require.Len(t, v.Next, 1, fmt.Sprintf("%s without a binary: %s", name, command))
			assert.Equal(t, ward.BootstrapCommand, v.Next[0].Run, fmt.Sprintf("%s without a binary: %s", name, command))
		}
	}
}

func TestJudgeAllowsTheRelinkWhileTheWorkspaceCannotLoad(t *testing.T) {
	testkit.Isolate(t)
	built := checkoutFixture(t, ownModule, true)
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: built})
	envelope := fmt.Sprintf("{\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Bash\",\"cwd\":\"%s\",\"tool_input\":{\"command\":\"GOEXPERIMENT=jsonv2 go build -o magus ./cmd/magus\"}}", built)
	loads := 0

	v := Judge(ctx, loadingAs(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "out of date"), &loads), Request{Input: envelope})

	assert.Equal(t, verdictWithRule("advise", string(denyRuleRawTool)), unworded(v))
	assert.Contains(t, v.Context, "cannot load its own sources")
}

// ./magus exists but predates the tree, so "already has a magus binary; rebuild with
// ./magus run go-build ." sends the reader to the one tool that cannot run. The exact
// bootstrap passes then, and nothing else does.
func TestRankOwnBuildAllowsTheBootstrapWhileTheBinaryCannotLoadTheTree(t *testing.T) {
	built := checkoutFixture(t, ownModule, true)
	loads := 0
	stale := types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, `unknown built-in rule "architecture-unbriefed"`)

	v := judgeOwnBuildWith(loadingAs(stale, &loads), built, ward.BootstrapCommand)

	assert.Empty(t, v.Deny)
	assert.Equal(t, denyRuleRawTool, v.Rule.Name)
	assert.Contains(t, v.Context, "bootstrap allowed")
	assert.Contains(t, v.Context, "cannot load it")
	assert.Equal(t, 1, loads)
}

// The allowance is the bootstrap and only the bootstrap: a missing prefix, a second prefix,
// a second command or any other go call keeps the deny it had.
func TestRankOwnBuildDeniesEverythingButTheBootstrapWhileTheBinaryCannotLoadTheTree(t *testing.T) {
	built := checkoutFixture(t, ownModule, true)
	stale := types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "out of date")
	for _, command := range []string{
		"go run -trimpath ./cmd/magus run go-build --no-cache .",
		"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build .",
		"CGO_ENABLED=0 " + ward.BootstrapCommand,
		ward.BootstrapCommand + " && go vet ./...",
		"go test ./...",
	} {
		loads := 0
		v := judgeOwnBuildWith(loadingAs(stale, &loads), built, command)
		assert.Equal(t, denyRuleRawTool, v.Rule.Name, command)
		assert.NotEmpty(t, v.Deny, command)
	}
}

// A tree the binary loads, or fails on for another reason, keeps "not a bootstrap".
func TestRankOwnBuildDeniesTheBootstrapWhenTheBinaryLoadsTheTree(t *testing.T) {
	built := checkoutFixture(t, ownModule, true)
	for name, loadErr := range map[string]error{
		"loads":        nil,
		"syntax error": errors.New("magusfile: syntax error"),
		"other code":   types.DiagnosticErrorf(types.GuardRuleMisdeclared, "name must not be empty"),
	} {
		loads := 0
		v := judgeOwnBuildWith(loadingAs(loadErr, &loads), built, ward.BootstrapCommand)
		assert.Contains(t, v.Deny, "already has a magus binary", name)
	}
}

func TestJudgeAllowsTheBootstrapWhileTheBinaryCannotLoadTheTree(t *testing.T) {
	testkit.Isolate(t)
	built := checkoutFixture(t, ownModule, true)
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: built})
	envelope := fmt.Sprintf("{\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Bash\",\"cwd\":\"%s\",\"tool_input\":{\"command\":\"%s\"}}", built, ward.BootstrapCommand)
	loads := 0

	v := Judge(ctx, loadingAs(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "out of date"), &loads), Request{Input: envelope})

	assert.Equal(t, verdictWithRule("advise", string(denyRuleRawTool)), unworded(v))
	assert.Contains(t, v.Context, "cannot load it")
}

// A ./magus older than a source it is stamped from lets the bootstrap through though it
// still loads the tree. A touched test file is not linked into it and changes nothing.
func TestRankOwnBuildAllowsTheBootstrapOverAStaleBinaryThatLoads(t *testing.T) {
	built := checkoutFixture(t, ownModule, true)
	ageBinary(t, built)
	writeSource(t, built, "internal/guard/x_test.go")
	loads := 0
	assert.Contains(t, judgeOwnBuildWith(loadingAs(nil, &loads), built, ward.BootstrapCommand).Deny, "already has a magus binary")

	writeSource(t, built, "internal/guard/x.go")
	loads = 0
	v := judgeOwnBuildWith(loadingAs(nil, &loads), built, ward.BootstrapCommand)
	assert.Empty(t, v.Deny)
	assert.Contains(t, v.Context, "older than its sources")
	assert.Zero(t, loads, "staleness is read off the files, before any load")
}

// ageBinary dates root's ./magus an hour back, so a source written now is newer.
func ageBinary(t *testing.T, root string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(root, "magus"), old, old))
}

func writeSource(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("package guard\n"), 0o644))
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
	line := job.CheckoutBootstrap(checkout)

	for _, tt := range []struct {
		name, lease, cwd, command string
		deny                      bool
	}{
		{name: "the root", cwd: root, command: line},
		{name: "the holder", lease: holder.ID, cwd: checkout, command: line},
		{name: "the holder by a relative path", lease: holder.ID, cwd: filepath.Dir(checkout), command: "./magus buzz hack/dev/bootstrap-checkout.buzz -- --checkout=" + filepath.Base(checkout)},
		{name: "another lease", lease: other.ID, cwd: root, command: line, deny: true},
		{name: "another lease by the script's absolute path", lease: other.ID, cwd: root, command: fmt.Sprintf("./magus buzz %s -- --checkout %s", filepath.Join(root, job.CheckoutBootstrapScript), checkout), deny: true},
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
			assert.Contains(t, v.Deny, "--checkout "+checkout, "names the command")
		})
	}
}
