package guard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/testkit"
)

const conflictedGo = "package agent\n<<<<<<< HEAD\nvar a = 1\n=======\nvar a = 2\n>>>>>>> origin/main\n"

// conflictFixture is a workspace carrying one file mid-merge and one clean tracked file.
func conflictFixture(t *testing.T) location {
	t.Helper()
	root := t.TempDir()
	placeFile(t, root, "internal/agent/catalog.go", conflictedGo)
	placeFile(t, root, "internal/agent/clean.go", "package agent\n")
	return location{workspace: root, dir: root}
}

func placeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644))
}

// TestDenyConflictRewriteCatchesTheTruncatingMerge pins the case that emptied
// internal/agent/catalog.go: a heredoc that resolved hunks and wrote the file back through
// open(path, 'w'), which truncates before the join that raised.
func TestDenyConflictRewriteCatchesTheTruncatingMerge(t *testing.T) {
	at := conflictFixture(t)
	named := "a script rewrites internal/agent/catalog.go, which holds merge conflict markers; use your editor tool on it."

	for _, command := range []string{
		"python3 - <<'EOF'\npath = 'internal/agent/catalog.go'\nout = []\nfor line in open(path):\n    out.append(pick(line))\nopen(path,'w').write(''.join(out))\nEOF",
		"python3 - <<'EOF'\nfor path in ['internal/agent/catalog.go', 'internal/agent/clean.go']:\n    s = open(path).read()\n    open(path, 'w').write(fix(s))\nEOF",
		"python3 - <<'EOF'\nfiles = ['internal/agent/catalog.go']\nfor p in files:\n    open(p, 'w').write(fix(open(p).read()))\nEOF",
		`python3 -c "from pathlib import Path; Path('internal/agent/catalog.go').write_text(x)"`,
		"perl -pi -e 's/^<<<<<<< HEAD\\n//' internal/agent/catalog.go",
		`node -e "for (const f of ['internal/agent/catalog.go']) require('fs').writeFileSync(f, out)"`,
		`./magus buzz -e 'fs\writeFile("internal/agent/catalog.go", content: out);'`,
		// The destination is unreadable, and the program names the conflicted file.
		"python3 - <<'EOF'\nimport sys\nTARGETS = {'internal/agent/catalog.go': 1}\nopen(sys.argv[1] if len(sys.argv) > 1 else next(iter(TARGETS)), 'w').write(x)\nEOF",
	} {
		assert.Equal(t, named, firstLine(denyConflictRewrite(at, command, DialectBash)), "%q", command)
	}

	t.Run("a program that parses hunks and writes where it does not say", func(t *testing.T) {
		command := "python3 - <<'EOF'\nimport subprocess\nfor p in subprocess.check_output(['git', 'diff', '--name-only', '--diff-filter=U']).split():\n" +
			"    s = open(p).read()\n    if '<<<<<<<' in s:\n        open(p, 'w').write(resolve(s))\nEOF"
		assert.Equal(t, "a script resolves conflict hunks and writes a file it does not name; use your editor tool on it.",
			firstLine(denyConflictRewrite(at, command, DialectBash)))
	})
}

// TestDenyConflictRewriteStaysQuiet: a clean tracked file is interpreter-rewrite's, and a
// read, a new file and a scratch path are nobody's.
func TestDenyConflictRewriteStaysQuiet(t *testing.T) {
	at := conflictFixture(t)
	for _, command := range []string{
		`python3 -c "open('internal/agent/clean.go','w').write(x)"`,
		"python3 - <<'EOF'\nprint(open('internal/agent/catalog.go').read().count('<<<<<<<'))\nEOF",
		"python3 - <<'EOF'\nopen('internal/agent/fresh.go','w').write('<<<<<<< HEAD\\n')\nEOF",
		"python3 - <<'EOF'\nsrc = open('internal/agent/catalog.go').read()\nopen('/tmp/scratchpad/catalog.go','w').write(src)\nEOF",
		`cd /tmp/x/scratchpad && python3 -c "open('internal/agent/catalog.go','w').write(x)"`,
		"grep -n '<<<<<<<' internal/agent/catalog.go",
		"magus buzz hack/dev/resolve-conflicts.buzz -- --file internal/agent/catalog.go --take 1=ours --apply",
	} {
		assert.Empty(t, denyConflictRewrite(at, command, DialectBash), "%q", command)
	}
	assert.Empty(t, denyConflictRewrite(location{}, `python3 -c "open('internal/agent/catalog.go','w').write(x)"`, DialectBash), "no workspace")
}

func TestConflictRewriteServesTheResolveScript(t *testing.T) {
	at := conflictFixture(t)
	placeFile(t, at.workspace, conflictScript, "")
	got := denyConflictRewrite(at, `python3 -c "open('internal/agent/catalog.go','w').write(x)"`, DialectBash)
	want := fmt.Sprintf("a script rewrites a file holding conflict markers; resolve its hunks with `%s`.", resolveCommand(at.workspace, "internal/agent/catalog.go"))
	assert.Equal(t, want, firstLine(got))
	assert.Contains(t, want, "hack/dev/resolve-conflicts.buzz -- --file internal/agent/catalog.go")
	assert.LessOrEqual(t, len([]rune(want)), 160, "the verdict fits one line")
}

// TestConflictRewriteDeniesWithoutTheWorkspaceTable is the reason the rule exists apart
// from interpreter-rewrite: with no workspace table, as when a conflicted policy does not
// load, interpreter-rewrite only advises and this still refuses.
func TestConflictRewriteDeniesWithoutTheWorkspaceTable(t *testing.T) {
	at := conflictFixture(t)
	line := `python3 -c "open('internal/agent/catalog.go','w').write(x)"`
	v := judgeShellLine(t.Context(), Dependencies{}, at, at.dir, line, DialectBash)
	assert.Equal(t, denyRuleConflictRewrite, v.Rule.Name)
	assert.NotEmpty(t, v.Deny)

	clean := judgeShellLine(t.Context(), Dependencies{}, at, at.dir, `awk '{print > "internal/agent/clean.go"}' in.txt`, DialectBash)
	assert.Equal(t, denyRuleInterpreterRewrite, clean.Rule.Name, "a clean file stays interpreter-rewrite's")
}

func TestRankConflictRewrite(t *testing.T) {
	for _, rule := range []denyRuleName{denyRuleInterpreterRewrite, denyRuleInterpreterScript} {
		got := rankConflictRewrite(ShellVerdict{Deny: "less specific", Rule: denyRule{Name: rule}}, "conflict reason")
		assert.Equal(t, denyRuleConflictRewrite, got.Rule.Name, "outranks %s", rule)
	}
	other := ShellVerdict{Deny: "whole tree", Rule: denyRule{Name: denyRuleWholeTree}}
	assert.Equal(t, other, rankConflictRewrite(other, "conflict reason"))
	assert.Equal(t, other, rankConflictRewrite(other, ""))
}

func TestHoldsConflictMarkers(t *testing.T) {
	root := t.TempDir()
	for name, tc := range map[string]struct {
		content string
		want    bool
	}{
		"a hunk":           {conflictedGo, true},
		"crlf":             {"a\r\n<<<<<<< HEAD\r\nb\r\n=======\r\nc\r\n>>>>>>> x\r\n", true},
		"open only":        {"<<<<<<< HEAD\nb\n", false},
		"quoted in code":   {"if '<<<<<<<' in s:\n    x = '>>>>>>>'\n", false},
		"eight characters": {"<<<<<<<< a\n>>>>>>>> b\n", false},
		"an ordinary file": {"package x\n", false},
	} {
		p := filepath.Join(root, strings.ReplaceAll(name, " ", "-"))
		require.NoError(t, os.WriteFile(p, []byte(tc.content), 0o644))
		assert.Equal(t, tc.want, holdsConflictMarkers(p), name)
	}
	assert.False(t, holdsConflictMarkers(filepath.Join(root, "missing")))
}

// The load error names the first import of a chain; the conflicted file is its sibling.
var errConflictedLoad = errors.New(`magusfile: exec magusfile.buzz: [BZZ2001] buzz: import "./hack/policy/guard": buzz: line 12:1: unexpected token '<<'`)

func TestConflictedPolicyFiles(t *testing.T) {
	root := t.TempDir()
	placeFile(t, root, "magusfile.buzz", "import \"./hack/policy/guard\";\n")
	placeFile(t, root, "hack/policy/guard.buzz", "import \"./pull_requests\";\n")
	placeFile(t, root, "hack/policy/pull_requests.buzz", conflictedGo)
	placeFile(t, root, "internal/agent/catalog.go", conflictedGo)

	assert.Equal(t, []string{"hack/policy/pull_requests.buzz"}, conflictedPolicyFiles(errConflictedLoad, root))
	assert.Nil(t, conflictedPolicyFiles(nil, root), "nothing failed")
	assert.Nil(t, conflictedPolicyFiles(errStaleLoad, t.TempDir()), "a load failure with no markers anywhere")

	placeFile(t, root, "magusfile.buzz", conflictedGo)
	assert.Equal(t, []string{"magusfile.buzz"}, conflictedPolicyFiles(errors.New("magusfile: exec magusfile.buzz: unexpected token"), root))
}

func TestConflictedPolicyLeadNamesTheFix(t *testing.T) {
	root := t.TempDir()
	assert.Empty(t, conflictedPolicyLead(root, nil))
	why := "\nOnly the built-in rules judge calls while these files the magusfile loads hold merge conflict markers: hack/policy/pull_requests.buzz."
	assert.Equal(t,
		"workspace guard rules are off until hack/policy/pull_requests.buzz holds no conflict markers."+why,
		conflictedPolicyLead(root, []string{"hack/policy/pull_requests.buzz"}))

	placeFile(t, root, conflictScript, "")
	got := conflictedPolicyLead(root, []string{"hack/policy/pull_requests.buzz"})
	assert.Equal(t, fmt.Sprintf("workspace guard rules are off until this file is resolved: `%s`.%s", resolveCommand(root, "hack/policy/pull_requests.buzz"), why), got)
	assert.LessOrEqual(t, len([]rune(firstLine(got))), 160, "the lead fits one line")
}

// TestConflictedPolicySaysSoOnEveryCall pins the note a merge reads while hack/policy is
// conflicted: the cause and its fix lead the first notice and every repeat, where the
// generic note says once that a rule judged nothing and then only that one still fails.
func TestConflictedPolicySaysSoOnEveryCall(t *testing.T) {
	testkit.Isolate(t)
	root, cacheDir := t.TempDir(), t.TempDir()
	placeFile(t, root, "hack/policy/guard.buzz", "")
	placeFile(t, root, "hack/policy/pull_requests.buzz", conflictedGo)
	ctx := WithLocation(t.Context(), cacheDir, root, root)
	lead := conflictedPolicyLead(root, []string{"hack/policy/pull_requests.buzz"})

	first := Judge(ctx, unloadedDependencies(errConflictedLoad), Request{Input: "ls", Host: "claude-code", Session: "s1"})
	assert.Equal(t, "advise", first.Decision)
	assert.True(t, strings.HasPrefix(first.Context, lead+"\n\nThe working tree's magus\\guard.command rule judged nothing: "), first.Context)

	repeat := Judge(ctx, unloadedDependencies(errConflictedLoad), Request{Input: "ls", Host: "claude-code", Session: "s1"})
	assert.Equal(t, lead+"\n\nOnly the built-in rules applied to this command. A workspace command rule is still failing, as told earlier in this session.", repeat.Context)
}
