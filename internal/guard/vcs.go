package guard

import (
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
)

// The git rules, which are the guard's largest single policy and the one with the
// most to lose: these are the calls that destroy uncommitted work, and a concurrent
// agent's along with it. Split out of internal/guard/shell.go so the VCS reasoning can be
// read without the raw-tool and output-plumbing rules interleaved through it.

// dependencyMutations are the argv prefixes that RE-RESOLVE dependencies and
// rewrite the lockfile, keyed by program. They are what types.CharmUpdate exists
// for: `rw` grants rewriting DERIVED output, which is reproducible from a clean
// checkout, while these read a registry and yield different bytes on different
// days, which is why update is not folded into rw.
//
// A hand-kept list rather than a catalog lookup, unlike the raw-tool rule: the
// spell catalog says which op renders a command, never whether that command's
// write is reproducible, and only the second question picks the charm.
//
// Deliberately narrow, and the exclusions are the load-bearing part. A bare `npm
// install`, `npm ci` and `pnpm install --frozen-lockfile` APPLY a lockfile rather
// than re-resolve one, so they are rw work at most and firing on them would put an
// advisory on the most routine command in a JS repo. `go mod edit` writes go.mod
// without consulting a registry, for the same reason. `mise install` installs
// TOOLS, whose versions are pinned in config rather than resolved into a lockfile.
//
// Each prefix is spelled as its argv words. Written as a space-joined string it needed
// re-splitting on every call, and the bare-program case had to be encoded as an empty
// string: a sentinel indistinguishable from a typo'd entry; here it is the empty prefix
// {{}}, which is what it means.
var dependencyMutations = map[string][][]string{
	"go":          {{"get"}, {"mod", "tidy"}},
	"npm":         {{"update"}, {"up"}},
	"pnpm":        {{"add"}, {"update"}, {"up"}},
	"yarn":        {{"add"}, {"upgrade"}, {"up"}},
	"bun":         {{"add"}, {"update"}},
	"cargo":       {{"update"}, {"add"}},
	"uv":          {{"lock"}, {"add"}},
	"poetry":      {{"lock"}, {"update"}, {"add"}},
	"pip-compile": {{}},
}

// isDependencyMutation reports whether one resolved command re-resolves dependency
// state. The empty prefix matches the program on its own.
func isDependencyMutation(c hint.Invocation) bool {
	for _, want := range dependencyMutations[c.Name] {
		if len(want) == 0 {
			return true
		}
		if len(c.Args) >= len(want) && slices.Equal(c.Args[:len(want)], want) {
			return true
		}
	}
	return false
}

// gitDeny is the deny one git invocation earns, if any.
func gitDeny(c hint.Invocation) (ShellVerdict, bool) {
	if c.Name != "git" {
		return ShellVerdict{}, false
	}
	g := parseGit(c.Args)
	if g.alias != "" {
		return denyInlineAlias("git", g.alias), true
	}
	sub, rest := g.sub, g.rest
	switch sub {
	case "stash":
		// Reading a stash is safe. RESTORING one is not, which this rule used to
		// assume it was: the stash stack is per-REPOSITORY, shared by every linked
		// worktree, so `git stash pop` with no ref applies whatever is at stash@{0}
		// (routinely a stranger's work from another worktree) into your tree, and
		// drops the entry if it applies cleanly. Naming the entry after reading
		// `git stash list` is the deliberate form and stays allowed.
		// `create` writes a stash COMMIT OBJECT and returns its name, touching
		// neither the working tree nor the stash stack, so denyWholeTree was a
		// false positive on it. It falls through to the checkpoint advisory
		// below, which is what it was reaching for.
		if len(rest) > 0 && slices.Contains([]string{"list", "show", "create"}, rest[0]) {
			return ShellVerdict{}, false
		}
		if len(rest) > 1 && slices.Contains([]string{"pop", "apply", "drop", "branch"}, rest[0]) {
			return ShellVerdict{}, false // an explicit stash@{N}: the caller chose which entry
		}
		// `git stash push -- <paths>` shelves only what it names, so the whole-tree
		// reason does not apply: nothing outside those paths moves, and a concurrent
		// agent's untracked work is untouched. It is also how a workspace escapes a
		// bootstrap deadlock (shelve the one hunk an old binary rejects, build,
		// restore), which this rule was denying, putting that answer out of reach.
		// A bare `git stash push` names nothing and stashes everything, so it stays
		// denied.
		if len(rest) > 1 && rest[0] == "push" && slices.Contains(rest, "--") {
			return ShellVerdict{}, false
		}
		if len(rest) > 0 && slices.Contains([]string{"pop", "apply", "drop"}, rest[0]) {
			return denySharedStash(rest[0]), true
		}
		return denyWholeTree("git stash"), true
	// `git worktree remove` is judged in internal/guard/worktree.go, which reads the
	// worktree and the job store.
	case "reset":
		if slices.Contains(rest, "--hard") {
			return denyWholeTree("git reset --hard"), true
		}
	case "checkout":
		// Whole-tree first: it is the broader reason, and one rule owning `-- .`
		// keeps the message the same whichever revision precedes the pathspec.
		if isWholeTreePathspec(rest) {
			return denyWholeTree("git checkout ."), true
		}
		if side := mergeSideRef(rest); side != "" {
			return ShellVerdict{
				Deny: denyMergeSideCheckout(side),
				Rule: denyRule{Name: denyRuleMergeSideCheckout, Arg: side},
			}, true
		}
	case "restore":
		if isWholeTreePathspec(rest) {
			return denyWholeTree("git restore ."), true
		}
		if side := mergeSideRef(rest); side != "" {
			return ShellVerdict{
				Deny: denyMergeSideCheckout(side),
				Rule: denyRule{Name: denyRuleMergeSideCheckout, Arg: side},
			}, true
		}
	case "clean":
		if isDeletingClean(rest) {
			return denyWholeTree("git clean"), true
		}
	case "add":
		// In the DENY pass, not beside the advisory below it: a stage-everything
		// form reached second (`git restore -- x && git add -A`) lost its deny to
		// whichever advisory the first command earned, which is the ordering the
		// two-pass split exists to prevent.
		if slices.ContainsFunc(rest, isStageAllOperand) {
			return ShellVerdict{Deny: denyStageAll, Why: denyStageAllWhy, Rule: denyRule{Name: denyRuleStageAll}}, true
		}
	}
	return ShellVerdict{}, false
}

// gitGuard classifies git invocations from PARSED commands, returning the first
// verdict any of them earns. grade is the line's grading: a deny it demotes or turns
// off hands the line to the commands after it, then to the advisories.
//
// Parsed rather than pattern-matched because the unanchored form denied `git
// stash` written as PROSE - a commit message, or the magus-vcs-hygiene skill,
// whose whole subject is those commands. A false positive here was once defended
// as the safe direction; with an AST it is not a trade at all, since a quoted
// word structurally cannot be a command, and `cd /repo && git stash` still
// matches however it is reached.
func gitGuard(cmds []hint.Invocation, grade func(ShellVerdict) (ShellVerdict, bool)) (ShellVerdict, bool) {
	for _, c := range cmds {
		if v, ok := gitDeny(c); ok {
			if v, ok := grade(v); ok {
				return v, true
			}
		}
	}

	// Advisories, in a second pass so a deny anywhere in a compound command wins
	// over an advisory earlier in it.
	for _, c := range cmds {
		// Every backend's push, relocated or not, ahead of the git-only arms below: it is
		// the one advisory the push gate upgrades to an ask, so a push that slipped by here
		// would publish without the person being asked.
		if isPush(c) {
			return ShellVerdict{Context: pushGuardContext, Rule: denyRule{Name: advisoryPushGate}}, true
		}
		if c.Name != "git" {
			continue
		}
		g := parseGit(c.Args)
		sub, rest := g.sub, g.rest
		switch sub {
		case "push":
			return ShellVerdict{Context: pushGuardContext, Rule: denyRule{Name: advisoryPushGate}}, true
		case "add":
			// The stage-everything forms already denied in the first pass.
			return ShellVerdict{Context: vcsGuardContext, Kind: advisoryStageClassify}, true
		case "commit":
			return ShellVerdict{Context: vcsGuardContext, Kind: advisoryStageClassify}, true
		case "checkout":
			// A revert needs the `--` separator; without it the operand is a
			// branch, which is not this rule's business.
			if slices.Contains(rest, "--") {
				return ShellVerdict{Context: revertGuardContext, Rule: denyRule{Name: advisoryRevertClassify}}, true
			}
		case "restore":
			// `git restore` targets worktree files by definition.
			return ShellVerdict{Context: revertGuardContext, Rule: denyRule{Name: advisoryRevertClassify}}, true
		case "describe":
			// --tags and --always are the build-stamp spelling (this repository's own
			// go_build target uses both): the caller wants a version string to embed,
			// not the identity of a tree it is handing to someone. A checkpoint does not
			// replace that, so the advisory would be noise on every build.
			if slices.ContainsFunc(rest, func(a string) bool { return a == "--tags" || a == "--always" }) {
				continue
			}
			return ShellVerdict{Context: checkpointGuardContext, Rule: denyRule{Name: advisoryCheckpointState}}, true
		case "stash":
			if len(rest) > 0 && rest[0] == "create" {
				return ShellVerdict{Context: checkpointGuardContext, Rule: denyRule{Name: advisoryCheckpointState}}, true
			}
		case "rev-parse":
			if isTreeIdentityQuery(rest) {
				return ShellVerdict{Context: checkpointGuardContext, Rule: denyRule{Name: advisoryCheckpointState}}, true
			}
		}
	}
	return ShellVerdict{}, false
}

// gitCommand is a git argv split where git itself splits it: the global options git reads
// before its subcommand (git(1) OPTIONS), the subcommand, and what follows it. Every git
// rule reads the subcommand from here, so an option in front of it (`git -C . reset
// --hard`, `git --no-pager stash`) cannot hide it from one of them.
type gitCommand struct {
	// at indexes sub in the argv, -1 when the argv names no subcommand.
	at   int
	sub  string
	rest []string
	// dirs are the -C operands in order. git resolves each relative one against the
	// directory the one before it reached, so they apply in sequence.
	dirs []string
	// opaque is set by --git-dir or --work-tree, which point git at a repository by a path
	// that need not be a checkout's directory, so no location can be read off them.
	opaque bool
	// configured is set by any -c or --config-env, which can change what the subcommand
	// does: grep.patternType makes `git grep` a PCRE search.
	configured bool
	// alias is the first inline config key that can define an alias, "" for none.
	alias string
}

// gitValuedGlobals are the global options that take their value as the NEXT word. Each
// long one also accepts `--opt=value`; git reads -C and -c only in the separate form.
var gitValuedGlobals = map[string]bool{
	"-C": true, "-c": true, "--config-env": true, "--git-dir": true, "--work-tree": true,
	"--namespace": true, "--attr-source": true, "--shallow-file": true, "--super-prefix": true,
}

// parseGit reads a git argv, the words after `git`, up to its subcommand.
//
// `--help`, `-h`, `--version` and `-v` end the options and become the `help` or `version`
// command, as git rewrites them. Any other flag is skipped as one word: git refuses an
// option it does not know before running anything, so reading past one can only judge a
// command that never runs. The options that print and exit (`--exec-path` with no value,
// `--list-cmds=`, `--html-path`) are read past for the same reason, and judging the words
// after them costs a deny on a line nobody writes.
func parseGit(args []string) gitCommand {
	g := gitCommand{at: -1}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--help", "-h":
			g.at, g.sub, g.rest = i, "help", args[i+1:]
			return g
		case "--version", "-v":
			g.at, g.sub, g.rest = i, "version", args[i+1:]
			return g
		}
		if !strings.HasPrefix(a, "-") {
			g.at, g.sub, g.rest = i, a, args[i+1:]
			return g
		}
		name, value, joined := strings.Cut(a, "=")
		switch {
		case !joined && gitValuedGlobals[a]:
			if i+1 >= len(args) {
				return g
			}
			i++
			value = args[i]
		case !joined || name == "-C" || name == "-c":
			continue
		}
		switch name {
		case "-C":
			// git skips an empty -C rather than failing on it.
			if value != "" {
				g.dirs = append(g.dirs, value)
			}
		case "--git-dir", "--work-tree":
			g.opaque = true
		case "-c", "--config-env":
			g.configured = true
			if key, _, _ := strings.Cut(value, "="); g.alias == "" && definesAlias(key) {
				g.alias = key
			}
		}
	}
	return g
}

// definesAlias reports a config key that can make a git word run another command: an
// alias, or an include, which loads a file that may define one. Section names are
// case-insensitive, so `ALIAS.x` counts.
func definesAlias(key string) bool {
	section, _, _ := strings.Cut(strings.ToLower(key), ".")
	return section == "alias" || section == "include" || section == "includeif"
}

// denyInlineAlias refuses a prog line that defines an alias inline, key naming the config
// key or option that does.
//
// A deny rather than judging the line as every destructive verb at once: the arguments
// those rules read (--hard, the pathspec, --all) come from the alias body too, and a
// config file or git's `--config-env` keeps that body off the line altogether. Spelling
// the expansion out loses nothing.
func denyInlineAlias(prog, key string) ShellVerdict {
	return ShellVerdict{
		Deny: "`" + key + "` defines an inline alias that hides which " + prog + " command runs; spell that command out.",
		Why:  "`" + key + "` on this line can define what a " + prog + " word runs, so no " + prog + " rule can read which command it is: one that discards work or pushes would pass unjudged.",
		Rule: denyRule{Name: denyRuleInlineAlias, Arg: key},
	}
}

// vcsCommand is an hg, sl or jj argv split at its subcommand. Unlike git, each reads its
// global options anywhere on the line, so rest is what follows the subcommand with every
// known global option and its value taken out.
type vcsCommand struct {
	sub  string
	rest []string
	// alias is the first config key or option that can define an alias, "" for none.
	alias string
}

// vcsGlobals are, per backend other than git, the global options each documents (`hg help
// -v`, `sl help -v`, `jj help`), true for one taking a value. A value follows as the next
// word or after `=`. git's are gitValuedGlobals.
var vcsGlobals = map[string]map[string]bool{
	"hg": hgGlobals,
	"sl": func() map[string]bool {
		sl := maps.Clone(hgGlobals)
		sl["--configfile"] = true
		return sl
	}(),
	"jj": {
		"-R": true, "--repository": true, "--at-operation": true, "--at-op": true,
		"--color": true, "--config": true, "--config-toml": true, "--config-file": true,
		"--ignore-working-copy": false, "--ignore-immutable": false, "--debug": false,
		"--quiet": false, "--no-pager": false,
	},
}

// hgGlobals are Mercurial's; Sapling adds --configfile. `--repo` is the one abbreviation
// hg accepts for --repository ahead of the others.
var hgGlobals = map[string]bool{
	"-R": true, "--repository": true, "--repo": true, "--cwd": true, "--config": true,
	"--config-file": true, "--color": true, "--encoding": true, "--encodingmode": true,
	"--pager": true,
	"-y":      false, "--noninteractive": false, "-q": false, "--quiet": false, "-v": false,
	"--verbose": false, "--debug": false, "--debugger": false, "--traceback": false,
	"--time": false, "--profile": false, "--hidden": false,
}

// parseVCS reads an hg, sl or jj argv: its first positional word is the subcommand, and
// the known global options are taken out wherever they sit. After `--` every word is
// positional. An unknown flag is kept as one word, since it may be the subcommand's own.
//
// hg and sl also accept a unique prefix of a long option. One is expanded only ahead of
// the subcommand, where nothing but a global can stand, so a subcommand's own flag that
// happens to prefix a global is never taken out.
func parseVCS(prog string, args []string) vcsCommand {
	opts := vcsGlobals[prog]
	var v vcsCommand
	rest := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			tail := args[i:]
			if v.sub == "" && len(tail) > 1 {
				v.sub, tail = tail[1], tail[2:]
			}
			rest = append(rest, tail...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			if v.sub == "" {
				v.sub = a
			} else {
				rest = append(rest, a)
			}
			continue
		}
		name, value, joined := strings.Cut(a, "=")
		valued, known := opts[name]
		if !known && v.sub == "" && prog != "jj" {
			name, valued, known = abbreviatedGlobal(opts, name)
		}
		if !known {
			if v.sub != "" {
				rest = append(rest, a)
			}
			continue
		}
		if valued && !joined {
			if i+1 >= len(args) {
				break
			}
			i++
			value = args[i]
		}
		if v.alias == "" {
			v.alias = aliasOption(prog, name, value)
		}
	}
	v.rest = rest
	return v
}

// abbreviatedGlobal expands name when it is a unique prefix of one long global option.
func abbreviatedGlobal(opts map[string]bool, name string) (string, bool, bool) {
	if !strings.HasPrefix(name, "--") || len(name) < 3 {
		return name, false, false
	}
	match := ""
	for opt := range opts {
		if strings.HasPrefix(opt, name) {
			if match != "" {
				return name, false, false
			}
			match = opt
		}
	}
	if match == "" {
		return name, false, false
	}
	return match, opts[match], true
}

// aliasOption reports what in one global option can define an alias for prog, or "": a
// `--config` key in the alias section (hg's and sl's `alias`, jj's `aliases`), or an
// option loading config from a file or a TOML string, which can hold one unseen.
func aliasOption(prog, name, value string) string {
	switch name {
	case "--config":
		key, _, _ := strings.Cut(value, "=")
		section, _, _ := strings.Cut(strings.ToLower(key), ".")
		want := "alias"
		if prog == "jj" {
			want = "aliases"
		}
		if strings.Trim(section, `"'`) == want {
			return key
		}
	case "--config-toml", "--config-file", "--configfile":
		return name
	}
	return ""
}

// aliasFlag spells an alias source as the option that set it.
func aliasFlag(alias string) string {
	if strings.HasPrefix(alias, "-") {
		return alias
	}
	return "--config " + alias
}

// isTreeIdentityQuery reports whether a `git rev-parse` invocation is asking WHICH
// REVISION this is, rather than one of the many repository-layout questions the
// same subcommand answers (`--show-toplevel`, `--git-dir`, `--is-inside-work-tree`).
//
// Two conditions, and both are needed. A HEAD-ish operand excludes the layout
// queries, which take no revision at all. `--abbrev-ref` is then excluded
// explicitly: it takes HEAD and answers with the BRANCH NAME, which a checkpoint
// does not replace.
//
// HEAD-ish is HEAD itself plus the forms that navigate from it (HEAD~2, HEAD^,
// HEAD@{1}), and NOT every word starting with those four letters: a branch called
// HEADLESS_BRANCH is an ordinary revision nobody is asking the identity of.
func isTreeIdentityQuery(args []string) bool {
	if slices.Contains(args, "--abbrev-ref") {
		return false
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if a == "@" || a == "HEAD" || strings.HasPrefix(a, "HEAD~") ||
			strings.HasPrefix(a, "HEAD^") || strings.HasPrefix(a, "HEAD@{") {
			return true
		}
	}
	return false
}

// gitGuardFallback applies the legacy regexes, and runs ONLY when the line does
// not parse. Its false positives on prose are the reason gitGuard exists, so it
// is confined to the case where there is no AST to consult, and there, an
// over-eager deny really is the safe direction, because these rules guard work
// that cannot be recovered.
//
// grade is the line's grading, as gitGuard takes it.
func gitGuardFallback(command string, grade func(ShellVerdict) (ShellVerdict, bool)) (ShellVerdict, bool) {
	var denies []ShellVerdict
	if m := inlineAliasRe.FindStringSubmatch(command); m != nil {
		denies = append(denies, denyInlineAlias("git", m[1]))
	}
	for _, wt := range []struct {
		matched bool
		op      string
	}{
		{stashRe.MatchString(command) && !stashSafeRe.MatchString(command), "git stash"},
		{resetRe.MatchString(command), "git reset --hard"},
		{checkoutRe.MatchString(command), "git checkout ."},
		{restoreRe.MatchString(command), "git restore ."},
		{cleanRe.MatchString(command), "git clean"},
	} {
		if wt.matched {
			denies = append(denies, denyWholeTree(wt.op))
		}
	}
	// Above the push ADVISORY, which used to answer first: `git add -A && git push` on an
	// unparsable line got a reminder instead of the deny, in the one place the file's own
	// invariant says an over-eager deny is the safe direction.
	if stageAllRe.MatchString(command) {
		denies = append(denies, ShellVerdict{Deny: denyStageAll, Why: denyStageAllWhy, Rule: denyRule{Name: denyRuleStageAll}})
	}
	for _, v := range denies {
		if v, ok := grade(v); ok {
			return v, true
		}
	}
	switch {
	case pushRe.MatchString(command):
		return ShellVerdict{Context: pushGuardContext, Rule: denyRule{Name: advisoryPushGate}}, true
	case stageRe.MatchString(command):
		return ShellVerdict{Context: vcsGuardContext, Kind: advisoryStageClassify}, true
	case scopedRevertRe.MatchString(command):
		return ShellVerdict{Context: revertGuardContext, Rule: denyRule{Name: advisoryRevertClassify}}, true
	}
	return ShellVerdict{}, false
}

// isWholeTreePathspec reports the `.` pathspec forms, with or without the `--`
// separator, and nothing narrower.
//
// Not every operand is a pathspec, which is what the earlier "first non-flag word"
// reading missed: `git checkout HEAD -- .` names a tree-ish first and `git restore
// --source HEAD .` passes one as a flag value, so both compared a REVISION against "."
// and fell through to the advisory. Everything after `--` is a pathspec by definition;
// without the separator a bare `.` is one wherever it sits, since no revision is spelled
// that way.
// mergeSideRefs are the pseudo-refs that name ONE SIDE of an operation in progress.
// MERGE_HEAD is the incoming side, ORIG_HEAD the position before it started, and
// CHERRY_PICK_HEAD and REVERT_HEAD are the same shape for their own operations.
var mergeSideRefs = []string{"MERGE_HEAD", "ORIG_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"}

// mergeSideRef returns the side-naming ref a checkout or restore is pulling a path out of,
// or "" when it names none.
//
// It requires a pathspec, because that is what separates the two commands. `git checkout
// MERGE_HEAD` moves HEAD and touches no file; `git checkout MERGE_HEAD -- <path>`
// OVERWRITES that path with one side's copy, discarding both the other side's changes and
// any merge already computed for it.
func mergeSideRef(args []string) string {
	if !slices.Contains(args, "--") {
		return ""
	}
	for _, a := range args {
		if a == "--" {
			return ""
		}
		if ref, ok := strings.CutPrefix(a, "--source="); ok {
			if slices.Contains(mergeSideRefs, ref) {
				return ref
			}
			continue
		}
		if slices.Contains(mergeSideRefs, a) {
			return a
		}
	}
	return ""
}

// denyMergeSideCheckout explains why restoring a path from one side of a merge is refused.
func denyMergeSideCheckout(ref string) string {
	return "restoring a path from " + ref + " discards the other side and the merge computed for it; for a generated file run `" + hint.VCSResolve.String() + "`.\n" +
		"It overwrites the path with ONE side, discarding the other side's changes and any merge already computed for that file.\n" +
		"It reads like \"undo my edit to this file\" and is not: during a merge the working-tree " +
		"copy IS the merge, and this replaces it wholesale.\n" +
		"Measured here: `git checkout MERGE_HEAD -- magusfile.buzz` during a conflict resolution " +
		"silently dropped the branch's own half of a merged feature. Nothing failed, the gate " +
		"stayed green, and the feature could not fire until someone read the code days later.\n" +
		"What to do instead: for a generated file, `" + hint.VCSResolve.String() + "` settles every " +
		"conflicted one by regenerating. To take one side deliberately, say which - `git checkout " +
		"--ours` or `--theirs` -- <path>`. To keep the merged result, it is already in the file; " +
		"copy it aside before doing anything else."
}

func isWholeTreePathspec(args []string) bool {
	if i := slices.Index(args, "--"); i >= 0 {
		args = args[i+1:]
	}
	return slices.Contains(args, ".")
}

// isStageAllOperand reports the stage-everything spellings of `git add`.
func isStageAllOperand(a string) bool {
	return a == "-A" || a == "--all" || a == "-u" || a == "--update" || a == "."
}

// isDeletingClean reports whether a `git clean` would actually delete.
//
// Read as short-flag CLUSTERS rather than as any word containing one of fdxX, which
// denied `git clean --dry-run` (the d in "dry") and `git clean --exclude=x` (the x):
// two invocations that remove nothing. A dry run anywhere wins: -n and --dry-run only
// list what would go.
func isDeletingClean(args []string) bool {
	deletes := false
	for _, a := range args {
		switch {
		case a == "--dry-run":
			return false
		case strings.HasPrefix(a, "--"):
			deletes = deletes || a == "--force"
		case strings.HasPrefix(a, "-"):
			cluster := a[1:]
			if strings.Contains(cluster, "n") {
				return false
			}
			deletes = deletes || strings.ContainsAny(cluster, "fdxX")
		}
	}
	return deletes
}

// nonGitVCSGuard is gitGuard for every other backend magus drives: Mercurial and
// Sapling, which are one dialect, and Jujutsu, which is its own.
//
// These were unmatched, and the guard doc justified that by saying magus "also drives
// Mercurial and Jujutsu, where recoverability differs: jj snapshots the working copy and
// keeps an operation log, so its nearest equivalents are undoable". That reasoning is
// TRUE OF JJ AND ONLY JJ, and it was generalized to Mercurial without argument. hg has no
// operation log: `hg rollback` only ever undid the last transaction and is gone, `hg
// revert` writes .orig backups for modified TRACKED files alone (and --no-backup drops
// even those), and `hg purge` deletes untracked files with no backup at all: the same
// blast radius as `git clean -f`, which denies. So an hg or sl user had none of the
// protection a git user has, for operations that are just as irrecoverable.
//
// jj was exempt too, on the same sentence's reasoning that its operation log makes these
// undoable. That exemption is gone. Undoable-IN-PRINCIPLE is a weaker guarantee than this
// bar: it needs someone to know `jj undo` exists and to reach for it before later
// operations bury the entry. And recoverability was never the whole test: the worktree
// rule denies because it destroys ANOTHER session's work, which `jj abandon` on a shared
// working copy does just as thoroughly.
//
// SCOPED forms stay allowed, matching the git rules: `hg revert <paths>` names what it
// touches, and only the whole-tree flags below discard a tree the caller did not enumerate.
//
// grade is the line's grading, as gitGuard takes it.
func nonGitVCSGuard(cmds []hint.Invocation, grade func(ShellVerdict) (ShellVerdict, bool)) (ShellVerdict, bool) {
	for _, c := range cmds {
		if v, ok := nonGitDeny(c); ok {
			if v, ok := grade(v); ok {
				return v, true
			}
		}
	}
	return ShellVerdict{}, false
}

// nonGitDeny is the deny one hg, sl or jj invocation earns, if any.
func nonGitDeny(c hint.Invocation) (ShellVerdict, bool) {
	if _, ok := vcsGlobals[c.Name]; !ok {
		return ShellVerdict{}, false
	}
	v := parseVCS(c.Name, c.Args)
	if v.alias != "" {
		return denyInlineAlias(c.Name, v.alias), true
	}
	sub, rest := v.sub, v.rest
	if c.Name == "jj" {
		return jjRule(c.Name, sub, rest)
	}
	if c.Name != "hg" && c.Name != "sl" {
		return ShellVerdict{}, false
	}
	switch sub {
	// Deletes UNTRACKED files outright. No backup, no undo, and untracked is exactly
	// where a concurrent agent's unfinished work lives.
	case "purge", "clean":
		return denyWholeTree(c.Name + " " + sub), true
	case "revert":
		if hasAnyFlag(rest, "--all", "-a") {
			return denyWholeTree(c.Name + " revert --all"), true
		}
	// `-C`/`--clean` discards uncommitted changes on the way to another revision,
	// with no .orig backup. sl spells the verb goto; hg accepts update, up and co.
	case "update", "up", "goto", "co":
		if hasAnyFlag(rest, "--clean", "-C") {
			return denyWholeTree(c.Name + " " + sub + " --clean"), true
		}
	}
	return ShellVerdict{}, false
}

// jjRule is the Jujutsu arm. Its verbs share no spelling with the others, so it reads as
// its own switch rather than another arm of one that would then be a lookup table.
func jjRule(prog, sub string, rest []string) (ShellVerdict, bool) {
	switch sub {
	// Abandons the changes in a revision, defaulting to the working copy.
	case "abandon":
		return denyWholeTree(prog + " abandon"), true
	// With paths it is the scoped restore the git rules also allow; with none it discards
	// every change in the working copy.
	case "restore":
		if !hasPositional(rest) {
			return denyWholeTree(prog + " restore"), true
		}
	// Denied outright, where git's removal is judged: the jj driver reports no workspace
	// registrations or publication (types.CheckoutReporter), so nothing can prove a forget
	// loses nothing.
	case "workspace":
		if len(rest) > 0 && rest[0] == "forget" {
			return ShellVerdict{
				Deny: "jj workspace forget drops a working-copy commit magus cannot prove empty; forget it from a session that owns it.",
				Why: "Do that once `jj workspace list` and `jj log` show its working-copy commit is empty or published.\n" +
					"jj workspace forget drops that workspace's working-copy commit, which in a repo running several is routinely another session's, and magus cannot yet read a jj workspace's state to prove it holds nothing.",
				Rule: denyRule{Name: denyRuleWorktreeRemove},
			}, true
		}
	}
	return ShellVerdict{}, false
}

// hasPositional reports whether args carries a non-flag argument, which for a restore is
// the difference between naming what to touch and taking the whole tree.
func hasPositional(args []string) bool {
	for _, a := range args {
		if a != "--" && !strings.HasPrefix(a, "-") {
			return true
		}
	}
	return false
}

// hasAnyFlag reports whether args carries any of the given flags as its own token, so a
// PATH that merely contains one does not match.
func hasAnyFlag(args []string, flags ...string) bool {
	for _, a := range args {
		if slices.Contains(flags, a) {
			return true
		}
	}
	return false
}
