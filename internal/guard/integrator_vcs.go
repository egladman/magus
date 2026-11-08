package guard

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

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

// integratorLands reports a call an integrator lands in its own checkout:
// a rebase integratorRebase reads, a cherry-pick or a revert.
func integratorLands(inv hint.Invocation) bool {
	if _, ok := integratorRebase(inv); ok {
		return true
	}
	sub, _ := vcsSubcommand(inv)
	return inv.Name == "git" && (sub == "cherry-pick" || sub == "revert")
}

// rebaseFlags are the options an integrator's rebase may carry. None runs
// a command, stashes, or moves a ref but the checked-out branch.
var rebaseFlags = []string{"-q", "--quiet", "-v", "--verbose", "--stat", "-n", "--no-stat",
	"--autosquash", "--no-autosquash", "--keep-empty", "--no-keep-empty", "--reapply-cherry-picks",
	"--no-reapply-cherry-picks", "--fork-point", "--no-fork-point", "--rebase-merges", "--no-rebase-merges",
	"--committer-date-is-author-date", "--reset-author-date", "--signoff", "--no-verify", "--verify",
	"--no-update-refs", "--no-autostash"}

// integratorRebase reads `git rebase` as an integrator may run it. operand
// is where the branch lands: --onto, else the one upstream. It is "" for
// --continue, --abort or --skip. ok is false for any other rebase.
func integratorRebase(inv hint.Invocation) (operand string, ok bool) {
	sub, rest := vcsSubcommand(inv)
	if inv.Name != "git" || sub != "rebase" {
		return "", false
	}
	if len(rest) == 1 && slices.Contains([]string{"--continue", "--abort", "--skip"}, rest[0]) {
		return "", true
	}
	var onto string
	var positional []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--onto" && i+1 < len(rest):
			i++
			onto = rest[i]
		case strings.HasPrefix(a, "--onto="):
			onto = strings.TrimPrefix(a, "--onto=")
		case slices.Contains(rebaseFlags, a):
		case strings.HasPrefix(a, "-"):
			return "", false
		default:
			positional = append(positional, a)
		}
	}
	// A second positional switches to another branch first.
	if len(positional) != 1 {
		return "", false
	}
	return cmp.Or(onto, positional[0]), true
}
