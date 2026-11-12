package guard

import (
	"cmp"
	"context"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/contract"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/ward"
)

// The load contract is what a magus built before a change needs to load the tree after it:
// the host declarations the magusfile compiles against, the API locks, the generated
// boundary bindings, and the Buzz checker and compiler that judge the policy. About one
// commit in five touches it; every other commit may move Go sources and still load.
// The paths are magus's own module, judged only in a checkout of it.
//
// A copy of loadContract in hack/magusfile/load-contract.buzz: Buzz reaches no Go digest.
var (
	loadContractDirs  = []string{"internal/spell/gen/decls/", "internal/interp/bindings/gen/"}
	loadContractFiles = []string{"libs/gopherbuzz/checker*.go", "libs/gopherbuzz/compiler*.go"}
)

// loadContract reports a root-relative path in the load contract.
func loadContract(rel string) bool {
	rel = filepath.ToSlash(rel)
	if strings.HasSuffix(rel, "_test.go") {
		return false
	}
	if strings.HasSuffix(rel, "api.lock") || strings.HasSuffix(rel, "api-go.lock") {
		return true
	}
	if slices.ContainsFunc(loadContractDirs, func(dir string) bool { return strings.HasPrefix(rel, dir) }) {
		return true
	}
	return slices.ContainsFunc(loadContractFiles, func(glob string) bool {
		ok, _ := path.Match(glob, rel)
		return ok
	})
}

// contractPaths is the load-contract paths among changed, sorted.
func contractPaths(changed []string) []string {
	var out []string
	for _, p := range changed {
		if loadContract(p) {
			out = append(out, filepath.ToSlash(p))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// gitNames runs git in root and returns the paths it names, one per line.
func gitNames(ctx context.Context, root string, args ...string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root, "-c", "core.quotepath=false"}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.Fields(string(out)), nil
}

// incomingChange is what a `git cherry-pick` or `git merge` line brings into root, as git
// would apply it, and whether the line is one of those. A pick of a range or of several
// commits counts them all; a merge counts the other side since the merge base. Lines that
// continue, skip or abort one, and a fast-forward, bring nothing new.
func incomingChange(ctx context.Context, root string, words []string) ([]string, bool) {
	if len(words) < 3 || path.Base(words[0]) != "git" {
		return nil, false
	}
	var revs []string
	for _, w := range words[2:] {
		switch {
		case w == "--abort" || w == "--continue" || w == "--skip" || w == "--quit" || w == "--ff-only":
			return nil, false
		case strings.HasPrefix(w, "-"):
		default:
			revs = append(revs, w)
		}
	}
	if len(revs) == 0 {
		return nil, false
	}
	var changed []string
	for _, rev := range revs {
		var names []string
		var err error
		switch {
		case words[1] == "merge":
			names, err = gitNames(ctx, root, "diff", "--name-only", "HEAD..."+rev)
		case words[1] != "cherry-pick":
			return nil, false
		case strings.Contains(rev, ".."):
			names, err = gitNames(ctx, root, "diff", "--name-only", rev)
		default:
			names, err = gitNames(ctx, root, "diff-tree", "--no-commit-id", "--name-only", "-r", "-m", "--first-parent", rev)
		}
		if err != nil {
			// A rev git cannot read is git's to refuse when the line runs.
			return nil, false
		}
		changed = append(changed, names...)
	}
	return changed, true
}

// denyContractPick refuses a pick or merge that changes the load contract straight into a
// checkout of magus whose tree still loads. While it does not load, the pick is the way
// out, and the stale-binary rule lets it through.
func denyContractPick(ctx context.Context, deps Dependencies, at location, line string, d Dialect) string {
	if deps.LoadFailure != nil {
		return ""
	}
	call, ok := soleCall(line, d)
	if !ok {
		return ""
	}
	root := at.workspace
	if root == "" || !ownSourceRoot(root) {
		return ""
	}
	changed, ok := incomingChange(ctx, root, literalWords(call.Args))
	if !ok {
		return ""
	}
	touched := contractPaths(changed)
	if len(touched) == 0 {
		return ""
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: this changes the load contract (%s), so it lands in a checkout of its own first: `%s`.",
			leadPath(touched), hint.JobFork.With("<integration>", "--checkout", "<dir>")),
		Why: "A change to the host declarations, the API locks, the generated bindings or the Buzz checker can leave ./magus unable to load the tree, and a prefix of a series can leave a tree no binary builds from. " +
			"Then every write waits for a binary nothing can build. In a checkout of its own the whole change is applied, ./cmd/magus compiled and the guard policy loaded before anything reaches this branch. " +
			"Pick and build there, then move this branch to the result.",
	}.full()
}

// rankContractPick outranks only a pass or an advisory: a deny already stops the line.
func rankContractPick(v ShellVerdict, reason string) ShellVerdict {
	if reason == "" || v.Deny != "" {
		return v
	}
	return ShellVerdict{Deny: reason, Rule: denyRule{Name: denyRuleStaleBinary}}
}

// denyContractSkew refuses a state-changing line while the binary's
// contract differs from a tree it still loads. A failed load is the
// stale-binary rule's, whose denial names the failure.
// Reads, VCS recovery and the relink still run.
func denyContractSkew(deps Dependencies, at location, line string, d Dialect) string {
	root := at.workspace
	if root == "" || deps.LoadFailure != nil {
		return ""
	}
	skew, err := contract.Check(root)
	if err == nil && !skew.Differs() {
		return ""
	}
	if readOnlyLine(line, d) || recoveryLine(line, d, deps.lease, deps.lease == "", at) || replacesStaleMagus(line, at) {
		return ""
	}
	if err != nil {
		return denial{
			Say: fmt.Sprintf("magus workspace: cannot read the contract %s carries (%v); relink ./magus, alone on its line: `%s`.",
				root, err, ward.RelinkAt(root)),
			Why: withPolicyNote(relinkWhy, runPolicyNote(line, at)),
		}.full()
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: %s (%s) embeds contract %.12s, and %s carries %.12s; relink ./magus, alone on its line: `%s`.",
			deps.Binary, cmp.Or(deps.BinaryVersion, "an unknown version"), skew.Binary, root, skew.Tree, ward.RelinkAt(root)),
		Why: withPolicyNote(relinkWhy, runPolicyNote(line, at)),
	}.full()
}

// leadPath names the first of paths and how many more there are.
func leadPath(paths []string) string {
	if len(paths) == 1 {
		return paths[0]
	}
	return fmt.Sprintf("%s and %d more", paths[0], len(paths)-1)
}
