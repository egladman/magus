package guard

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/ward"
)

// errMissingImport is what a tree reports when a magusfile imports a file that is not there.
var errMissingImport = errors.New(`magusfile: exec magusfile.buzz: [BZZ2001] buzz: import "./hack/policy/guard": module not found`)

func TestNamedImports(t *testing.T) {
	assert.Equal(t, []string{"./hack/policy/guard"}, namedImports(errMissingImport))
	assert.Equal(t, []string{"./a", "b/c"}, namedImports(errors.New(`[BZZ2001] buzz: import "./a": buzz: import "b/c": module not found`)))
	assert.Empty(t, namedImports(errMissingMember), "a missing member names no file")
	assert.Empty(t, namedImports(errors.New(`buzz: import "./x": unexpected token`)), "only BZZ2001 names a missing file")
	assert.Empty(t, namedImports(nil))
}

func TestNamesImport(t *testing.T) {
	imports := []string{"./hack/policy/guard"}
	assert.True(t, namesImport(imports, "hack/policy/guard.buzz"))
	assert.True(t, namesImport(imports, "sub/hack/policy/guard.buzz"), "the importer's directory is not in the message")
	assert.False(t, namesImport(imports, "hack/policy/guard.old.buzz"))
	assert.False(t, namesImport(imports, "xhack/policy/guard.buzz"), "the match is at a directory boundary")
	assert.False(t, namesImport(imports, "hack/policy/guard"), "an import resolves to a .buzz file")
	assert.True(t, namesImport([]string{"../lib/x"}, "lib/x.buzz"))
}

func TestRestoreTargets(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"git mv old.buzz new.buzz", []string{"new.buzz"}},
		{"git mv -f old.buzz new.buzz", nil},
		{"git mv a b c", nil},
		{"git restore a.buzz", []string{"a.buzz"}},
		{"git restore --staged --worktree a.buzz b.buzz", []string{"a.buzz", "b.buzz"}},
		{"git restore --source=HEAD~1 -- a.buzz", []string{"a.buzz"}},
		{"git restore -s HEAD~1 a.buzz", []string{"a.buzz"}},
		{"git restore -p a.buzz", nil},
		{"git checkout -- a.buzz", []string{"a.buzz"}},
		{"git checkout HEAD -- a.buzz", []string{"a.buzz"}},
		{"git checkout a.buzz", nil},
		{"git checkout -b topic -- a.buzz", nil},
		{"git reset --hard", nil},
		{"git -C ../other restore a.buzz", nil},
	} {
		assert.Equal(t, tc.want, restoreTargets(literalWords(mustSoleCall(t, tc.line).Args)), tc.line)
	}
}

func mustSoleCall(t *testing.T, line string) *syntax.CallExpr {
	t.Helper()
	call, ok := soleCall(line, DialectBash)
	if !ok {
		t.Fatalf("%q is not one command", line)
	}
	return call
}

// While a magusfile import names a file that is missing, putting that file back runs, and
// the verdict says why. Every other change stays denied, a restore that carries another
// path included.
func TestMissingImportAllowsRestoringTheNamedFile(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(errMissingImport)

	for _, line := range []string{
		"git mv hack/policy/guard.old.buzz hack/policy/guard.buzz",
		"git restore hack/policy/guard.buzz",
		"git restore --staged --worktree --source=HEAD -- hack/policy/guard.buzz",
		"git checkout -- hack/policy/guard.buzz",
		"git checkout HEAD -- hack/policy/guard.buzz",
	} {
		v := judgeUnloaded(ctx, deps, line)
		assert.Equal(t, verdictWithRule(decisionAdvise, string(denyRuleStaleBinary)), unworded(v), line)
		assert.Contains(t, v.Context, "puts back hack/policy/guard.buzz", line)
	}

	write := Judge(ctx, deps, Request{Input: filepath.Join(root, "hack", "policy", "guard.buzz"), IsPath: true, Host: "claude-code", Session: "s1"})
	assert.Equal(t, verdictWithRule(decisionAdvise, string(denyRuleStaleBinary)), unworded(write))
	assert.Contains(t, write.Context, "puts back hack/policy/guard.buzz")

	for _, line := range []string{
		"touch notes.txt",
		"git mv hack/policy/guard.buzz hack/policy/other.buzz",
		"git restore hack/policy/other.buzz",
		"git restore hack/policy/guard.buzz notes.txt",
		"git restore hack/*/guard.buzz",
		"git checkout -- hack/policy/guard.buzz && git push origin HEAD:topic",
		"git checkout main",
		"git restore ../outside/hack/policy/guard.buzz",
	} {
		v := judgeUnloaded(ctx, deps, line)
		shortDeny(t, cacheDir, v, denyRuleStaleBinary, staleOwnSay)
	}

	other := Judge(ctx, deps, Request{Input: filepath.Join(root, "main.go"), IsPath: true, Host: "claude-code", Session: "s2"})
	shortDeny(t, cacheDir, other, denyRuleStaleBinary, staleOwnSay)
}

// A load failure that names no file leaves the restore denied.
func TestRestoreIsDeniedWhenTheLoadErrorNamesNoFile(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(errMissingMember)

	shortDeny(t, cacheDir, judgeUnloaded(ctx, deps, "git restore hack/policy/guard.buzz"), denyRuleStaleBinary, staleOwnSay)
}

// The bootstrap the deny prints runs for a ./magus that cannot resolve an import, not only
// for one that reports MGS1021.
func TestBootstrapRunsWhileAnImportDoesNotResolve(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)

	v := judgeUnloaded(ctx, unloadedDepsFor(errMissingImport), ward.BootstrapCommand)
	assert.NotEqual(t, decisionDeny, v.Decision, v.Reason)
}
