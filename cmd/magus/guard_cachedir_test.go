package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
)

// TestNamesWorkspaceCacheDirMatchesEverySpelling walks the three halves of the match: the
// literal default name under the root, a cache dir the config or the environment relocated
// somewhere else entirely, and the name as a segment inside a word that is not a path magus
// can resolve.
func TestNamesWorkspaceCacheDirMatchesEverySpelling(t *testing.T) {
	root, moved := t.TempDir(), t.TempDir()
	at := hookActivityLocation{base: filepath.Join(root, ".magus"), workspace: root}

	for _, p := range []string{
		".magus",
		".magus/lease",
		".magus/advisories/anon.served-next",
		"./.magus/logs/abc.log",
		filepath.Join(root, ".magus", "lease"),
		// The renderings a word with a parameter expansion produces, and a relative path
		// aimed above the root: neither resolves anywhere near a cache dir.
		"/.magus/lease",
		"../../.magus/lease",
		`open('.magus/lease','a')`,
	} {
		assert.True(t, namesWorkspaceCacheDir(at, p), "%q names the cache dir", p)
	}

	relocated := hookActivityLocation{base: moved, workspace: root}
	assert.True(t, namesWorkspaceCacheDir(relocated, filepath.Join(moved, "lease")),
		"a relocated cache dir is still the cache dir")
	assert.True(t, namesWorkspaceCacheDir(relocated, ".magus/lease"),
		"the default name matches even when the resolved dir moved: a command that spells it means it")

	assert.True(t, namesWorkspaceCacheDir(hookActivityLocation{}, "/repo/.magus/lease"),
		"an absolute path is matched even where magus could not locate the workspace, which is what the literal name is for")
}

// TestNamesWorkspaceCacheDirIsSilentEverywhereElse pins the false positives. A sibling name
// matching would deny ordinary work under a rule that refuses every role.
func TestNamesWorkspaceCacheDirIsSilentEverywhereElse(t *testing.T) {
	root := t.TempDir()
	at := hookActivityLocation{base: filepath.Join(root, ".magus"), workspace: root}

	for _, p := range []string{
		"",
		".magus-notes/a.md",
		".magusfile",
		"magus/lease",
		"cmd/magus/guard_cachedir.go",
		"docs/guides/integrations/agents/guard.md",
		filepath.Join(t.TempDir(), ".magus-notes", "a.md"),
		"elsewhere/.magus-x",
	} {
		assert.False(t, namesWorkspaceCacheDir(at, p), "%q is not the cache dir", p)
	}
}

// TestDenyCacheDirCommandReadsTheParsedLine covers the command surface on its own: what
// WRITES in there, what merely reads, and the magus argv that must never match however
// much it writes.
//
// The second half of the write table is the bypass list: every row there passed under the
// four-operator redirect switch and the eight-verb writer allowlist this replaced.
func TestDenyCacheDirCommandReadsTheParsedLine(t *testing.T) {
	root := t.TempDir()
	at := hookActivityLocation{base: filepath.Join(root, ".magus"), workspace: root, dir: root}

	for _, command := range []string{
		"echo x >> .magus/advisories/anon.served-next",
		"echo x > .magus/lease",
		"rm -rf .magus",
		"rm .magus/advisories/anon.served-next",
		"sed -i 's/a/b/' .magus/lease",
		"cp foo .magus/lease",
		"mv /tmp/lease .magus/lease",
		"mkdir -p .magus/advisories",
		"touch .magus/advisories/anon.code-search",
		"truncate -s 0 .magus/lease",
		"chmod 600 .magus/lease",
		"echo x | tee .magus/lease",
		"cd /repo && rm .magus/lease",

		// `>>|` and `&>|` are zsh-only spellings the default parser never produces, so
		// they are classified by TestWritesToFileClassifiesEveryRedirectOperator and not
		// executed here.
		"echo x >| .magus/lease",
		"echo x &> .magus/lease",
		"echo x >& .magus/lease",
		"exec 3<> .magus/lease",
		"sh -c 'echo x > .magus/lease'",
		"bash -c 'rm .magus/lease'",
		"eval 'echo x > .magus/lease'",
		"sudo sh -c 'echo x >| .magus/lease'",
		"dd of=.magus/lease",
		"ln -sf /dev/null .magus/lease",
		"install -m 644 f .magus/lease",
		"rmdir .magus/advisories",
		"chown me .magus/lease",
		"find .magus -delete",
		`python3 -c "open('.magus/advisories/anon.served-next','a').write(line)"`,
		"sort -o .magus/lease f",
		"awk '{print > \".magus/lease\"}' f",
		"cd sub && rm ../.magus/lease",
	} {
		assert.NotEmpty(t, denyCacheDirCommand(at, command), "%q writes into the cache dir", command)
	}

	for _, command := range []string{
		"cat .magus/logs/x.log",
		"./magus query output outabc",
		"./magus session lease adj/x",
		"magus run go-build .",
		"magus clean --cache",
		"echo x >> .magus-notes/a.md",
		"rm -rf .magus-notes",
		"sed 's/a/b/' .magus/lease",
		"cp .magus/logs/x.log /tmp/x.log",
		`echo "rm -rf .magus"`,
		"git commit -m 'guard the cache dir'",
		"sort .magus/logs/x.log",
		"find .magus -name '*.log'",
		"grep -r lease .magus",
		"head -n 5 .magus/lease",
		"jq . .magus/advisories/anon.served-next",
	} {
		assert.Empty(t, denyCacheDirCommand(at, command), "%q does not write into the cache dir", command)
	}
}

// TestWritesToFileClassifiesEveryRedirectOperator is the exhaustiveness the two redirect
// readers lacked: `>|` alone bypassed both the cache-dir deny and the magus-output deny,
// and nothing said the switch had stopped covering the grammar.
func TestWritesToFileClassifiesEveryRedirectOperator(t *testing.T) {
	want := map[string]bool{
		">": true, ">>": true, "<>": true, ">&": true, ">|": true, ">>|": true,
		"&>": true, "&>|": true, "&>>": true, "&>>|": true,
		"<": false, "<&": false, "<<": false, "<<-": false, "<<<": false,
	}
	seen := map[string]bool{}
	for op := syntax.RdrOut; op <= syntax.AppAllClob; op++ {
		spelling := op.String()
		classified, ok := want[spelling]
		require.True(t, ok, "the shell grammar grew the redirect operator %q and nothing classified it", spelling)
		assert.Equal(t, classified, writesToFile(op), "%q", spelling)
		seen[spelling] = true
	}
	assert.Len(t, seen, len(want), "every classified operator must still exist in the grammar")
}

// TestRankCacheDirWriteOutranksEveryOtherDeny is the one ranking inversion in the guard,
// and the reason is that both verdicts refuse the same line: the in-place refusal routes
// the reader to an editor tool, which is the surface that would refuse the same bytes
// again.
func TestRankCacheDirWriteOutranksEveryOtherDeny(t *testing.T) {
	existing := commandVerdict{Deny: "sed -i is imprecise", Rule: denyRule{Name: denyRuleSedInPlace}}

	got := rankCacheDirWrite(existing, "magus cache dir")
	assert.Equal(t, denyRuleCacheDirWrite, got.Rule.Name)
	assert.Equal(t, "magus cache dir", got.Deny)

	assert.Equal(t, existing, rankCacheDirWrite(existing, ""), "without a reason the rule is inert")
}

// TestHookCmdJudgesTheCacheDirOnBothSurfaces is the hook's decision table for this rule.
// It is here rather than in TestEvaluateBashGuard because the rule is not pure: it reads
// the resolved cache location, so the table it belongs in is the one that runs hookCmd.
//
// The rows run UNBOUND, which is the contract: this is the guard's own evidence rather
// than a lane, so an orchestrator and a person in their own checkout are refused too.
func TestHookCmdJudgesTheCacheDirOnBothSurfaces(t *testing.T) {
	for name, tc := range map[string]struct {
		input string
		path  bool
		want  string
	}{
		"the lease marker":    {input: ".magus/lease", path: true, want: "deny\n"},
		"a served-next entry": {input: ".magus/advisories/anon.served-next", path: true, want: "deny\n"},
		// Spelled bare, so it resolves inside this package's own directory: a path
		// naming a directory that does not exist yet draws the new-directory advisory,
		// which is a different rule answering and would say nothing about this one.
		"a source file":           {input: "guard_cachedir.go", path: true, want: "pass\n"},
		"a sibling directory":     {input: ".magus-notes/a.md", path: true, want: "pass\n"},
		"appending to a journal":  {input: "echo x >> .magus/advisories/anon.served-next", want: "deny\n"},
		"removing the whole dir":  {input: "rm -rf .magus", want: "deny\n"},
		"an in-place marker edit": {input: "sed -i 's/a/b/' .magus/lease", want: "deny\n"},
		"copying over the marker": {input: "cp foo .magus/lease", want: "deny\n"},
		"reading a log":           {input: "cat .magus/logs/x.log", want: "pass\n"},
		"reading a captured run":  {input: "./magus query output outabc", want: "pass\n"},
		"binding through magus":   {input: "./magus session lease adj/x", want: "pass\n"},
		"a sibling name":          {input: "echo x >> .magus-notes/a.md", want: "pass\n"},
	} {
		t.Run(name, func(t *testing.T) {
			global = globalFlags{}
			t.Setenv(trail.EnvBaggage, "")
			ctx, root := fleetFixture(t)
			// The fixture's cache dir is already elsewhere, so these rows exercise the
			// literal spelling against a RELOCATED dir, which is the harder half.
			require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(""), 0o644))

			args := []string{"-o", "name"}
			if tc.path {
				args = append(args, "--path")
			}
			var out bytes.Buffer
			err := hookCmd(ctx, strings.NewReader(tc.input), &out, args)
			if tc.want == "deny\n" {
				require.Error(t, err, "a deny that exits 0 blocks nothing")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, out.String())
		})
	}
}

// TestHookCmdDeniesTheCacheDirAheadOfTheLaneItSitsIn is the rank the rule was written for:
// a worker handed a lane that covers the dir must read what the dir IS, not a verdict
// about whose lane it is.
func TestHookCmdDeniesTheCacheDirAheadOfTheLaneItSitsIn(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	lease := narrowLease()
	lease.OwnedPaths = []string{"**"}
	// Unregistered, so the lane rule has a denial of its own to be outranked BY. A lane
	// that covers the path and says nothing leaves the rank unobserved.
	lease.Registered = 0
	ctx, _ := fleetFixture(t, lease)

	var out bytes.Buffer
	err := hookCmd(ctx, strings.NewReader(".magus/lease"), &out,
		[]string{"--path", "--lease", lease.ID, "-o", "json"})
	require.Error(t, err)
	assert.Contains(t, out.String(), "magus cache dir")
	assert.Contains(t, out.String(), "magus is the only writer of it")
	assert.NotContains(t, out.String(), "registered the base it landed on",
		"the lane rules must not answer for this path")
}

// TestCacheDirDenyNamesTheVerbs pins the half of the text that does the work. A refusal
// that only says no leaves the reader to reach for the file again by another route.
func TestCacheDirDenyNamesTheVerbs(t *testing.T) {
	reason := cacheDirDenial(".magus/lease")

	assert.Contains(t, reason, "session lease", "the deny must name the verb that binds")
	assert.Contains(t, reason, "clean", "the deny must name the verb that clears outputs")
	assert.Contains(t, reason, "query output", "the deny must name how a log is read")
	assert.Contains(t, reason, "READING in there is fine")
	assert.Contains(t, reason, "lease", "the deny must say what the marker is")
	assert.Contains(t, reason, "advisories")
}
