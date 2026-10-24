package guard

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
)

// workerVCSOp names the version-control operation a worker lease may not run, short of a
// commit placed in its own checkout, or "" for one it may. It holds a worker to a list of
// what it may run rather than vcsMutation's list of what nobody may: a verb neither list
// names, a config alias included, is refused instead of slipping through.
func workerVCSOp(c hint.Invocation) string {
	if fastForward(c) {
		return ""
	}
	if op := vcsMutation(c); op != "" {
		return op
	}
	sub, rest := vcsSubcommand(c)
	var may bool
	switch c.Name {
	case "git":
		may = workerMayRunGit(sub, rest)
	case "hg", "sl":
		may = workerMayRunHg(sub, rest)
	case "jj":
		may = workerMayRunJJ(sub, rest)
	default:
		return ""
	}
	if sub == "" || may {
		return ""
	}
	return fmt.Sprintf("%s %s", c.Name, sub)
}

// fastForward reports `git merge --ff-only <commit>`, the recovery of a worker whose placed
// ./magus was built ahead of its checkout: it moves the branch the checkout is on forward to
// a commit named by its id, and git refuses it when the branch has diverged or a change
// nobody committed is in the way, so it rewrites and loses nothing. A branch name is refused,
// since following one is how a worker drifts onto commits nobody handed it, and so is a git
// option ahead of the verb, which can point the merge at another checkout.
func fastForward(c hint.Invocation) bool {
	if c.Name != "git" || len(c.Args) != 3 || c.Args[0] != "merge" {
		return false
	}
	flag, commit := c.Args[1], c.Args[2]
	if flag != "--ff-only" {
		flag, commit = commit, flag
	}
	return flag == "--ff-only" && commitID(commit)
}

// integrationMerge reports `git merge --no-ff [--no-edit] <branch>` for a branch row, an
// integration job, names in integrates: the one merge its holder exists to run. Any other
// branch, option or verb is judged as for every worker, so the allowance widens nothing else.
func integrationMerge(c hint.Invocation, row types.Job) bool {
	if c.Name != "git" || !row.Integrating() || len(c.Args) < 3 || len(c.Args) > 4 || c.Args[0] != "merge" {
		return false
	}
	var noFF bool
	branch := ""
	for _, a := range c.Args[1:] {
		switch {
		case a == "--no-ff":
			noFF = true
		case a == "--no-edit":
		case strings.HasPrefix(a, "-") || branch != "":
			return false
		default:
			branch = a
		}
	}
	return noFF && slices.Contains(row.Integrates, branch)
}

// workerMergeFlags are the git merge options a worker's merge of its base may carry: none
// picks a strategy, so neither side of the merge can be dropped.
var workerMergeFlags = []string{"--no-edit", "--edit", "-e", "--ff", "--no-ff", "--ff-only", "--commit", "--no-commit",
	"--squash", "--log", "--no-log", "--stat", "-n", "--no-stat", "-q", "--quiet", "-v", "--verbose"}

// workerMerge reads `git merge` as a worker may run it: `--continue` or `--abort`, which
// finish or drop the merge under way (operand ""), or one revision merged with only
// workerMergeFlags and a message. ok is false for any other merge: an octopus, a strategy,
// or an option this list does not name.
func workerMerge(inv hint.Invocation) (operand string, ok bool) {
	sub, rest := vcsSubcommand(inv)
	if inv.Name != "git" || sub != "merge" {
		return "", false
	}
	if len(rest) == 1 && (rest[0] == "--continue" || rest[0] == "--abort") {
		return "", true
	}
	var operands []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "-m" || a == "--message":
			i++
		case strings.HasPrefix(a, "--message="), slices.Contains(workerMergeFlags, a):
		case strings.HasPrefix(a, "-"):
			return "", false
		default:
			operands = append(operands, a)
		}
	}
	if len(operands) != 1 {
		return "", false
	}
	return operands[0], true
}

// mergesBase reports operand naming the base a worker may merge into its own branch: base
// itself, its origin/ form, the revision the job was handed or reported, by id, or the
// branch of a job its row depends on. A job id is its branch name, so a stacked job reaches
// the work it builds on without a second base.
func mergesBase(operand, base string, row types.Job) bool {
	if slices.ContainsFunc(row.DependsOn, func(dep string) bool {
		return dep != "" && (operand == dep || operand == "origin/"+dep)
	}) {
		return true
	}
	if commitID(operand) {
		for _, token := range []string{row.Checkpoint, row.ReportedBase} {
			if rev, _ := types.ParseCheckpointToken(token); rev != "" && strings.HasPrefix(rev, operand) {
				return true
			}
		}
		return row.Worktree != nil && row.Worktree.Base != "" && strings.HasPrefix(row.Worktree.Base, operand)
	}
	return base != "" && (operand == base || operand == "origin/"+path.Base(base))
}

// commitID reports a full or abbreviated hexadecimal object name, as `magus version` prints a
// build's commit.
func commitID(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// addsCheckout reports a call that makes a second checkout of the repository by hand: `git
// worktree add`, `hg share` or `jj workspace add`. Sapling has no such verb.
func addsCheckout(c hint.Invocation) bool {
	sub, rest := vcsSubcommand(c)
	switch c.Name {
	case "git":
		return sub == "worktree" && firstWordIs(rest, "add")
	case "hg":
		return sub == "share"
	case "jj":
		return sub == "workspace" && firstWordIs(rest, "add")
	}
	return false
}

// removesOwnCheckout reports whether each worktree removal on command names the checkout
// row's fork made for it, which its holder may remove; the worktree-remove rule still
// judges whether that loses work.
func removesOwnCheckout(command, cwd string, row types.Job) bool {
	if !row.Worktree.Standing() {
		return false
	}
	calls, ok := locateCalls(command, DialectBash, cwd, isWorktreeRemove, false)
	if !ok || len(calls) == 0 {
		return false
	}
	for _, c := range calls {
		s, why := locateRemoval(c)
		if why != "" || !samePath(s.target, row.Worktree.Root) {
			return false
		}
	}
	return true
}

// addsScratchCheckout reports whether each checkout add on command makes its checkout
// under $XDG_CACHE_HOME/magus/tmp, a temp copy outside every workspace that moves nothing
// the orchestrator reads. A target the line does not spell as a literal path fails, and so
// does a relative one after a global option, which may move where it resolves from.
func addsScratchCheckout(command, cwd string) bool {
	cache, err := xdg.CacheDir()
	if err != nil {
		return false
	}
	scratch := filepath.Join(cache, "magus", "tmp")
	calls, ok := locateCalls(command, DialectBash, cwd, addsCheckout, false)
	if !ok || len(calls) == 0 {
		return false
	}
	for _, c := range calls {
		if c.unfollowed || c.args == nil {
			return false
		}
		sub, rest := vcsSubcommand(c.inv)
		target := checkoutAddTarget(c.inv.Name, rest)
		if target == "" || strings.ContainsAny(target, "$`~") {
			return false
		}
		if !filepath.IsAbs(target) {
			if !c.at.known || c.at.dir == "" || len(c.inv.Args) == 0 || c.inv.Args[0] != sub {
				return false
			}
			target = filepath.Join(c.at.dir, target)
		}
		target = filepath.Clean(target)
		if samePath(target, scratch) || !within(target, scratch) {
			return false
		}
	}
	return true
}

// checkoutAddTarget is the directory a checkout add names, "" when its words do not name
// exactly one. rest is the words after the subcommand.
func checkoutAddTarget(tool string, rest []string) string {
	var valued []string
	switch tool {
	case "git":
		if !firstWordIs(rest, "add") {
			return ""
		}
		rest, valued = rest[1:], []string{"-b", "-B", "--reason"}
	case "jj":
		if !firstWordIs(rest, "add") {
			return ""
		}
		rest, valued = rest[1:], []string{"--name", "-r", "--revision", "--sparse-patterns", "-m", "--message"}
	case "hg":
	default:
		return ""
	}
	var positional []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--":
			positional = append(positional, rest[i+1:]...)
			i = len(rest)
		case slices.Contains(valued, a):
			i++
		case strings.HasPrefix(a, "-"):
		default:
			positional = append(positional, a)
		}
	}
	switch {
	case tool == "hg" && len(positional) == 2:
		// hg share SOURCE DEST; DEST defaults to SOURCE's name here, so it must be spelled.
		return positional[1]
	case tool != "hg" && len(positional) >= 1:
		return positional[0]
	}
	return ""
}

// checkoutAddDenial words the lease-vcs refusal of a checkout made by hand. what is the
// refused call as the reader typed it.
func checkoutAddDenial(what string, row types.Job, role string) string {
	return fmt.Sprintf("magus workspace: %s makes a checkout by hand, and lease %s is %s: `%s` makes a job's checkout, and removes it once the job ends",
		what, row.ID, role, hint.JobFork.With("<job>", "--checkout", "<dir>"))
}

// workerGitVerbs read the repository, or touch only the worker's own index and branch. rm
// and mv touch the tree too, and lease-write grades each path they name.
var workerGitVerbs = []string{
	"add", "annotate", "blame", "cat-file", "check-attr", "check-ignore", "check-ref-format",
	"cherry", "commit", "count-objects", "describe", "diff", "diff-files", "diff-index",
	"diff-tree", "for-each-ref", "grep", "help", "log", "ls-files", "ls-remote", "ls-tree",
	"merge-base", "mv", "name-rev", "range-diff", "restore", "rev-list", "rev-parse", "rm",
	"shortlog", "show", "show-branch", "show-ref", "stash", "status", "var", "verify-commit",
	"verify-tag", "version", "whatchanged",
}

// workerVCSMovesRefs reports a call that moves HEAD, a branch, a bookmark or the index: a
// worker's own state only in its own checkout, and another checkout's state anywhere else.
// A listing or a read moves nothing.
func workerVCSMovesRefs(c hint.Invocation) bool {
	sub, rest := vcsSubcommand(c)
	switch c.Name {
	case "git":
		switch sub {
		case "switch", "checkout", "restore", "stash", "add", "rm", "mv":
			return true
		case "branch":
			return positionals(rest) > 0 && !anyFlag(rest, "alrv", "--list", "--all", "--remotes",
				"--contains", "--no-contains", "--merged", "--no-merged", "--points-at", "--show-current")
		}
	case "hg", "sl":
		switch sub {
		case "update", "up", "checkout", "co", "goto", "add", "addremove", "remove", "rm", "forget",
			"rename", "mv", "move", "copy", "cp", "shelve", "unshelve":
			return true
		case "bookmark", "book", "branch":
			return positionals(rest) > 0 && !anyFlag(rest, "l", "--list")
		}
	case "jj":
		switch sub {
		case "new", "edit", "next", "prev":
			return true
		case "bookmark", "b":
			return firstWordIs(rest, "create", "c", "set", "s", "move", "m", "rename", "r", "delete", "d", "forget", "f", "track", "t", "untrack")
		}
	}
	return false
}

func workerMayRunGit(sub string, rest []string) bool {
	switch sub {
	case "rm", "mv":
		if anyFlag(rest, "", "--pathspec-from-file") || !namesOnlyPaths(operands(rest, "")) {
			return false
		}
	case "branch":
		return !anyFlag(rest, "dDfmMcCu", "--delete", "--force", "--move", "--copy",
			"--set-upstream-to", "--unset-upstream", "--edit-description")
	case "switch":
		return !anyFlag(rest, "Cfm", "--force-create", "--force", "--discard-changes", "--merge")
	case "checkout":
		return !anyFlag(rest, "Bfm", "--force", "--merge")
	case "fetch":
		return !anyFlag(rest, "u", "--update-head-ok") && !fetchesIntoLocalRef(rest)
	case "config":
		return firstWordIs(rest, "get", "list") ||
			anyFlag(rest, "l", "--get", "--get-all", "--get-regexp", "--get-urlmatch", "--list")
	case "tag":
		return (len(rest) == 0 || anyFlag(rest, "l", "--list")) && !anyFlag(rest, "df", "--delete", "--force")
	case "remote":
		return len(rest) == 0 || firstWordIs(rest, "-v", "--verbose", "show", "get-url")
	case "reflog":
		return len(rest) == 0 || firstWordIs(rest, "show") || strings.HasPrefix(rest[0], "-")
	case "symbolic-ref":
		return !anyFlag(rest, "d", "--delete") && positionals(rest) <= 1
	case "worktree":
		return firstWordIs(rest, "list")
	case "submodule":
		return len(rest) == 0 || firstWordIs(rest, "status", "summary")
	case "sparse-checkout":
		return firstWordIs(rest, "list")
	case "notes":
		return len(rest) == 0 || firstWordIs(rest, "list", "show")
	case "apply":
		return anyFlag(rest, "", "--check", "--stat", "--numstat", "--summary") &&
			!anyFlag(rest, "3", "--apply", "--index", "--cached", "--3way")
	}
	return slices.Contains(workerGitVerbs, sub)
}

// fetchesIntoLocalRef reports whether a fetch names a refspec with a destination, which
// moves a local branch instead of a remote-tracking one. The first positional is the
// remote, and a URL carries a colon of its own.
func fetchesIntoLocalRef(rest []string) bool {
	seen := 0
	for _, a := range rest {
		if a == "--" || strings.HasPrefix(a, "-") {
			continue
		}
		seen++
		if seen > 1 && strings.Contains(a, ":") {
			return true
		}
	}
	return false
}

var workerHgVerbs = []string{
	"add", "annotate", "blame", "branch", "branches", "cat", "ci", "commit", "diff", "files",
	"grep", "heads", "help", "id", "identify", "in", "incoming", "log", "manifest", "mv", "out",
	"outgoing", "parents", "paths", "remove", "rename", "rm", "root", "show", "smartlog", "ssl",
	"st", "status", "sum", "summary", "tip", "version", "whereami",
}

func workerMayRunHg(sub string, rest []string) bool {
	switch sub {
	case "remove", "rm", "rename", "mv":
		if anyFlag(rest, "IX", "--include", "--exclude") || !namesOnlyPaths(operands(rest, "IX")) {
			return false
		}
	case "bookmark", "bookmarks", "book":
		return !anyFlag(rest, "dfm", "--delete", "--force", "--rename")
	case "update", "up", "goto", "co", "checkout":
		return !anyFlag(rest, "C", "--clean")
	case "config":
		return !anyFlag(rest, "e", "--edit") && !slices.ContainsFunc(rest, func(a string) bool {
			return !strings.HasPrefix(a, "-") && strings.Contains(a, "=")
		})
	}
	return slices.Contains(workerHgVerbs, sub)
}

var workerJJVerbs = []string{
	"commit", "desc", "describe", "diff", "evolog", "help", "interdiff", "log", "new",
	"obslog", "root", "show", "st", "status", "version",
}

func workerMayRunJJ(sub string, rest []string) bool {
	switch sub {
	case "bookmark", "b", "branch":
		return firstWordIs(rest, "list", "l")
	case "config":
		return firstWordIs(rest, "get", "list", "path", "l")
	case "file":
		return firstWordIs(rest, "show", "list", "annotate")
	case "op", "operation":
		return firstWordIs(rest, "log", "show", "diff")
	case "workspace":
		return firstWordIs(rest, "list", "root")
	case "git":
		return firstWordIs(rest, "fetch")
	case "restore":
		return hasPositional(rest)
	}
	return slices.Contains(workerJJVerbs, sub)
}

// namesOnlyPaths reports operands that are each a path the line spells out. lease-write
// grades the words, so a pathspec the VCS expands (`.`, a glob, git's `:` magic, an hg
// `glob:` pattern) or one climbing out with `..` would remove files no word names.
func namesOnlyPaths(ops []string) bool {
	if len(ops) == 0 {
		return false
	}
	for _, op := range ops {
		clean := path.Clean(filepath.ToSlash(op))
		if clean == "." || clean == "/" || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsAny(op, "*?[:") {
			return false
		}
	}
	return true
}

// anyFlag is hasFlag over several flags: any letter of shorts, or any of longs.
func anyFlag(args []string, shorts string, longs ...string) bool {
	for _, r := range shorts {
		if hasFlag(args, r, "") {
			return true
		}
	}
	return slices.ContainsFunc(longs, func(long string) bool {
		return hasFlag(args, 0, strings.TrimPrefix(long, "--"))
	})
}

func firstWordIs(args []string, words ...string) bool {
	return len(args) > 0 && slices.Contains(words, args[0])
}

func positionals(args []string) int {
	n := 0
	for _, a := range args {
		if a != "--" && !strings.HasPrefix(a, "-") {
			n++
		}
	}
	return n
}
