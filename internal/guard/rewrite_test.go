package guard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rewriteFixture is a workspace carrying one tracked file, which is what separates a
// rewrite from a script producing new output.
func rewriteFixture(t *testing.T) location {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal/ledger"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal/ledger/store.go"), []byte("package ledger\n"), 0o644))
	return location{workspace: root, dir: root}
}

// TestDenyInterpreterRewriteReadsTheScriptHoweverItArrives pins the gap this rule closed: a
// heredoc-fed script rewrote a tracked Go file and the guard passed it, because the older
// rule demanded a SUBSTITUTION and never read a heredoc at all.
func TestDenyInterpreterRewriteReadsTheScriptHoweverItArrives(t *testing.T) {
	at := rewriteFixture(t)

	for _, command := range []string{
		// The spelling that was measured walking through.
		"python3 - <<'PY'\nopen('internal/ledger/store.go','w').write(out)\nPY",
		`python3 - <<PY
with open("internal/ledger/store.go", "w") as f:
    f.write(resolved)
PY`,
		`python3 -c "open('internal/ledger/store.go','w').write(out)"`,
		`python -c "open('internal/ledger/store.go','a').write(out)"`,
		"perl -pi -e 's/a/b/' internal/ledger/store.go",
		"perl -i -e 's/a/b/' internal/ledger/store.go",
		"ruby -i -e 'gsub' internal/ledger/store.go",
		`node -e "require('fs').writeFileSync('internal/ledger/store.go', out)"`,
		`awk '{print > "internal/ledger/store.go"}' f`,
		"cat f | python3 - <<'PY'\nopen('internal/ledger/store.go','w').write(out)\nPY",
	} {
		assert.NotEmpty(t, denyInterpreterRewrite(at, command), "%q rewrites a tracked file", command)
	}
}

// TestDenyInterpreterRewriteStaysQuiet covers what must keep working. A rule that refused
// ordinary scripting is one people switch off, and then it guards nothing.
func TestDenyInterpreterRewriteStaysQuiet(t *testing.T) {
	at := rewriteFixture(t)

	for _, command := range []string{
		// A scratch or temp destination is ordinary work.
		"python3 - <<'PY'\nopen('/tmp/out.go','w').write(x)\nPY",
		`python3 -c "open('/var/folders/ab/scratch.go','w').write(x)"`,

		// Creating a file that is not there yet is authoring, not rewriting.
		`python3 -c "open('internal/ledger/fresh.go','w').write(x)"`,

		// Reads.
		`python3 -c "print(open('internal/ledger/store.go').read())"`,
		"python3 - <<'PY'\nprint(open('internal/ledger/store.go').read())\nPY",

		// Not an interpreter: a heredoc into cat is data, and the lane rule is what judges
		// where it lands.
		"cat > internal/ledger/store.go <<'EOF'\npackage ledger\nEOF",

		// A quoted mention of the act is not the act.
		`echo "python3 -c \"open('internal/ledger/store.go','w')\""`,
	} {
		assert.Empty(t, denyInterpreterRewrite(at, command), "%q", command)
	}

	t.Run("no workspace", func(t *testing.T) {
		assert.Empty(t, denyInterpreterRewrite(location{},
			"python3 -c \"open('internal/ledger/store.go','w').write(x)\""))
	})
}

// TestRankInterpreterRewriteNeverReplacesADeny pins the ordering: `sed -i` and the
// substitute-then-write rule refuse the same act in their own words, and a reader who got
// one of those must not get this one instead.
func TestRankInterpreterRewriteNeverReplacesADeny(t *testing.T) {
	stood := BashVerdict{Deny: denySedInPlace, Rule: denyRule{Name: denyRuleSedInPlace}}
	assert.Equal(t, stood, rankInterpreterRewrite(stood, "a rewrite reason"))

	advisory := BashVerdict{Context: "something milder"}
	got := rankInterpreterRewrite(advisory, "a rewrite reason")
	assert.Equal(t, "a rewrite reason", got.Deny)
	assert.Equal(t, denyRuleInterpreterRewrite, got.Rule.Name)

	assert.Equal(t, advisory, rankInterpreterRewrite(advisory, ""))
}
