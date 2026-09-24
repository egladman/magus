package mergequeue

// This file composes the queue's version-control work from the VCS capabilities:
// building a candidate, predicting the tree a candidate merges as, and deciding what a
// review covers. Every choice of merge base, of which conflicts are the author's, and of
// which differences a review need not see is made here, so no backend can make it
// differently.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/libs/mergequeue/types"
	magustypes "github.com/egladman/magus/types"
)

// candidateIdentity authors every candidate commit. A candidate is never pushed, so no
// one sees it; it is fixed so the same inputs yield the same commit.
var candidateIdentity = magustypes.Person{Name: "merge queue", Email: "queue@mergequeue.invalid"}

// candidateDate dates every candidate commit, so the same inputs yield the same commit
// in every job that builds it.
var candidateDate = time.Unix(946684800, 0).UTC()

// reviewDepth bounds how many merges of the base reviewTarget and ownTop look through.
const reviewDepth = 8

// withLimit bounds the base-branch commits a conflict report names.
const withLimit = 10

func branchRef(branch string) string { return "refs/heads/" + branch }

func fetchBase(ctx context.Context, v types.ReadVCS, cl Clone, base string) (string, error) {
	tip, err := v.FetchRef(ctx, cl.Root, cl.Remote, branchRef(base))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", base, err)
	}
	return tip, nil
}

// fetchHead makes c.Head present: through c.Ref when it still names it, else by id,
// which covers a ref moved past the head or deleted since.
func fetchHead(ctx context.Context, v types.ReadVCS, cl Clone, c types.Change) error {
	if c.Ref != "" {
		if id, err := v.FetchRef(ctx, cl.Root, cl.Remote, c.Ref); err == nil && id == c.Head {
			return nil
		}
	}
	return v.FetchCommit(ctx, cl.Root, cl.Remote, c.Head)
}

// sources returns the paths no target declares as its output, in order.
func sources(ctx context.Context, f types.BuildFacts, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out, err := f.Outputs(ctx, paths)
	if err != nil {
		return nil, fmt.Errorf("outputs: %w", err)
	}
	return slices.DeleteFunc(slices.Clone(paths), func(p string) bool { return out[p] }), nil
}

// with names the commits on onto, since head forked from it, that touched paths. It is a
// report's detail, so a failure yields none rather than an error.
func with(ctx context.Context, v types.ReadVCS, root, onto, head string, paths []string) []string {
	commits, err := v.RangeCommits(ctx, root, head, onto, paths)
	if err != nil {
		return nil
	}
	var out []string
	for _, c := range commits {
		if len(c.Parents) > 1 {
			continue
		}
		out = append(out, short(c.ID)+" "+c.Subject)
		if len(out) == withLimit {
			break
		}
	}
	return out
}

// mergeBase is the merge base c's own delta is measured from when it merges onto rev: its
// stack base when rev does not carry it (the change beneath merged as a squash or a
// rebase), else "" for the natural one.
func mergeBase(ctx context.Context, v types.ReadVCS, root, rev string, c types.Change) (string, error) {
	if c.StackBase == "" {
		return "", nil
	}
	carried, err := v.IsAncestor(ctx, root, c.StackBase, rev)
	if err != nil || carried {
		return "", err
	}
	return c.StackBase, nil
}

// checkMerge merges c onto tip without a checkout and returns a *conflictError when a
// file no target declares as its output conflicts.
func checkMerge(ctx context.Context, v types.ReadVCS, f types.BuildFacts, root, tip string, c types.Change) error {
	mb, err := mergeBase(ctx, v, root, tip, c)
	if err != nil {
		return err
	}
	r, err := v.MergeTrees(ctx, root, magustypes.TreeMerge{Base: mb, Ours: tip, Theirs: c.Head})
	if err != nil {
		return err
	}
	src, err := sources(ctx, f, conflictPaths(r.Conflicts))
	if err != nil {
		return err
	}
	if len(src) > 0 {
		return &conflictError{conflict: sourceConflict{change: c, paths: src, with: with(ctx, v, root, tip, c.Head, src)}}
	}
	return nil
}

func conflictPaths(cs []magustypes.Conflict) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Path
	}
	return out
}

// candidateSpec is one candidate to build.
type candidateSpec struct {
	clone   Clone
	facts   types.BuildFacts
	onto    string
	change  types.Change
	scratch string // the directory each candidate gets a private one under
}

// built is a candidate whose merge is committed, and what regenerating it would rewrite.
type built struct {
	types.Candidate
	touched []string // the paths the merge changed or settled, sorted
	settled []string // the generated files it conflicted in, which took the change's side
}

// buildMerge checks out onto in a directory of its own, merges the change, settles
// conflicts in generated files by taking the change's side, and commits. A source
// conflict is a *conflictError. On any error nothing is left behind.
func buildMerge(ctx context.Context, v types.BuildVCS, s candidateSpec) (b built, err error) {
	c, root := s.change, s.clone.Root
	from := s.onto
	mb, err := mergeBase(ctx, v, root, s.onto, c)
	if err != nil {
		return built{}, err
	}
	if mb != "" {
		// A merge takes the natural merge base, which for a change stacked on a squashed
		// one is where the stack forked, and would bring back what the change deleted
		// from the one beneath it. Recording the stack base as merged into onto, without
		// taking any of its content, makes the stack base the natural one.
		if err := v.FetchCommit(ctx, root, s.clone.Remote, mb); err != nil {
			return built{}, err
		}
		tree, err := v.TreeID(ctx, root, s.onto)
		if err != nil {
			return built{}, err
		}
		from, err = v.CommitTree(ctx, root, magustypes.TreeCommit{CommitMeta: queueMeta("merge queue: #" + c.ID + " is stacked on " + short(mb)),
			Tree: tree, Parents: []string{s.onto, mb}})
		if err != nil {
			return built{}, err
		}
	}
	// A directory of its own per candidate, so no hook run on one can have planted
	// anything where another will look.
	box, err := os.MkdirTemp(s.scratch, "candidate-"+c.ID+"-")
	if err != nil {
		return built{}, err
	}
	cand := types.Candidate{Dir: filepath.Join(box, "checkout"), Scratch: filepath.Join(box, "scratch")}
	if err := os.Mkdir(cand.Scratch, 0o700); err != nil {
		_ = os.RemoveAll(box)
		return built{}, err
	}
	if err := v.CreateCheckout(ctx, root, cand.Dir, from); err != nil {
		_ = os.RemoveAll(box)
		return built{}, err
	}
	defer func() {
		if err != nil {
			// The build's own error is what the caller acts on.
			_ = discard(ctx, v, root, cand)
		}
	}()
	settled, err := mergeIn(ctx, v, s, cand.Dir)
	if err != nil {
		return built{}, err
	}
	if cand.Commit, err = v.Commit(ctx, cand.Dir, magustypes.CheckoutCommit{CommitMeta: queueMeta("merge queue: candidate #" + c.ID)}); err != nil {
		return built{}, err
	}
	touched, err := v.DiffTrees(ctx, root, s.onto, cand.Commit)
	if err != nil {
		return built{}, err
	}
	return built{Candidate: cand, touched: slices.Compact(slices.Sorted(slices.Values(slices.Concat(touched, settled)))), settled: settled}, nil
}

// discard removes a candidate's checkout and its private directory.
func discard(ctx context.Context, v types.BuildVCS, root string, cand types.Candidate) error {
	if cand.Dir == "" {
		return nil
	}
	err := v.RemoveCheckout(context.WithoutCancel(ctx), root, cand.Dir)
	if rmErr := os.RemoveAll(filepath.Dir(cand.Dir)); err == nil {
		err = rmErr
	}
	return err
}

// mergeIn merges the change into dir and settles the generated files it conflicts in,
// returning them.
func mergeIn(ctx context.Context, v types.BuildVCS, s candidateSpec, dir string) ([]string, error) {
	c := s.change
	// The queue's own identity, as on the commit concluding it: the candidate is the
	// same commit in every job that builds it, whatever the box configures.
	if err := v.StartMerge(ctx, dir, c.Head, candidateIdentity); err != nil {
		return nil, err
	}
	conflicts, err := v.Conflicts(ctx, dir)
	if err != nil || len(conflicts) == 0 {
		return nil, err
	}
	paths := conflictPaths(conflicts)
	src, err := sources(ctx, s.facts, paths)
	if err != nil {
		return nil, err
	}
	if len(src) > 0 {
		if err := v.AbortMerge(ctx, dir); err != nil {
			return nil, err
		}
		return nil, &conflictError{conflict: sourceConflict{change: c, paths: src, with: with(ctx, v, s.clone.Root, s.onto, c.Head, src)}}
	}
	var content, deleted []string
	for _, cf := range conflicts {
		if cf.Kind == magustypes.ConflictKindContent {
			content = append(content, cf.Path)
		} else {
			deleted = append(deleted, cf.Path)
		}
	}
	if len(content) > 0 {
		if err := v.KeepIncoming(ctx, dir, content); err != nil {
			return nil, err
		}
		if err := v.MarkResolved(ctx, dir, content); err != nil {
			return nil, err
		}
	}
	if len(deleted) > 0 {
		if err := v.RemoveConflicts(ctx, dir, deleted); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

// regenerateIn runs regenerate on the generated files among b.touched and commits what
// it rewrote. A rewrite of anything no target declares as its output is refused: a
// candidate adds only regenerated files to what was merged.
func regenerateIn(ctx context.Context, v types.BuildVCS, s candidateSpec, b built, regenerate types.RegenerateFunc, units []string) (string, error) {
	out, err := s.facts.Outputs(ctx, b.touched)
	if err != nil {
		return "", fmt.Errorf("outputs: %w", err)
	}
	regen := slices.DeleteFunc(slices.Clone(b.touched), func(p string) bool { return !out[p] })
	if len(regen) == 0 {
		return b.Commit, nil
	}
	if err := regenerate(ctx, types.Regeneration{Dir: b.Dir, Scratch: b.Scratch, Onto: s.onto, Change: s.change, Paths: regen, Units: units}); err != nil {
		return "", err
	}
	written, err := v.DirtyFiles(ctx, b.Dir, nil)
	if err != nil || len(written) == 0 {
		return b.Commit, err
	}
	stray, err := sources(ctx, s.facts, written)
	if err != nil {
		return "", err
	}
	if len(stray) > 0 {
		return "", &types.RefusedError{Reason: "regeneration wrote files no target declares as output: " + strings.Join(stray, ", "), Paths: stray}
	}
	return v.Commit(ctx, b.Dir, magustypes.CheckoutCommit{CommitMeta: queueMeta("regenerate generated files"), Paths: written})
}

func queueMeta(msg string) magustypes.CommitMeta {
	return magustypes.CommitMeta{Message: msg, Author: candidateIdentity, Committer: candidateIdentity, Date: candidateDate}
}

// predict is the tree the base carries once the candidate built onto onto merges onto
// tip: the candidate's changes since onto, merged onto tip with onto as the merge base.
// Onto, not any older commit, because a candidate's delta is defined against what it was
// built on: a change stacked on a squashed one deletes a line the one beneath it added,
// and from an older base that deletion reads as never having happened. A path both the
// candidate and what reached tip since onto changed is a combination nobody validated,
// reported as a *conflictError naming c.
func predict(ctx context.Context, v types.ReadVCS, root, tip, onto, cand string, c types.Change) (string, error) {
	if tip == onto {
		return v.TreeID(ctx, root, cand)
	}
	merged, err := v.DiffTrees(ctx, root, onto, tip)
	if err != nil {
		return "", err
	}
	built, err := v.DiffTrees(ctx, root, onto, cand)
	if err != nil {
		return "", err
	}
	var both []string
	for _, p := range merged {
		if slices.Contains(built, p) {
			both = append(both, p)
		}
	}
	if len(both) > 0 {
		return "", &conflictError{conflict: sourceConflict{change: c, paths: both}}
	}
	r, err := v.MergeTrees(ctx, root, magustypes.TreeMerge{Base: onto, Ours: tip, Theirs: cand})
	if err != nil {
		return "", err
	}
	if len(r.Conflicts) > 0 {
		return "", fmt.Errorf("merging %s onto %s conflicted in %s, which neither side changed alone", short(cand), short(tip), strings.Join(conflictPaths(r.Conflicts), ", "))
	}
	return r.Tree, nil
}

// regenerationProof is a merge of the base into a change whose tree differs from the
// plain merge only in declared outputs. Its review covers the commit beneath it only
// once the base's own regeneration, run on the plain merge of Onto into First, yields
// exactly Commit's tree.
type regenerationProof struct {
	Commit, First, Onto string
	Paths               []string
}

// reviewTarget is the commit a review of head covers: head itself, or, walking back
// through merges of the base branch into the change (GitHub's "Update branch", or an
// update commit the queue pushed), the commit beneath each one that adds nothing a
// reviewer did not see. A merge adds nothing when its tree is the plain merge of its
// parents, or, for a change stacked on stackBase, their merge from stackBase; callers
// pass a stack base only once the change beneath has merged, since until then that form
// drops the content beneath. A merge differing only in declared outputs adds nothing
// only if the base's regeneration reproduces them, which only a step running that
// regeneration can prove, so those are returned as proofs owed.
func reviewTarget(ctx context.Context, v types.ReadVCS, f types.BuildFacts, root, tip, head, stackBase string) (string, []regenerationProof, error) {
	var owed []regenerationProof
	for range reviewDepth {
		cm, err := v.FindCommit(ctx, root, head)
		if err != nil {
			return "", nil, err
		}
		if len(cm.Parents) != 2 {
			return head, owed, nil
		}
		first, second := cm.Parents[0], cm.Parents[1]
		onBase, err := v.IsAncestor(ctx, root, second, tip)
		if err != nil {
			return "", nil, err
		}
		if !onBase {
			return head, owed, nil
		}
		tree, err := v.TreeID(ctx, root, head)
		if err != nil {
			return "", nil, err
		}
		plain, err := v.MergeTrees(ctx, root, magustypes.TreeMerge{Ours: second, Theirs: first})
		if err != nil {
			return "", nil, err
		}
		if len(plain.Conflicts) == 0 && plain.Tree == tree {
			head = first
			continue
		}
		if stackBase != "" {
			stacked, err := v.MergeTrees(ctx, root, magustypes.TreeMerge{Base: stackBase, Ours: second, Theirs: first})
			if err != nil {
				return "", nil, err
			}
			if len(stacked.Conflicts) == 0 && stacked.Tree == tree {
				head = first
				continue
			}
		}
		diff, err := v.DiffTrees(ctx, root, plain.Tree, tree)
		if err != nil {
			return "", nil, err
		}
		src, err := sources(ctx, f, diff)
		if err != nil {
			return "", nil, err
		}
		if len(src) > 0 || len(diff) == 0 {
			return head, owed, nil
		}
		owed = append(owed, regenerationProof{Commit: head, First: first, Onto: second, Paths: diff})
		head = first
	}
	return head, owed, nil
}

// forkPoint is where x left tip's history: the one commit outside x's own commits that
// they sit on, x itself when tip carries it, or "" when x merged tip's history in more
// than once, which no rebase moves cleanly.
func forkPoint(ctx context.Context, v types.ReadVCS, root, tip, x string) (string, error) {
	own, err := v.RangeCommits(ctx, root, tip, x, nil)
	if err != nil {
		return "", err
	}
	return forkOf(x, own), nil
}

// forkOf is forkPoint over own, x's own commits with their parents.
func forkOf(x string, own []magustypes.Commit) string {
	if len(own) == 0 {
		return x
	}
	in := make(map[string]bool, len(own))
	for _, c := range own {
		in[c.ID] = true
	}
	fork := ""
	for _, c := range own {
		for _, p := range c.Parents {
			switch {
			case in[p], p == fork:
			case fork == "":
				fork = p
			default:
				return ""
			}
		}
	}
	return fork
}

// trivialRebase reports whether now is old rebased from oldBase onto newBase with
// nothing else changed: old's whole delta since oldBase, replayed onto newBase, merges
// without a conflict into exactly now's tree.
func trivialRebase(ctx context.Context, v types.ReadVCS, root, oldBase, old, newBase, now string) (bool, error) {
	if oldBase == "" || newBase == "" {
		return false, nil
	}
	r, err := v.MergeTrees(ctx, root, magustypes.TreeMerge{Base: oldBase, Ours: newBase, Theirs: old})
	if err != nil || len(r.Conflicts) > 0 {
		return false, err
	}
	tree, err := v.TreeID(ctx, root, now)
	if err != nil {
		return false, err
	}
	return r.Tree == tree, nil
}

// ownCommits is the set of commits x carries that tip does not.
func ownCommits(ctx context.Context, v types.ReadVCS, root, tip, x string) (map[string]bool, error) {
	commits, err := v.RangeCommits(ctx, root, tip, x, nil)
	if err != nil {
		return nil, err
	}
	own := make(map[string]bool, len(commits))
	for _, c := range commits {
		own[c.ID] = true
	}
	return own, nil
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// sourceConflict is a textual conflict in files that are not generated: a real code
// conflict, which the queue never resolves.
type sourceConflict struct {
	change types.Change // the change whose addition conflicted
	paths  []string     // the conflicted source files
	with   []string     // base-branch commits touching paths ("abc123 subject")
}

type conflictError struct{ conflict sourceConflict }

func (e *conflictError) Error() string {
	return fmt.Sprintf("%s conflicts in %s", e.conflict.change.Label(), strings.Join(e.conflict.paths, ", "))
}

func asConflict(err error) (sourceConflict, bool) {
	var ce *conflictError
	if errors.As(err, &ce) {
		return ce.conflict, true
	}
	return sourceConflict{}, false
}

// waitError says a change cannot go further now for a reason that says nothing against
// it, such as its branch moving. The change waits and is retried on the next run.
type waitError struct {
	code   types.Code
	reason string
}

func (e *waitError) Error() string { return e.reason }
