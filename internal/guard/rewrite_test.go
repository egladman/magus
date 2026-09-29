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
		// The backup-suffix spelling of in-place: -i takes "bak" as a value in flag
		// clusters, but perl/ruby fold it into one word instead.
		"perl -i.bak -pe 's/a/b/' internal/ledger/store.go",
		`node -e "require('fs').writeFileSync('internal/ledger/store.go', out)"`,
		`awk '{print > "internal/ledger/store.go"}' f`,
		// The append form of the same redirect.
		`awk '{print >> "internal/ledger/store.go"}' f`,
		"cat f | python3 - <<'PY'\nopen('internal/ledger/store.go','w').write(out)\nPY",
		// The destination named through a variable, a pathlib receiver, or argv.
		"python3 - <<'PY'\np = 'internal/ledger/store.go'\ns = open(p).read()\nopen(p, 'w').write(s)\nPY",
		`python3 -c "from pathlib import Path; Path('internal/ledger/store.go').write_text(x)"`,
		"python3 - internal/ledger/store.go <<'PY'\nimport sys\nopen(sys.argv[1], 'w').write(x)\nPY",
		// A JavaScript binding names its destination behind a declaring keyword.
		`node -e "const p = 'internal/ledger/store.go'; require('fs').writeFileSync(p, out)"`,
		// Buzz carried on the line: an -e snippet, or a heredoc on stdin.
		`./magus buzz -e 'fs\writeFile("internal/ledger/store.go", content: out);'`,
		"magus buzz - <<'BZ'\nfinal p: str = \"internal/ledger/store.go\";\nfs\\writeFileAtomic(p, content: out);\nBZ",
		"magus buzz --embedded <<'BZ'\nfs\\writeLines(\"internal/ledger/store.go\", lines: rows);\nBZ",
	} {
		assert.NotEmpty(t, denyInterpreterRewrite(at, command, DialectBash), "%q rewrites a tracked file", command)
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

		// A pure print: awk's range/comparison operators share a character with its
		// redirect operator, but neither follows a print/printf statement here, so this
		// is a read like any other. The exact false positive observed live.
		"awk 'NR>=1,NR<=20' internal/ledger/store.go",
		// A bare (no `=`) comparison, same reasoning.
		"awk '$1 > 5' internal/ledger/store.go",

		// Not an interpreter: a heredoc into cat is data, and the write-path rule is what judges
		// where it lands.
		"cat > internal/ledger/store.go <<'EOF'\npackage ledger\nEOF",

		// A quoted mention of the act is not the act.
		`echo "python3 -c \"open('internal/ledger/store.go','w')\""`,

		// A tracked path the program carries as DATA, while it writes elsewhere: to a
		// scratch file by literal or by variable, or to its own stdout.
		"python3 - <<'PY'\nimport json, subprocess\npaths = ['internal/ledger/store.go', 'CLAUDE.md']\n" +
			"job = subprocess.run(['./magus', 'describe', 'job', 'x', '-o', 'json'], capture_output=True).stdout\n" +
			"open('/private/tmp/claude-501/scratchpad/job.json', 'w').write(json.dumps({'paths': paths}))\nPY",
		"python3 - <<'PY'\nimport json, os\nSCRATCH = '/private/tmp/claude-501/scratchpad'\nrows = {'internal/ledger/store.go': 1}\n" +
			"with open(os.path.join(SCRATCH, 'rows.json'), 'w') as f:\n    f.write(json.dumps(rows))\nPY",
		"python3 - <<'PY' > /private/tmp/claude-501/scratchpad/out.json\nimport json, sys\nsys.stdout.write(json.dumps(['internal/ledger/store.go']))\nPY",

		// The same relative name, after a cd into scratch, is the scratch copy.
		`cd /tmp/x/scratchpad && python3 -c "open('internal/ledger/store.go','w').write(x)"`,
		`S=/private/tmp/c/scratchpad; cd "$S" && ./magus buzz -e 'fs\writeFile("internal/ledger/store.go", content: x);'`,

		// Buzz that appends, creates, reads, or names its file for the content judge.
		`magus buzz -e 'fs\appendFile("internal/ledger/store.go", content: x);'`,
		`magus buzz -e 'fs\writeFile("internal/ledger/fresh.go", content: x);'`,
		`magus buzz -e 'std\print(fs\readFile("internal/ledger/store.go"));'`,
		"magus buzz hack/rewrite.buzz",
	} {
		assert.Empty(t, denyInterpreterRewrite(at, command, DialectBash), "%q", command)
	}

	t.Run("no workspace", func(t *testing.T) {
		assert.Empty(t, denyInterpreterRewrite(location{},
			"python3 -c \"open('internal/ledger/store.go','w').write(x)\"", DialectBash))
	})
}

// TestRankInterpreterRewriteNeverReplacesADeny pins the ordering: `sed -i` and the
// substitute-then-write rule refuse the same act in their own words, and a reader who got
// one of those must not get this one instead.
func TestRankInterpreterRewriteNeverReplacesADeny(t *testing.T) {
	stood := ShellVerdict{Deny: denySedInPlace, Rule: denyRule{Name: denyRuleSedInPlace}}
	assert.Equal(t, stood, rankInterpreterRewrite(stood, "a rewrite reason"))

	advisory := ShellVerdict{Context: "something milder"}
	got := rankInterpreterRewrite(advisory, "a rewrite reason")
	assert.Equal(t, "a rewrite reason", got.Deny)
	assert.Equal(t, denyRuleInterpreterRewrite, got.Rule.Name)

	assert.Equal(t, advisory, rankInterpreterRewrite(advisory, ""))
}
