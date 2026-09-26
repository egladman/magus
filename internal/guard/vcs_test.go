package guard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseGit pins each global option git reads before its subcommand, in both the
// separate and the `=` spelling where git accepts both.
func TestParseGit(t *testing.T) {
	t.Parallel()
	sub := func(at int, name string, rest ...string) gitCommand {
		if rest == nil {
			rest = []string{}
		}
		return gitCommand{at: at, sub: name, rest: rest}
	}
	with := func(g gitCommand, edit func(*gitCommand)) gitCommand {
		edit(&g)
		return g
	}
	for line, want := range map[string]gitCommand{
		"reset --hard":                               sub(0, "reset", "--hard"),
		"-C . reset --hard":                          with(sub(2, "reset", "--hard"), func(g *gitCommand) { g.dirs = []string{"."} }),
		"-C a -C b push":                             with(sub(4, "push"), func(g *gitCommand) { g.dirs = []string{"a", "b"} }),
		`-C "" stash`:                                sub(2, "stash"),
		"-c x=y stash":                               with(sub(2, "stash"), func(g *gitCommand) { g.configured = true }),
		"-c core.pager stash":                        with(sub(2, "stash"), func(g *gitCommand) { g.configured = true }),
		"--config-env x=Y add":                       with(sub(2, "add"), func(g *gitCommand) { g.configured = true }),
		"--config-env=x=Y add":                       with(sub(1, "add"), func(g *gitCommand) { g.configured = true }),
		"--git-dir .git clean":                       with(sub(2, "clean"), func(g *gitCommand) { g.opaque = true }),
		"--git-dir=.git clean":                       with(sub(1, "clean"), func(g *gitCommand) { g.opaque = true }),
		"--work-tree . clean":                        with(sub(2, "clean"), func(g *gitCommand) { g.opaque = true }),
		"--work-tree=. clean":                        with(sub(1, "clean"), func(g *gitCommand) { g.opaque = true }),
		"--git-dir=.git --work-tree=. checkout -- .": with(sub(2, "checkout", "--", "."), func(g *gitCommand) { g.opaque = true }),
		"--namespace ns push":                        sub(2, "push"),
		"--namespace=ns push":                        sub(1, "push"),
		"--attr-source HEAD restore":                 sub(2, "restore"),
		"--attr-source=HEAD restore":                 sub(1, "restore"),
		"--shallow-file f commit":                    sub(2, "commit"),
		"--super-prefix p/ commit":                   sub(2, "commit"),
		"--exec-path=/x stash":                       sub(1, "stash"),
		"--exec-path stash":                          sub(1, "stash"),
		"--list-cmds=main stash":                     sub(1, "stash"),
		"-p stash":                                   sub(1, "stash"),
		"--paginate stash":                           sub(1, "stash"),
		"-P push":                                    sub(1, "push"),
		"--no-pager stash":                           sub(1, "stash"),
		"--no-replace-objects reset --hard":          sub(1, "reset", "--hard"),
		"--literal-pathspecs checkout .":             sub(1, "checkout", "."),
		"--glob-pathspecs checkout .":                sub(1, "checkout", "."),
		"--noglob-pathspecs checkout .":              sub(1, "checkout", "."),
		"--icase-pathspecs checkout .":               sub(1, "checkout", "."),
		"--no-optional-locks status":                 sub(1, "status"),
		"--bare worktree remove x":                   sub(1, "worktree", "remove", "x"),
		"--help reset":                               sub(0, "help", "reset"),
		"-h":                                         sub(0, "help"),
		"-C . --version":                             with(sub(2, "version"), func(g *gitCommand) { g.dirs = []string{"."} }),
		"-v":                                         sub(0, "version"),
		"-c alias.x=reset x": with(sub(2, "x"), func(g *gitCommand) {
			g.configured, g.alias = true, "alias.x"
		}),
		"-c ALIAS.x=reset x": with(sub(2, "x"), func(g *gitCommand) {
			g.configured, g.alias = true, "ALIAS.x"
		}),
		"--config-env=alias.x=CMD x": with(sub(1, "x"), func(g *gitCommand) {
			g.configured, g.alias = true, "alias.x"
		}),
		"-c include.path=/tmp/c x": with(sub(2, "x"), func(g *gitCommand) {
			g.configured, g.alias = true, "include.path"
		}),
		"-c includeIf.onbranch:x.path=/tmp/c x": with(sub(2, "x"), func(g *gitCommand) {
			g.configured, g.alias = true, "includeIf.onbranch:x.path"
		}),
		// git reads -C and -c only as separate words, and refuses these before running
		// anything, so the next word is judged instead.
		"-C=x stash": sub(1, "stash"),
		"-c=x stash": sub(1, "stash"),
		// No subcommand: git prints usage, or reports the missing directory.
		"":        {at: -1},
		"-C":      {at: -1},
		"-C . -p": {at: -1, dirs: []string{"."}},
	} {
		assert.Equal(t, want, parseGit(splitLine(line)), "git %s", line)
	}
}

// splitLine splits a test line on spaces, reading `""` as the empty word.
func splitLine(line string) []string {
	words := strings.Fields(line)
	for i, w := range words {
		if w == `""` {
			words[i] = ""
		}
	}
	if words == nil {
		return []string{}
	}
	return words
}

// TestGitGuardReadsPastGlobalOptions pins that no global option in front of a subcommand
// hides it from the rule it triggers: each of these passed before parseGit, every git rule
// read Args[0] as the subcommand.
func TestGitGuardReadsPastGlobalOptions(t *testing.T) {
	t.Parallel()
	wholeTree := func(op string) denyRule { return denyRule{Name: denyRuleWholeTree, Arg: op} }
	for command, want := range map[string]denyRule{
		"git -C . reset --hard":                               wholeTree("git reset --hard"),
		"git -c x=y stash":                                    wholeTree("git stash"),
		"git --git-dir=.git --work-tree=. checkout -- .":      wholeTree("git checkout ."),
		"git --git-dir .git --work-tree . checkout -- .":      wholeTree("git checkout ."),
		"git --no-pager stash":                                wholeTree("git stash"),
		"git -p clean -fdx":                                   wholeTree("git clean"),
		"git --paginate restore .":                            wholeTree("git restore ."),
		"git --namespace=ns --literal-pathspecs reset --hard": wholeTree("git reset --hard"),
		"git --no-optional-locks --bare stash":                wholeTree("git stash"),
		"git --attr-source HEAD --icase-pathspecs checkout .": wholeTree("git checkout ."),
		"git --config-env x=Y stash":                          wholeTree("git stash"),
		"git --exec-path stash":                               wholeTree("git stash"),
		"git -C . stash pop":                                  {Name: denyRuleSharedStash, Arg: "pop"},
		"git -P add -A":                                       {Name: denyRuleStageAll},
		"git --no-replace-objects worktree remove ../wt":      {Name: denyRuleWorktreeRemove},
		"git -C . checkout MERGE_HEAD -- a.go":                {Name: denyRuleMergeSideCheckout, Arg: "MERGE_HEAD"},
		"git -c alias.x='reset --hard' x":                     {Name: denyRuleInlineAlias, Arg: "alias.x"},
		"git -c alias.st=status st":                           {Name: denyRuleInlineAlias, Arg: "alias.st"},
		"git --config-env alias.x=CMD x":                      {Name: denyRuleInlineAlias, Arg: "alias.x"},
		"git -c include.path=/tmp/aliases x":                  {Name: denyRuleInlineAlias, Arg: "include.path"},
		"git status && git -C . -c alias.x=clean x -fd":       {Name: denyRuleInlineAlias, Arg: "alias.x"},
	} {
		v := Evaluate(testDependencies(), command)
		assert.Equal(t, want, v.Rule, "%q", command)
		assert.NotEmpty(t, v.Deny, "%q", command)
	}

	for command, rule := range map[string]denyRuleName{
		"git -P push":                                 advisoryPushGate,
		"git --no-pager -C . push origin x":           advisoryPushGate,
		"git -C . checkout -- a.go":                   advisoryRevertClassify,
		"git --literal-pathspecs restore a":           advisoryRevertClassify,
		"git -c core.hooksPath=/dev/null commit -m x": denyRuleName(advisoryStageClassify),
		"git -C . add a.go":                           denyRuleName(advisoryStageClassify),
		"git --no-pager describe":                     advisoryCheckpointState,
		"git -C . rev-parse HEAD":                     advisoryCheckpointState,
	} {
		v := Evaluate(testDependencies(), command)
		assert.Empty(t, v.Deny, "%q", command)
		assert.Equal(t, string(rule), v.advisoryName(), "%q", command)
	}

	for _, command := range []string{
		"git -C . status",
		"git --no-pager log --oneline -3",
		"git -c color.ui=never diff --stat",
		"git -P stash list",
		"git -C . stash show -p",
		"git --git-dir=.git --work-tree=. status --short",
		"git -C . reset HEAD~1",
		"git -C . checkout main",
		"git --help reset",
		"git -C . clean -n",
		// Prose naming an alias is not a git option.
		"echo 'git -c alias.x=reset x'",
	} {
		v := Evaluate(testDependencies(), command)
		assert.Empty(t, v.Deny, "%q", command)
		assert.Empty(t, v.Context, "%q", command)
	}
}

// TestGitGuardFallbackReadsPastGlobalOptions is the unparsable-line half: the patterns
// read past the same options.
func TestGitGuardFallbackReadsPastGlobalOptions(t *testing.T) {
	t.Parallel()
	for cmd, want := range map[string]denyRuleName{
		"git -C . reset --hard && (":           denyRuleWholeTree,
		"git --no-pager stash && (":            denyRuleWholeTree,
		"git -c x=y checkout . && (":           denyRuleWholeTree,
		"git -P clean -fd && (":                denyRuleWholeTree,
		"git -C . add -A && (":                 denyRuleStageAll,
		"git -c alias.x='reset --hard' x && (": denyRuleInlineAlias,
		"git --config-env=alias.x=CMD x && (":  denyRuleInlineAlias,
	} {
		_, parsed := ParseCommands(cmd)
		require.False(t, parsed, "%q must be unparsable or it does not exercise the fallback", cmd)
		v := Evaluate(testDependencies(), cmd)
		assert.Equal(t, string(want), v.RuleName(), "%q", cmd)
	}
	assert.Empty(t, Evaluate(testDependencies(), "git -C . stash list && (").Deny)
}

// TestGitGuardFollowsDashCIntoAnotherCheckout pins that pointing git at a sibling
// checkout of this repository with -C changes where it acts, not whether it is judged:
// the destructive rules deny there as here, the push resolves to that checkout, and a
// read passes.
func TestGitGuardFollowsDashCIntoAnotherCheckout(t *testing.T) {
	main, wt := twoCheckouts(t)
	t.Chdir(main)

	for command, want := range map[string]denyRule{
		"git -C " + wt + " reset --hard":                {Name: denyRuleWholeTree, Arg: "git reset --hard"},
		"git -C " + wt + " checkout -- .":               {Name: denyRuleWholeTree, Arg: "git checkout ."},
		"git -C .. -C wt-feature stash":                 {Name: denyRuleWholeTree, Arg: "git stash"},
		"git -C " + wt + " checkout MERGE_HEAD -- a.go": {Name: denyRuleMergeSideCheckout, Arg: "MERGE_HEAD"},
		"git -C " + main + " worktree remove " + wt:     {Name: denyRuleWorktreeRemove},
		"git --work-tree=" + wt + " clean -fd":          {Name: denyRuleWholeTree, Arg: "git clean"},
	} {
		assert.Equal(t, want, Evaluate(testDependencies(), command).Rule, "%q", command)
	}
	for _, command := range []string{"git -C " + wt + " status", "git -C .. -C wt-feature log -1"} {
		v := Evaluate(testDependencies(), command)
		assert.Empty(t, v.Deny, "%q", command)
		assert.Empty(t, v.Context, "%q", command)
	}

	site, ok := locatePush("git --no-pager -C .. -C wt-feature push", DialectBash, main)
	require.True(t, ok)
	assert.Equal(t, pushSite{dir: wt, relocated: true}, site)
}
