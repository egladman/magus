package guard

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// workerVCSOp names the version-control operation a worker lease may not run, short of a
// commit placed in its own checkout, or "" for one it may. It holds a worker to a list of
// what it may run rather than vcsMutation's list of what nobody may: a verb neither list
// names, a config alias included, is refused instead of slipping through.
func workerVCSOp(c hint.Invocation) string {
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
	return c.Name + " " + sub
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
