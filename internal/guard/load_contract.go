package guard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/file"
)

// The load contract is what a magus built before a change needs to load the tree after it:
// the host declarations the magusfile compiles against, the API locks, the generated
// boundary bindings, and the Buzz checker and compiler that judge the policy. About one
// commit in five touches it; every other commit may move Go sources and still load.
//
// A copy of LOAD_CONTRACT in hack/dev/merge-job-branches.buzz: neither can read the other.
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
		Say: fmt.Sprintf("magus workspace: this changes the load contract (%s), so it lands through a dry run that builds and loads the result first, never straight into this checkout.", leadPath(touched)),
		Why: "A change to the host declarations, the API locks, the generated bindings or the Buzz checker can leave ./magus unable to load the tree, and a prefix of a series can leave a tree no binary builds from. " +
			"Then every write waits for a binary nothing can build. The dry run applies the whole change in a scratch worktree, compiles ./cmd/magus, checks generator drift and loads the guard policy with the result, and lands nothing unless all of it passes.",
	}.full()
}

// rankContractPick outranks only a pass or an advisory: a deny already stops the line.
func rankContractPick(v ShellVerdict, reason string) ShellVerdict {
	if reason == "" || v.Deny != "" {
		return v
	}
	return ShellVerdict{Deny: reason, Rule: denyRule{Name: denyRuleStaleBinary}}
}

// leadPath names the first of paths and how many more there are.
func leadPath(paths []string) string {
	if len(paths) == 1 {
		return paths[0]
	}
	return fmt.Sprintf("%s and %d more", paths[0], len(paths)-1)
}

// binarySkew is how the checkout's ./magus stands against the tree it judges, read once per
// HEAD: the load-contract paths that differ from the commit it was built at, and whether
// it was built from uncommitted changes, which makes that difference a guess.
type binarySkew struct {
	Head     string   `json:"head"`
	Built    string   `json:"built"`
	Dirty    bool     `json:"dirty,omitempty"`
	Contract []string `json:"contract,omitempty"`
}

const binarySkewFile = "binary-skew.jsonl"

// key is what a kept answer is looked up by: the HEAD it judged and the commit the binary
// was built at.
func (s binarySkew) key() string {
	return fmt.Sprintf("%s %s", s.Head, s.Built)
}

// checkoutSkew is binarySkew for the binary judging at, when it is the checkout's own
// ./magus and says which commit it was built at; ok is false otherwise. The answer is kept
// in the cache keyed by HEAD and that commit, so a call pays one git diff per HEAD.
func checkoutSkew(ctx context.Context, deps Dependencies, at location) (binarySkew, bool) {
	root := at.workspace
	if root == "" || deps.BinaryCommit == "" || deps.Revision == nil || !ownSourceRoot(root) || !sameFile(deps.Binary, filepath.Join(root, "magus")) {
		return binarySkew{}, false
	}
	head := deps.Revision(ctx, root, "")
	if head == "" {
		return binarySkew{}, false
	}
	store := file.JSONL[binarySkew]{Path: filepath.Join(at.cacheDir, binarySkewFile), Key: binarySkew.key}
	key := binarySkew{Head: head, Built: deps.BinaryCommit}.key()
	if at.cacheDir != "" {
		if kept, err := store.Load(); err == nil {
			if i := slices.IndexFunc(kept, func(s binarySkew) bool { return store.Key(s) == key }); i >= 0 {
				return kept[i], true
			}
		}
	}
	changed, err := gitNames(ctx, root, "diff", "--name-only", deps.BinaryCommit, "HEAD")
	if err != nil {
		return binarySkew{}, false
	}
	skew := binarySkew{Head: head, Built: deps.BinaryCommit, Dirty: deps.BinaryDirty, Contract: contractPaths(changed)}
	if at.cacheDir != "" {
		_ = store.Update(ctx, func([]binarySkew) []binarySkew { return []binarySkew{skew} })
	}
	return skew, true
}

func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}

// denyContractSkew refuses a state-changing line while ./magus was built before a change to
// the load contract that HEAD carries: its next load may fail, and a commit or run made now
// is judged by a policy that binary may not read. A binary built from uncommitted changes
// may already carry the change; the stale-sources notice speaks for it instead.
func denyContractSkew(ctx context.Context, deps Dependencies, at location, line string, d Dialect) string {
	skew, ok := checkoutSkew(ctx, deps, at)
	if !ok || len(skew.Contract) == 0 || skew.Dirty {
		return ""
	}
	if readOnlyLine(line, d) || recoveryLine(line, d, deps.lease, deps.lease == "") || replacesStaleMagus(line, at.workspace) {
		return ""
	}
	built := skew.Built
	if len(built) > 12 {
		built = built[:12]
	}
	fix := binaryRemedy(true, at.workspace, deps.lease, deps.lease == "")
	return denial{
		Say: fmt.Sprintf("magus workspace: ./magus was built at %s, before %s changed the load contract; %s first.", built, leadPath(skew.Contract), fix.say),
		Why: "A binary built before a change to the host declarations, the API locks, the generated bindings or the Buzz checker may not load the tree after it, and anything run now is judged by a policy it may not read. " +
			"Reads, `git status`, VCS recovery and the rebuild still run.",
	}.full()
}
