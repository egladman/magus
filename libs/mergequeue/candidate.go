package mergequeue

// This file composes the queue's version-control work from [VCS]: building a candidate,
// predicting the tree a candidate lands as, and deciding what a review covers. Every
// choice of merge base, of which conflicts are the author's, and of which differences a
// review need not see is made here, so no backend can make it differently.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// queueIdentity authors every candidate commit and commits every update commit. A
// candidate is never pushed.
var queueIdentity = Person{Name: "merge queue", Email: "queue@mergequeue.invalid"}

// candidateDate dates every candidate commit, so the same inputs yield the same commit.
var candidateDate = time.Unix(946684800, 0).UTC()

// reviewDepth bounds how many merges of the base reviewTarget looks through.
const reviewDepth = 8

// withLimit bounds the base-branch commits a conflict report names.
const withLimit = 10

// checkoutSeq names concurrent checkouts apart.
var checkoutSeq atomic.Int64

func branchRef(branch string) string { return "refs/heads/" + branch }

// fetchHead makes c.Head present: through c.Ref when it still names it, else by id,
// which covers a ref moved past the head or deleted since.
func fetchHead(ctx context.Context, v VCS, c Change) error {
	if c.Ref != "" {
		if id, err := v.FetchRef(ctx, c.Ref); err == nil && id == c.Head {
			return nil
		}
	}
	return v.FetchCommit(ctx, c.Head)
}

// sources returns the paths rev does not mark generated, in order.
func sources(ctx context.Context, v VCS, rev string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	gen, err := v.GeneratedPaths(ctx, rev, paths)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(paths), func(p string) bool { return gen[p] }), nil
}

// with names the commits on onto, since head forked from it, that touched paths. It is a
// report's detail, so a failure yields none rather than an error.
func with(ctx context.Context, v VCS, onto, head string, paths []string) []string {
	commits, err := v.RangeCommits(ctx, head, onto, paths)
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

// mergeBase is the merge base c's own delta is measured from when it lands on rev: its
// stack base when rev does not carry it (the change beneath landed as a squash or a
// rebase), else "" for the natural one.
func mergeBase(ctx context.Context, v VCS, rev string, c Change) (string, error) {
	if c.StackBase == "" {
		return "", nil
	}
	carried, err := v.IsAncestor(ctx, c.StackBase, rev)
	if err != nil || carried {
		return "", err
	}
	return c.StackBase, nil
}

// checkMerge merges c onto tip without a checkout and returns a *ConflictError when a
// file baseCommit does not mark generated conflicts.
func checkMerge(ctx context.Context, v VCS, baseCommit, tip string, c Change) error {
	mb, err := mergeBase(ctx, v, tip, c)
	if err != nil {
		return err
	}
	r, err := v.MergeTrees(ctx, TreeMerge{Base: mb, Ours: tip, Theirs: c.Head})
	if err != nil {
		return err
	}
	src, err := sources(ctx, v, baseCommit, r.Conflicts)
	if err != nil {
		return err
	}
	if len(src) > 0 {
		return &ConflictError{Conflict: Conflict{Change: c, Paths: src, With: with(ctx, v, tip, c.Head, src)}}
	}
	return nil
}

// candidateSpec is one candidate to build.
type candidateSpec struct {
	baseCommit string // marks generated files
	onto       string
	change     Change
	scratch    string
	regenerate RegenerateFunc
}

// buildCandidate checks out onto in a directory of its own, merges the change, settles
// conflicts in generated files, regenerates the generated files the merge touched, and
// commits. A source conflict is a *ConflictError; a regeneration the change's code broke,
// or one writing a file that is not generated, is a *RefusedError. On any error the
// checkout is gone.
func buildCandidate(ctx context.Context, v VCS, s candidateSpec) (cand Candidate, err error) {
	c := s.change
	from := s.onto
	mb, err := mergeBase(ctx, v, s.onto, c)
	if err != nil {
		return Candidate{}, err
	}
	if mb != "" {
		// A merge takes the natural merge base, which for a change stacked on a squashed
		// one is where the stack forked, and would bring back what the change deleted
		// from the one beneath it. Recording the stack base as merged into onto, without
		// taking any of its content, makes the stack base the natural one.
		if err := v.FetchCommit(ctx, mb); err != nil {
			return Candidate{}, err
		}
		tree, err := v.TreeID(ctx, s.onto)
		if err != nil {
			return Candidate{}, err
		}
		from, err = v.CommitTree(ctx, TreeCommit{CommitMeta: queueMeta("merge queue: #" + c.ID + " is stacked on " + short(mb)),
			Tree: tree, Parents: []string{s.onto, mb}})
		if err != nil {
			return Candidate{}, err
		}
	}
	dir := filepath.Join(s.scratch, fmt.Sprintf("candidate-%d-%d-%s", os.Getpid(), checkoutSeq.Add(1), c.ID))
	if err := v.CreateCheckout(ctx, dir, from); err != nil {
		return Candidate{}, err
	}
	defer func() {
		if err != nil {
			_ = v.RemoveCheckout(context.WithoutCancel(ctx), dir)
		}
	}()
	settled, err := mergeIn(ctx, v, dir, s)
	if err != nil {
		return Candidate{}, err
	}
	commit, err := v.Commit(ctx, dir, CheckoutCommit{CommitMeta: queueMeta("merge queue: candidate #" + c.ID)})
	if err != nil {
		return Candidate{}, err
	}
	touched, err := v.DiffTrees(ctx, s.onto, commit)
	if err != nil {
		return Candidate{}, err
	}
	touched = slices.Compact(slices.Sorted(slices.Values(slices.Concat(touched, settled))))
	if s.regenerate != nil {
		if commit, err = regenerateIn(ctx, v, dir, s, commit, touched); err != nil {
			return Candidate{}, err
		}
	}
	return Candidate{Commit: commit, Dir: dir}, nil
}

// mergeIn merges the change into dir and settles the generated files it conflicts in,
// returning them.
func mergeIn(ctx context.Context, v VCS, dir string, s candidateSpec) ([]string, error) {
	c := s.change
	if err := v.StartMerge(ctx, dir, c.Head); err != nil {
		return nil, err
	}
	conflicts, err := v.Conflicts(ctx, dir)
	if err != nil || len(conflicts) == 0 {
		return nil, err
	}
	paths := make([]string, len(conflicts))
	for i, cp := range conflicts {
		paths[i] = cp.Path
	}
	src, err := sources(ctx, v, s.baseCommit, paths)
	if err != nil {
		return nil, err
	}
	if len(src) > 0 {
		if err := v.AbortMerge(ctx, dir); err != nil {
			return nil, err
		}
		return nil, &ConflictError{Conflict: Conflict{Change: c, Paths: src, With: with(ctx, v, s.onto, c.Head, src)}}
	}
	var content, deleted []string
	for _, cp := range conflicts {
		if cp.Deleted {
			deleted = append(deleted, cp.Path)
		} else {
			content = append(content, cp.Path)
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

// regenerateIn runs the hook on the generated files among touched and commits what it
// rewrote. A rewrite of anything baseCommit does not mark generated is refused: a
// candidate adds only regenerated files to what was merged.
func regenerateIn(ctx context.Context, v VCS, dir string, s candidateSpec, commit string, touched []string) (string, error) {
	gen, err := v.GeneratedPaths(ctx, s.baseCommit, touched)
	if err != nil {
		return "", err
	}
	regen := slices.DeleteFunc(slices.Clone(touched), func(p string) bool { return !gen[p] })
	if len(regen) == 0 {
		return commit, nil
	}
	if err := s.regenerate(ctx, dir, s.onto, s.change, regen); err != nil {
		return "", err
	}
	written, err := v.DirtyFiles(ctx, dir)
	if err != nil || len(written) == 0 {
		return commit, err
	}
	stray, err := sources(ctx, v, s.baseCommit, written)
	if err != nil {
		return "", err
	}
	if len(stray) > 0 {
		return "", &RefusedError{Reason: "regeneration wrote files that are not generated: " + strings.Join(stray, ", "), Paths: stray}
	}
	return v.Commit(ctx, dir, CheckoutCommit{CommitMeta: queueMeta("regenerate generated files"), Paths: written})
}

func queueMeta(msg string) CommitMeta {
	return CommitMeta{Message: msg, Author: queueIdentity, Committer: queueIdentity, Date: candidateDate}
}

// predict is the tree the base carries once the candidate built onto onto lands on tip:
// the candidate's changes since onto, merged onto tip with onto as the merge base. Onto,
// not any older commit, because a candidate's delta is defined against what it was built
// on: a change stacked on a squashed one deletes a line the one beneath it added, and
// from an older base that deletion reads as never having happened. A path both the
// candidate and what reached tip since onto changed is a combination nobody validated,
// reported as a *ConflictError naming c.
func predict(ctx context.Context, v VCS, tip, onto, cand string, c Change) (string, error) {
	if tip == onto {
		return v.TreeID(ctx, cand)
	}
	merged, err := v.DiffTrees(ctx, onto, tip)
	if err != nil {
		return "", err
	}
	built, err := v.DiffTrees(ctx, onto, cand)
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
		return "", &ConflictError{Conflict: Conflict{Change: c, Paths: both}}
	}
	r, err := v.MergeTrees(ctx, TreeMerge{Base: onto, Ours: tip, Theirs: cand})
	if err != nil {
		return "", err
	}
	if len(r.Conflicts) > 0 {
		return "", fmt.Errorf("merging %s onto %s conflicted in %s, which neither side changed alone", short(cand), short(tip), strings.Join(r.Conflicts, ", "))
	}
	return r.Tree, nil
}

// regenerationProof is a merge of the base into a change whose tree differs from the
// plain merge only in generated files. Its review covers the commit beneath it only once
// regenerating Paths in a checkout of Commit, as merged onto Onto, rewrites nothing.
type regenerationProof struct {
	Commit, Onto string
	Paths        []string
}

// reviewTarget is the commit a review of head covers: head itself, or, walking back
// through merges of the base branch into the change (GitHub's "Update branch", or an
// update commit the queue pushed), the commit beneath each one that adds nothing a
// reviewer did not see. A merge adds nothing when its tree is the plain merge of its
// parents, or, for a change stacked on stackBase, their merge from stackBase. A merge
// differing only in files baseCommit marks generated adds nothing only if regeneration
// reproduces them, which only a step that runs regeneration can prove, so those are
// returned as proofs owed. A generated mark alone exempts nothing: a vendored tree marked
// generated would otherwise ride in unreviewed.
func reviewTarget(ctx context.Context, v VCS, baseCommit, tip, head, stackBase string) (string, []regenerationProof, error) {
	var owed []regenerationProof
	for range reviewDepth {
		cm, err := v.FindCommit(ctx, head)
		if err != nil {
			return "", nil, err
		}
		if len(cm.Parents) != 2 {
			return head, owed, nil
		}
		first, second := cm.Parents[0], cm.Parents[1]
		onBase, err := v.IsAncestor(ctx, second, tip)
		if err != nil {
			return "", nil, err
		}
		if !onBase {
			return head, owed, nil
		}
		tree, err := v.TreeID(ctx, head)
		if err != nil {
			return "", nil, err
		}
		plain, err := v.MergeTrees(ctx, TreeMerge{Ours: second, Theirs: first})
		if err != nil {
			return "", nil, err
		}
		if len(plain.Conflicts) == 0 && plain.Tree == tree {
			head = first
			continue
		}
		if stackBase != "" {
			stacked, err := v.MergeTrees(ctx, TreeMerge{Base: stackBase, Ours: second, Theirs: first})
			if err != nil {
				return "", nil, err
			}
			if len(stacked.Conflicts) == 0 && stacked.Tree == tree {
				head = first
				continue
			}
			plain = stacked
		}
		diff, err := v.DiffTrees(ctx, plain.Tree, tree)
		if err != nil {
			return "", nil, err
		}
		src, err := sources(ctx, v, baseCommit, diff)
		if err != nil {
			return "", nil, err
		}
		if len(src) > 0 || len(diff) == 0 {
			return head, owed, nil
		}
		owed = append(owed, regenerationProof{Commit: head, Onto: second, Paths: diff})
		head = first
	}
	return head, owed, nil
}

// forkPoint is where x left tip's history: the one commit outside x's own commits that
// they sit on, x itself when tip carries it, or "" when x merged tip's history in more
// than once, which no rebase moves cleanly.
func forkPoint(ctx context.Context, v VCS, tip, x string) (string, error) {
	own, err := v.RangeCommits(ctx, tip, x, nil)
	if err != nil || len(own) == 0 {
		return x, err
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
				return "", nil
			}
		}
	}
	return fork, nil
}

// trivialRebase reports whether now is old rebased from oldBase onto newBase with
// nothing else changed: old's whole delta since oldBase, replayed onto newBase, merges
// without a conflict into exactly now's tree.
func trivialRebase(ctx context.Context, v VCS, oldBase, old, newBase, now string) (bool, error) {
	if oldBase == "" || newBase == "" {
		return false, nil
	}
	r, err := v.MergeTrees(ctx, TreeMerge{Base: oldBase, Ours: newBase, Theirs: old})
	if err != nil || len(r.Conflicts) > 0 {
		return false, err
	}
	tree, err := v.TreeID(ctx, now)
	if err != nil {
		return false, err
	}
	return r.Tree == tree, nil
}
