package guard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuardDeniesAFilterWithoutInput(t *testing.T) {
	for _, tc := range []struct{ command, tool string }{
		{"grep -n foo", "grep"},
		{"grep foo -", "grep"},
		{"grep -e a -e b", "grep"},
		{"grep -A 3 x", "grep"},
		{"grep -A3 -m 1 x", "grep"},
		{"grep --max-count=3 --color=always x", "grep"},
		{"grep -3 x", "grep"},
		// BSD grep, the one macOS ships, reads stdin for a recursive search with no path.
		{"grep -rn TODO", "grep"},
		{"egrep 'a|b'", "egrep"},
		{"fgrep x", "fgrep"},
		{"/usr/bin/grep foo", "grep"},
		{"rg foo -", "rg"},
		{"sed -n 1,200p", "sed"},
		{"sed -e s/a/b/ -e s/c/d/", "sed"},
		{"awk '{print $1}'", "awk"},
		{"awk -F: '{print $1}' n=1", "awk"},
		{"jq .", "jq"},
		{"jq -r .name", "jq"},
		{"jq --arg a b .", "jq"},
		{"jq --rawfile body notes.md .", "jq"},
		{"jq -f prog.jq", "jq"},
		{"head -n 5", "head"},
		{"head -5", "head"},
		{"tail -f", "tail"},
		{"sort -u -k2 -t,", "sort"},
		{"uniq -c", "uniq"},
		{"uniq - out.txt", "uniq"},
		{"wc -l", "wc"},
		{"cut -d: -f1", "cut"},
		{"tr a b", "tr"},
		{"tr -d x", "tr"},
		{"cat", "cat"},
		{"cat -", "cat"},
		{"cat notes.md -", "cat"},
		{"cat > out.txt", "cat"},
		{"tee out.txt", "tee"},
		{"xargs rm", "xargs"},
		{"xargs -n1 -I{} echo {}", "xargs"},
		{"less", "less"},
		{"more", "more"},
		// The unfed stage of a pipeline is the one that waits.
		{"grep foo | head -5", "grep"},
		{"timeout 5 grep foo", "grep"},
		{"command grep foo", "grep"},
		{"if grep -q x; then echo y; fi", "grep"},
		{"make build && sort", "sort"},
		// A substitution reads what its command was given, which here is nothing.
		{"echo $(grep foo)", "grep"},
		{`echo "$(grep foo)"`, "grep"},
		{"diff <(sort) expected.txt", "sort"},
		// A redirect on one statement feeds that statement only.
		{"grep a < f; grep b", "grep"},
	} {
		v := Evaluate(testDependencies(), tc.command)
		assert.Equal(t, denyRule{Name: denyRuleFilterWithoutInput, Arg: tc.tool}, v.Rule, tc.command)
		assert.Contains(t, v.Deny, "`"+tc.tool+"`", "the deny names the stage left without input: %s", tc.command)
	}
}

// The measured command reaches the backtick rule first at Evaluate, so the leftover grep is
// pinned here directly.
func TestFilterWithoutInputCatchesTheBacktickLeftover(t *testing.T) {
	tool, ok := unfedReader(backtickEvidence, DialectBash)
	require.True(t, ok)
	assert.Equal(t, "grep", tool)
}

func TestGuardAllowsAFedFilter(t *testing.T) {
	for _, cmd := range []string{
		"grep foo file",
		"cmd | grep foo",
		"grep foo < f",
		"grep -e a -e b file",
		"grep -A 3 x file",
		"grep -rn TODO .",
		"grep -- foo -file",
		"sed -n 1p file",
		"jq . f.json",
		"jq --arg a b . f",
		"echo x | tr a b",
		"tr a b < f",
		"head -n 5 file",
		"head -c 100 /dev/urandom",
		"awk -F: '{print $1}' /etc/passwd",
		"sort -k2 -t, file.csv",
		"cut -d: -f1 file",
		"uniq in.txt -",
		"cat <<EOF\nhi\nEOF",
		"grep foo <<< \"$x\"",
		`while read l; do echo "$l"; done < f`,
		"while read l; do grep \"$l\"; done < list",
		"{ grep a; grep b; } < f",
		"(grep a) < f",
		"echo x | { grep a; grep b; }",
		"echo x | while read l; do grep a; done",
		"echo x | tee >(grep x)",
		"cat file | xargs grep foo",
		"find . -name '*.go' -exec grep -l foo {} +",
		"echo $(date)",
		`echo "$(cat f | grep foo)"`,
		"less +G file",
		// Background jobs read /dev/null when job control is off.
		"grep foo &",
		// A function body is fed wherever it is called, which the line need not show.
		"f() { grep foo; }",
		// exec redirects stdin for every later command on the line.
		"exec < f; grep foo",
		// No program at all is a usage error, not a read.
		"grep",
		"tr",
		"jq",
		// Programs that read nothing, or may not.
		"jq -n '1+1'",
		"awk 'BEGIN { print 1 }'",
		"awk -f prog.awk",
		// ripgrep searches the working directory unless stdin is a pipe or a file.
		"rg foo",
		"rg -e foo",
		"xargs -a list.txt rm",
		"sort --files0-from=list",
		// nohup swaps a terminal stdin for an unreadable one.
		"nohup grep foo",
		// An unknown flag leaves the tool unclassified, and unclassified never fires.
		"grep --frobnicate foo",
		"sort --frobnicate",
		// An unquoted expansion may split into a file operand, and a quoted one may be a flag.
		"grep -n foo $F",
		`grep "$pat"`,
		// A line that does not parse does not run.
		"cat 'unterminated",
	} {
		assert.NotEqual(t, denyRuleFilterWithoutInput, Evaluate(testDependencies(), cmd).Rule.Name, "should not fire: %s", cmd)
	}
}

func TestFilterWithoutInputDenyNamesTheFix(t *testing.T) {
	grep := filterWithoutInputDeny("grep")
	assert.Contains(t, grep, "name a file, pipe into it, or redirect one with `<`")
	assert.Contains(t, grep, "BSD grep", "a recursive grep needs its path, and the reason is macOS")
	assert.Contains(t, grep, "hangs")

	tr := filterWithoutInputDeny("tr")
	assert.Contains(t, tr, "its operands are never input", "tr takes no file at all")
	assert.NotContains(t, tr, "name a file")

	assert.NotContains(t, filterWithoutInputDeny("sort"), "BSD grep")
	assert.LessOrEqual(t, strings.Count(grep, "\n"), 2, "a deny is three lines at most")
}

// kind reads the flag lists in map order, so a spelling in two lists of one tool would be
// classified by whichever came up first.
func TestStdinReaderFlagsAreUnambiguous(t *testing.T) {
	for name, r := range stdinReaders {
		seen := map[string]flagKind{}
		for k, spellings := range r.flags {
			for _, flag := range strings.Fields(spellings) {
				prev, dup := seen[flag]
				assert.Falsef(t, dup, "%s: %s is listed as kind %d and %d", name, flag, prev, k)
				seen[flag] = k
				assert.Truef(t, strings.HasPrefix(flag, "-"), "%s: %q is not a flag", name, flag)
				assert.Falsef(t, !strings.HasPrefix(flag, "--") && len(flag) != 2, "%s: %q is a short flag longer than one letter", name, flag)
				assert.Falsef(t, k == flagOptional && !strings.HasPrefix(flag, "--"), "%s: %q: only a long flag takes an optional value", name, flag)
			}
		}
	}
}
