package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkoutFixture writes a go.mod declaring module, and a magus binary when withBinary.
func checkoutFixture(t *testing.T, module string, withBinary bool) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n\ngo 1.26\n"), 0o644))
	if withBinary {
		require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte("binary"), 0o755))
	}
	return root
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
		{name: "bootstrap", cwd: fresh, command: bootstrapCommand, advise: true, says: "Go's build cache stays on"},
		{name: "bare package operand", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath cmd/magus run go-build --no-cache .", advise: true},
		{name: "trailing slash", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus/ run go-build --no-cache .", advise: true},
		{name: "subcommand -C inside the workspace", cwd: filepath.Dir(fresh), command: "GOEXPERIMENT=jsonv2 go run -C " + filepath.Base(fresh) + " -trimpath ./cmd/magus run go-build --no-cache .", advise: true, says: fresh},

		{name: "binary exists", cwd: built, command: bootstrapCommand, deny: true, says: "already has a magus binary"},
		{name: "a bare link with a binary", cwd: built, command: "go build -o magus ./cmd/magus", deny: true, says: "already has a magus binary"},
		{name: "a bare link is served the bootstrap", cwd: fresh, command: "go build -o magus ./cmd/magus", deny: true, says: bootstrapCommand},
		{name: "a trimmed link is served the bootstrap", cwd: fresh, command: "go build -trimpath -o magus ./cmd/magus", deny: true, says: bootstrapCommand},
		{name: "without the prefix", cwd: fresh, command: "go run -trimpath ./cmd/magus run go-build --no-cache .", deny: true, says: bootstrapCommand},
		{name: "another experiment", cwd: fresh, command: "GOEXPERIMENT=none go run -trimpath ./cmd/magus run go-build --no-cache .", deny: true, says: bootstrapCommand},
		{name: "a second prefix", cwd: fresh, command: "CGO_ENABLED=0 " + bootstrapCommand, deny: true, says: bootstrapCommand},
		{name: "wrapped", cwd: fresh, command: "env " + bootstrapCommand, deny: true, says: bootstrapCommand},
		{name: "without --no-cache", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build .", deny: true, says: bootstrapCommand},
		{name: "without -trimpath", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run ./cmd/magus run go-build --no-cache .", deny: true, says: bootstrapCommand},
		{name: "another target", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run test --no-cache .", deny: true},
		{name: "another package", cwd: fresh, command: "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus-docs run go-build --no-cache .", deny: true},
		{name: "go generate", cwd: fresh, command: "go generate ./...", deny: true, says: bootstrapCommand},
		{name: "chained", cwd: fresh, command: bootstrapCommand + " && go vet ./...", deny: true, says: "alone on its line"},
		{name: "foreign module", cwd: foreign, command: bootstrapCommand, deny: true},

		// A -C outside the workspace passes the pure rule, which has no way to know what is
		// there; a checkout of magus there is this repository's policy to judge.
		{name: "another magus checkout by -C", cwd: elsewhere, command: "go -C " + built + " test ./..."},
		{name: "a bootstrap into another checkout by -C", cwd: elsewhere, command: "GOEXPERIMENT=jsonv2 go -C " + fresh + " run -trimpath ./cmd/magus run go-build --no-cache ."},
		{name: "foreign tree by -C", cwd: elsewhere, command: "go -C " + foreign + " test ./..."},
		{name: "magus against another root", cwd: elsewhere, command: "./magus --root " + fresh + " run go-build ."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := judgeOwnBuild(tt.cwd, tt.command)
			switch {
			case tt.advise:
				assert.Empty(t, v.Deny)
				assert.Equal(t, denyRuleRawTool, v.Rule.Name)
				assert.Contains(t, v.Context, "bootstrap allowed")
				assert.Contains(t, v.Context, tt.says)
			case tt.deny:
				assert.Equal(t, denyRuleRawTool, v.Rule.Name)
				assert.Contains(t, v.Deny, tt.says)
			default:
				assert.Empty(t, v.Deny)
			}
		})
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
		assert.Equal(t, hint.Next{
			ID:   hint.DenyRemedyPrefix + string(denyRuleRawTool),
			Run:  bootstrapCommand,
			Argv: bootstrapArgv,
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
	envelope := `{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"` + fresh + `","tool_input":{"command":"` + bootstrapCommand + `"}}`

	v := Judge(ctx, strict(testDependencies()), Request{Input: envelope})

	assert.Equal(t, verdictWithRule("advise", string(denyRuleRawTool)), unworded(v))
	assert.Contains(t, v.Context, "Use ./magus from then on")
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
			assert.Equal(t, denyRuleRawTool, v.Rule.Name, name+": "+command)
			assert.NotEmpty(t, v.Deny, name+": "+command)
			assert.NotContains(t, v.Context, "recovery allowed", name+": "+command)

			v = judgeOwnBuildWith(loadingAs(loadErr, &loads), fresh, command)
			assert.Contains(t, v.Deny, bootstrapCommand, name+" without a binary: "+command)
		}
	}
}

func TestJudgeAllowsTheRelinkWhileTheWorkspaceCannotLoad(t *testing.T) {
	testkit.Isolate(t)
	built := checkoutFixture(t, ownModule, true)
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: built})
	envelope := `{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"` + built + `","tool_input":{"command":"GOEXPERIMENT=jsonv2 go build -o magus ./cmd/magus"}}`
	loads := 0

	v := Judge(ctx, loadingAs(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "out of date"), &loads), Request{Input: envelope})

	assert.Equal(t, verdictWithRule("advise", string(denyRuleRawTool)), unworded(v))
	assert.Contains(t, v.Context, "cannot load its own sources")
}
