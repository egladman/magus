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
		"git -C . checkout MERGE_HEAD -- a.go":                {Name: denyRuleMergeSideCheckout, Arg: "MERGE_HEAD"},
		"git -c alias.x='reset --hard' x":                     {Name: denyRuleInlineAlias, Arg: "alias.x"},
		"git -c alias.st=status st":                           {Name: denyRuleInlineAlias, Arg: "alias.st"},
		"git --config-env alias.x=CMD x":                      {Name: denyRuleInlineAlias, Arg: "alias.x"},
		"git -c include.path=/tmp/aliases x":                  {Name: denyRuleInlineAlias, Arg: "include.path"},
		"git status && git -C . -c alias.x=clean x -fd":       {Name: denyRuleInlineAlias, Arg: "alias.x"},
	} {
		v := Evaluate(strict(testDependencies()), command)
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
		v := Evaluate(strict(testDependencies()), command)
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
		v := Evaluate(strict(testDependencies()), command)
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
		v := Evaluate(strict(testDependencies()), cmd)
		assert.Equal(t, string(want), v.RuleName(), "%q", cmd)
	}
	assert.Empty(t, Evaluate(strict(testDependencies()), "git -C . stash list && (").Deny)
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
		"git --work-tree=" + wt + " clean -fd":          {Name: denyRuleWholeTree, Arg: "git clean"},
	} {
		assert.Equal(t, want, Evaluate(strict(testDependencies()), command).Rule, "%q", command)
	}
	for _, command := range []string{"git -C " + wt + " status", "git -C .. -C wt-feature log -1"} {
		v := Evaluate(strict(testDependencies()), command)
		assert.Empty(t, v.Deny, "%q", command)
		assert.Empty(t, v.Context, "%q", command)
	}

	site, ok := locatePush("git --no-pager -C .. -C wt-feature push", DialectBash, main)
	require.True(t, ok)
	assert.Equal(t, pushSite{dir: wt, relocated: true}, site)
}

// TestParseVCS pins hg's, sl's and jj's global options, which each reads anywhere on the
// line: taken out of what follows the subcommand, separate or `=`, and abbreviated only
// where hg and sl accept it.
func TestParseVCS(t *testing.T) {
	t.Parallel()
	cmd := func(sub string, rest ...string) vcsCommand {
		if rest == nil {
			rest = []string{}
		}
		return vcsCommand{sub: sub, rest: rest}
	}
	aliased := func(c vcsCommand, alias string) vcsCommand {
		c.alias = alias
		return c
	}
	for line, want := range map[string]vcsCommand{
		"hg purge":                                        cmd("purge"),
		"hg -R . purge":                                   cmd("purge"),
		"hg --repository . purge":                         cmd("purge"),
		"hg --repository=. purge":                         cmd("purge"),
		"hg --repo . purge":                               cmd("purge"),
		"hg --cwd . purge":                                cmd("purge"),
		"hg --cwd=. purge":                                cmd("purge"),
		"hg --config ui.x=y purge":                        cmd("purge"),
		"hg --config=ui.x=y purge":                        cmd("purge"),
		"hg -y -q -v --debug --traceback purge":           cmd("purge"),
		"hg --noninteractive --quiet --verbose purge":     cmd("purge"),
		"hg --time --profile --hidden --debugger purge":   cmd("purge"),
		"hg --color never --pager never purge":            cmd("purge"),
		"hg --encoding utf-8 --encodingmode strict purge": cmd("purge"),
		"hg --pag never purge":                            cmd("purge"),
		"hg --col=never purge":                            cmd("purge"),
		"hg revert -R . --all":                            cmd("revert", "--all"),
		"hg update --cwd . -C":                            cmd("update", "-C"),
		"hg revert -- -R":                                 cmd("revert", "--", "-R"),
		"hg -- purge":                                     cmd("purge"),
		"sl --cwd . purge":                                cmd("purge"),
		"sl goto --config ui.x=y --clean":                 cmd("goto", "--clean"),
		"jj -R . abandon":                                 cmd("abandon"),
		"jj --repository=. abandon":                       cmd("abandon"),
		"jj --at-op @ restore":                            cmd("restore"),
		"jj --at-operation=@ restore":                     cmd("restore"),
		"jj --ignore-working-copy --ignore-immutable --debug --quiet --no-pager abandon": cmd("abandon"),
		"jj --color never --config ui.x=y abandon":                                       cmd("abandon"),
		"jj restore -R ../x":          cmd("restore"),
		"jj restore --from @- a.go":   cmd("restore", "--from", "@-", "a.go"),
		"jj workspace --quiet forget": cmd("workspace", "forget"),
		// jj takes no abbreviation, so an unknown flag is kept as the subcommand's own.
		"jj restore --col never":            cmd("restore", "--col", "never"),
		"hg --config alias.x=purge x":       aliased(cmd("x"), "alias.x"),
		"hg --config=ALIAS.x=purge x":       aliased(cmd("x"), "ALIAS.x"),
		"hg --config-file /tmp/rc x":        aliased(cmd("x"), "--config-file"),
		"sl --config alias.x=purge x":       aliased(cmd("x"), "alias.x"),
		"sl --configfile /tmp/rc x":         aliased(cmd("x"), "--configfile"),
		"jj --config aliases.x=abandon x":   aliased(cmd("x"), "aliases.x"),
		`jj --config "aliases".x=abandon x`: aliased(cmd("x"), `"aliases".x`),
		"jj --config-toml ui.x=1 x":         aliased(cmd("x"), "--config-toml"),
		"jj --config-file /tmp/c.toml x":    aliased(cmd("x"), "--config-file"),
		// hg's section is alias, jj's is aliases: each is only the other's ordinary key.
		"hg --config aliases.x=purge status": cmd("status"),
		"jj --config alias.x=abandon log":    cmd("log"),
		"hg":                                 cmd(""),
	} {
		words := strings.Fields(line)
		assert.Equal(t, want, parseVCS(words[0], words[1:]), line)
	}
}

// TestNonGitVCSGuardReadsPastGlobalOptions is TestGitGuardReadsPastGlobalOptions for hg,
// sl and jj: every destructive form reached past a global option before the fix.
func TestNonGitVCSGuardReadsPastGlobalOptions(t *testing.T) {
	t.Parallel()
	wholeTree := func(op string) denyRule { return denyRule{Name: denyRuleWholeTree, Arg: op} }
	for command, want := range map[string]denyRule{
		"hg -R . purge":                                wholeTree("hg purge"),
		"hg --cwd . --config ui.x=y clean":             wholeTree("hg clean"),
		"hg -y --pager never revert --all":             wholeTree("hg revert --all"),
		"hg --pag never purge":                         wholeTree("hg purge"),
		"hg --repository=. update -C":                  wholeTree("hg update --clean"),
		"sl --cwd . purge":                             wholeTree("sl purge"),
		"sl --configfile=x -R . goto --clean":          {Name: denyRuleInlineAlias, Arg: "--configfile"},
		"sl -q goto --clean":                           wholeTree("sl goto --clean"),
		"jj -R . abandon":                              wholeTree("jj abandon"),
		"jj --at-op @ --ignore-working-copy abandon":   wholeTree("jj abandon"),
		"jj --config ui.x=y restore":                   wholeTree("jj restore"),
		"jj restore -R ../x":                           wholeTree("jj restore"),
		"jj --no-pager workspace forget":               {Name: denyRuleWorktreeRemove},
		"hg --config alias.x=purge x":                  {Name: denyRuleInlineAlias, Arg: "alias.x"},
		"sl --config alias.x=purge x":                  {Name: denyRuleInlineAlias, Arg: "alias.x"},
		"jj --config aliases.x=abandon x":              {Name: denyRuleInlineAlias, Arg: "aliases.x"},
		"jj --config-toml 'aliases.x=[\"abandon\"]' x": {Name: denyRuleInlineAlias, Arg: "--config-toml"},
	} {
		v := Evaluate(strict(testDependencies()), command)
		assert.Equal(t, want, v.Rule, "%q", command)
		assert.NotEmpty(t, v.Deny, "%q", command)
	}
	for _, command := range []string{
		"hg -R . status",
		"hg --cwd . log -l 3",
		"hg --config ui.x=y revert a.go",
		"sl --cwd . status",
		"sl -R . goto main",
		"jj -R . log",
		"jj --at-op @ restore a.go",
		"jj --ignore-working-copy status",
		"jj --config ui.x=y workspace list",
	} {
		v := Evaluate(strict(testDependencies()), command)
		assert.Empty(t, v.Deny, "%q", command)
		assert.Empty(t, v.Context, "%q", command)
	}
	for _, command := range []string{"hg -R . push", "sl --cwd . push --to main", "jj --at-op @ -R . git push"} {
		assert.Equal(t, string(advisoryPushGate), Evaluate(strict(testDependencies()), command).RuleName(), "%q", command)
	}
}
