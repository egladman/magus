package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/testkit"
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
	deps := testDependencies()
	d := effectiveDialect("")
	return rankOwnBuild(Evaluate(deps, command), ownBuildVerdict(deps, cwd, command, d))
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
		{name: "bare package operand", cwd: fresh, command: "go run -trimpath cmd/magus run go-build --no-cache .", advise: true},
		{name: "trailing slash", cwd: fresh, command: "go run -trimpath ./cmd/magus/ run go-build --no-cache .", advise: true},
		{name: "wrapped", cwd: fresh, command: "mise exec -- " + bootstrapCommand, advise: true},
		{name: "subcommand -C inside the workspace", cwd: filepath.Dir(fresh), command: "go run -C " + filepath.Base(fresh) + " -trimpath ./cmd/magus run go-build --no-cache .", advise: true, says: fresh},

		{name: "binary exists", cwd: built, command: bootstrapCommand, deny: true, says: "already has a magus binary"},
		{name: "a bare link with a binary", cwd: built, command: "go build -o magus ./cmd/magus", deny: true, says: "already has a magus binary"},
		{name: "a bare link is served the bootstrap", cwd: fresh, command: "go build -o magus ./cmd/magus", deny: true, says: bootstrapCommand},
		{name: "a trimmed link is served the bootstrap", cwd: fresh, command: "go build -trimpath -o magus ./cmd/magus", deny: true, says: bootstrapCommand},
		{name: "without --no-cache", cwd: fresh, command: "go run -trimpath ./cmd/magus run go-build .", deny: true, says: bootstrapCommand},
		{name: "without -trimpath", cwd: fresh, command: "go run ./cmd/magus run go-build --no-cache .", deny: true, says: bootstrapCommand},
		{name: "another target", cwd: fresh, command: "go run -trimpath ./cmd/magus run test --no-cache .", deny: true},
		{name: "another package", cwd: fresh, command: "go run -trimpath ./cmd/magus-ruledocs run go-build --no-cache .", deny: true},
		{name: "go generate", cwd: fresh, command: "go generate ./...", deny: true, says: bootstrapCommand},
		{name: "chained", cwd: fresh, command: bootstrapCommand + " && go vet ./...", deny: true, says: "alone on its line"},
		{name: "foreign module", cwd: foreign, command: bootstrapCommand, deny: true},

		// A -C outside the workspace passes the pure rule, which has no way to know what is
		// there; a checkout of magus there is this repository's policy to judge.
		{name: "another magus checkout by -C", cwd: elsewhere, command: "go -C " + built + " test ./..."},
		{name: "a bootstrap into another checkout by -C", cwd: elsewhere, command: "go -C " + fresh + " run -trimpath ./cmd/magus run go-build --no-cache ."},
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
		assert.Equal(t, bootstrapArgv, v.Next[0].Argv, command)
		assert.Equal(t, Evaluate(testDependencies(), command), judgeOwnBuild(built, command), command)
	}
}

// Judge reads the envelope's cwd, so a worker's fresh checkout is judged as the worker
// sees it rather than as the hook process's directory.
func TestJudgeAllowsTheBootstrapBuildAtTheEnvelopeCwd(t *testing.T) {
	testkit.Isolate(t)
	fresh := checkoutFixture(t, ownModule, false)
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: fresh})
	envelope := `{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"` + fresh + `","tool_input":{"command":"` + bootstrapCommand + `"}}`

	v := Judge(ctx, testDependencies(), Request{Input: envelope})

	assert.Equal(t, "advise", v.Decision)
	assert.Equal(t, string(denyRuleRawTool), v.Rule)
	assert.Contains(t, v.Context, "Use ./magus from then on")
}
