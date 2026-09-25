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
		{name: "bootstrap", cwd: fresh, command: "go build -o magus ./cmd/magus", advise: true, says: "./magus run go-build ."},
		{name: "bare package operand", cwd: fresh, command: "go build -o magus cmd/magus", advise: true},
		{name: "dot-slash output", cwd: fresh, command: "go build -o ./magus ./cmd/magus/", advise: true},
		{name: "joined output flag", cwd: fresh, command: "go build -o=magus ./cmd/magus", advise: true},
		{name: "absolute output in root", cwd: fresh, command: "go build -o " + filepath.Join(fresh, "magus") + " ./cmd/magus", advise: true},
		{name: "wrapped", cwd: fresh, command: "mise exec -- go build -o magus ./cmd/magus", advise: true},
		{name: "global -C", cwd: elsewhere, command: "go -C " + fresh + " build -o magus ./cmd/magus", advise: true, says: fresh},
		{name: "subcommand -C", cwd: elsewhere, command: "go build -C " + fresh + " -o magus ./cmd/magus", advise: true, says: fresh},
		{name: "joined -C", cwd: elsewhere, command: "go build -C=" + fresh + " -o magus ./cmd/magus", advise: true},

		{name: "binary exists", cwd: built, command: "go build -o magus ./cmd/magus", deny: true, says: "already has a magus binary"},
		{name: "binary exists by -C", cwd: elsewhere, command: "go -C " + built + " build -o magus ./cmd/magus", deny: true, says: "./magus run go-build ."},
		{name: "another package", cwd: fresh, command: "go build -o magus ./cmd/magus-ruledocs", deny: true},
		{name: "two packages", cwd: fresh, command: "go build -o magus ./cmd/magus ./cmd/magus-ruledocs", deny: true},
		{name: "another output path", cwd: fresh, command: "go build -o bin/magus ./cmd/magus", deny: true},
		{name: "output outside root", cwd: fresh, command: "go build -o /tmp/magus ./cmd/magus", deny: true},
		{name: "no output", cwd: fresh, command: "go build ./cmd/magus", deny: true},
		{name: "extra flag", cwd: fresh, command: "go build -trimpath -o magus ./cmd/magus", deny: true},
		{name: "go generate", cwd: fresh, command: "go generate ./...", deny: true},
		{name: "chained", cwd: fresh, command: "go build -o magus ./cmd/magus && go vet ./...", deny: true, says: "alone on its line"},
		{name: "foreign module", cwd: foreign, command: "go build -o magus ./cmd/magus", deny: true},

		// A -C outside the workspace passes the pure rule, which has no way to know what is
		// there. A checkout of magus is covered by the same targets, so the deny holds.
		{name: "sibling checkout, global -C", cwd: elsewhere, command: "go -C " + built + " test ./...", deny: true, says: "checkout of magus itself"},
		{name: "sibling checkout, subcommand -C", cwd: elsewhere, command: "go test -C " + built + " ./...", deny: true},
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
				assert.Contains(t, v.Context, "bootstrap build allowed")
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

// go run and go install are left as the pure rules judged them: the exemption only ever
// turns a deny into an advisory for the one build, and touches no other verdict.
func TestRankOwnBuildLeavesOtherGoVerbsAlone(t *testing.T) {
	fresh := checkoutFixture(t, ownModule, false)
	for _, command := range []string{"go run ./cmd/magus", "go install ./cmd/magus"} {
		assert.Equal(t, Evaluate(testDependencies(), command), judgeOwnBuild(fresh, command), command)
	}
}

// Judge reads the envelope's cwd, so a worker's fresh checkout is judged as the worker
// sees it rather than as the hook process's directory.
func TestJudgeAllowsTheBootstrapBuildAtTheEnvelopeCwd(t *testing.T) {
	testkit.Isolate(t)
	fresh := checkoutFixture(t, ownModule, false)
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: fresh})
	envelope := `{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"` + fresh + `","tool_input":{"command":"go build -o magus ./cmd/magus"}}`

	v := Judge(ctx, testDependencies(), Request{Input: envelope})

	assert.Equal(t, "advise", v.Decision)
	assert.Equal(t, string(denyRuleRawTool), v.Rule)
	assert.Contains(t, v.Context, "./magus run go-build .")
}
