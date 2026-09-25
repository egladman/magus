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

	"github.com/egladman/magus/internal/merge3"
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
	writes, err := f.Classify(ctx, paths)
	if err != nil {
		return nil, fmt.Errorf("classify: %w", err)
	}
	return slices.DeleteFunc(slices.Clone(paths), func(p string) bool { return writes[p].Output }), nil
}

// outputs returns the paths some target declares as its output, in order.
func outputs(ctx context.Context, f types.BuildFacts, paths []string) ([]string, error) {
	writes, err := f.Classify(ctx, paths)
	if err != nil {
		return nil, fmt.Errorf("classify: %w", err)
	}
	return slices.DeleteFunc(slices.Clone(paths), func(p string) bool { return !writes[p].Output }), nil
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
// file no target declares as its output conflicts and auto-resolution does not settle it.
func checkMerge(ctx context.Context, v types.ReadVCS, f types.BuildFacts, root, tip string, c types.Change) error {
	mb, err := mergeBase(ctx, v, root, tip, c)
	if err != nil {
		return err
	}
	m := magustypes.TreeMerge{Base: mb, Ours: tip, Theirs: c.Head}
	r, err := v.MergeTrees(ctx, root, m)
	if err != nil {
		return err
	}
	_, src, declined, err := resolveSources(ctx, v, f, root, m, r.Conflicts)
	if err != nil {
		return err
	}
	if len(src) > 0 {
		return &conflictError{conflict: sourceConflict{change: c, paths: src, with: with(ctx, v, root, tip, c.Head, src), declined: declined}}
	}
	return nil
}

// settledSource is a conflicted source file auto-resolution settled: the merge, and the
// build tool's classification that allowed it.
type settledSource struct {
	path    string
	res     merge3.Resolution
	verdict string // the classifier's line, `<path>: <class> (<why>)`
}

// String names the path's class and why, and each region as a location with its kind.
func (s settledSource) String() string { return s.verdict + "; " + s.res.Label() }

// resolvedNote is how a report names what the queue settled.
func resolvedNote(settled []settledSource) string {
	names := make([]string, len(settled))
	for i, s := range settled {
		names[i] = s.String()
	}
	return "auto-resolved " + strings.Join(names, " | ")
}

// declinedNote is how a report names the conflicted source files auto-resolution tried
// and why each stayed conflicted, or "" when it tried none.
func declinedNote(declined []string) string {
	if len(declined) == 0 {
		return ""
	}
	return "not auto-resolved: " + strings.Join(declined, " | ")
}

// resolveSources settles what it can of the conflicts in cs that are not in generated
// files. It returns those settled, the rest, and for each of the rest it tried, why it
// declined, all in cs's order.
//
// A content conflict is merged from its three versions: at m.Ours, at m.Theirs, and at
// m.Base, or at their one merge base when m.Base is empty. It settles when merge3 settles
// every region both sides changed and the build tool allows the result
// (BuildFacts.AutoResolvable, the change classifier's low-risk classes or an opted-in
// code path). A file the base lacks stays conflicted, and a file settles only whole. The
// computation is the running queue's own, from the blobs alone, so whoever recomputes it
// from the same commits gets the same bytes.
func resolveSources(ctx context.Context, v types.ReadVCS, f types.BuildFacts, root string, m magustypes.TreeMerge, cs []magustypes.Conflict) (settled []settledSource, left, declined []string, err error) {
	if len(cs) == 0 {
		return nil, nil, nil, nil
	}
	writes, err := f.Classify(ctx, conflictPaths(cs))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("classify: %w", err)
	}
	base, looked := m.Base, m.Base != ""
	for _, c := range cs {
		if writes[c.Path].Output {
			continue
		}
		if c.Kind != magustypes.ConflictKindContent {
			left = append(left, c.Path)
			continue
		}
		if !looked {
			looked = true
			// No one base (none, or a criss-cross) leaves base empty, and every file conflicted.
			if base, _, err = v.MergeBase(ctx, root, m.Ours, m.Theirs); err != nil {
				return nil, nil, nil, err
			}
		}
		s, why, err := resolveFile(ctx, v, f, root, base, m, c.Path)
		if err != nil {
			return nil, nil, nil, err
		}
		if why != "" {
			left, declined = append(left, c.Path), append(declined, why)
			continue
		}
		settled = append(settled, s)
	}
	return settled, left, declined, nil
}

// resolveFile merges path's versions at m.Ours and m.Theirs from base's. why is non-empty
// when it does not settle, saying why.
func resolveFile(ctx context.Context, v types.ReadVCS, f types.BuildFacts, root, base string, m magustypes.TreeMerge, path string) (settledSource, string, error) {
	if base == "" {
		return settledSource{}, path + ": the sides share no one merge base", nil
	}
	was, err := v.ReadFileAt(ctx, root, base, path)
	if err != nil {
		// No capability tells a path the base lacks from a failed read. Either way the
		// file stays conflicted, the safe side.
		return settledSource{}, path + ": absent at the merge base", nil //nolint:nilerr // no base version to merge from
	}
	ours, err := v.ReadFileAt(ctx, root, m.Ours, path)
	if err != nil {
		return settledSource{}, "", err
	}
	theirs, err := v.ReadFileAt(ctx, root, m.Theirs, path)
	if err != nil {
		return settledSource{}, "", err
	}
	res, ok := merge3.Resolve(path, []byte(was), []byte(ours), []byte(theirs))
	switch {
	case !ok && len(res.Regions) == 0:
		return settledSource{}, path + ": binary, or too far from the merge base to merge by line", nil
	case !ok:
		return settledSource{}, res.Label(), nil
	}
	verdict, allowed, err := f.AutoResolvable(ctx, path, []byte(was), res.Content)
	if err != nil {
		return settledSource{}, "", fmt.Errorf("auto-resolvable %s: %w", path, err)
	}
	if !allowed {
		return settledSource{}, verdict, nil
	}
	return settledSource{path: path, res: res, verdict: verdict}, "", nil
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
	touched  []string        // the paths the merge changed or settled, sorted
	settled  []string        // the generated files it conflicted in, which took the change's side
	resolved []settledSource // the source files it conflicted in, which auto-resolution settled
}

// buildMerge checks out onto in a directory of its own, merges the change, settles
// conflicts in generated files by taking the change's side and those auto-resolution
// settles in source files, and commits. Any other source conflict is a *conflictError.
// On any error nothing is left behind.
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
	cand, err := checkout(ctx, v, root, s.scratch, "candidate-"+c.ID, from)
	if err != nil {
		return built{}, err
	}
	cand.Change = c.ID
	defer func() {
		if err != nil {
			// The build's own error is what the caller acts on.
			_ = discard(ctx, v, root, cand)
		}
	}()
	settled, resolved, err := mergeIn(ctx, v, s, cand.Dir, from)
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
	return built{Candidate: cand, touched: slices.Compact(slices.Sorted(slices.Values(slices.Concat(touched, settled)))), settled: settled,
		resolved: resolved}, nil
}

// checkout checks commit out in a directory of its own under scratch, named from name,
// beside a scratch directory private to it, so no hook run in another checkout can have
// planted anything where one run here will look. On error nothing is left behind.
func checkout(ctx context.Context, v types.BuildVCS, root, scratch, name, commit string) (types.Candidate, error) {
	box, err := os.MkdirTemp(scratch, name+"-")
	if err != nil {
		return types.Candidate{}, err
	}
	cand := types.Candidate{Commit: commit, Dir: filepath.Join(box, "checkout"), Scratch: filepath.Join(box, "scratch")}
	if err := os.Mkdir(cand.Scratch, 0o700); err != nil {
		_ = os.RemoveAll(box)
		return types.Candidate{}, err
	}
	if err := v.CreateCheckout(ctx, root, cand.Dir, commit); err != nil {
		_ = os.RemoveAll(box)
		return types.Candidate{}, err
	}
	return cand, nil
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

// mergeIn merges the change into dir, whose checkout is of ours, settles the generated
// files it conflicts in and the source files auto-resolution settles, and returns both.
func mergeIn(ctx context.Context, v types.BuildVCS, s candidateSpec, dir, ours string) ([]string, []settledSource, error) {
	c := s.change
	// The queue's own identity, as on the commit concluding it: the candidate is the
	// same commit in every job that builds it, whatever the box configures.
	if err := v.StartMerge(ctx, dir, c.Head, candidateIdentity); err != nil {
		return nil, nil, err
	}
	conflicts, err := v.Conflicts(ctx, dir)
	if err != nil || len(conflicts) == 0 {
		return nil, nil, err
	}
	resolved, src, declined, err := resolveSources(ctx, v, s.facts, dir, magustypes.TreeMerge{Ours: ours, Theirs: c.Head}, conflicts)
	if err != nil {
		return nil, nil, err
	}
	if len(src) > 0 {
		if err := v.AbortMerge(ctx, dir); err != nil {
			return nil, nil, err
		}
		return nil, nil, &conflictError{conflict: sourceConflict{change: c, paths: src, with: with(ctx, v, s.clone.Root, s.onto, c.Head, src),
			declined: declined}}
	}
	if err := writeResolved(ctx, v, dir, resolved); err != nil {
		return nil, nil, err
	}
	var generated, content, deleted []string
	for _, cf := range conflicts {
		if slices.ContainsFunc(resolved, func(r settledSource) bool { return r.path == cf.Path }) {
			continue
		}
		generated = append(generated, cf.Path)
		if cf.Kind == magustypes.ConflictKindContent {
			content = append(content, cf.Path)
		} else {
			deleted = append(deleted, cf.Path)
		}
	}
	if len(content) > 0 {
		if err := v.KeepIncoming(ctx, dir, content); err != nil {
			return nil, nil, err
		}
		if err := v.MarkResolved(ctx, dir, content); err != nil {
			return nil, nil, err
		}
	}
	if len(deleted) > 0 {
		if err := v.RemoveConflicts(ctx, dir, deleted); err != nil {
			return nil, nil, err
		}
	}
	return generated, resolved, nil
}

// writeResolved writes each settled file into the checkout at dir and marks it resolved.
// Only a regular file is written: through a symlink the change controls, the write would
// land wherever the link points.
func writeResolved(ctx context.Context, v types.BuildVCS, dir string, resolved []settledSource) error {
	if len(resolved) == 0 {
		return nil
	}
	paths := make([]string, len(resolved))
	for i, r := range resolved {
		abs := filepath.Join(dir, filepath.FromSlash(r.path))
		info, err := os.Lstat(abs)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return &types.RefusedError{Paths: []string{r.path}, Reason: r.path + " conflicts and is opted into auto-resolution, but is not a regular file",
				Remedy: "Merge the base branch in and resolve it by hand."}
		}
		if err := os.WriteFile(abs, r.res.Content, info.Mode().Perm()); err != nil {
			return err
		}
		paths[i] = r.path
	}
	return v.MarkResolved(ctx, dir, paths)
}

// regenerateIn runs regenerate on the generated files among b.touched and commits what
// it rewrote. A candidate adds only declared writes to what was merged: a rewrite of
// anything the build tool does not declare it writes is refused. A file the build tool
// maintains is put back as the candidate holds it and left out of the commit, since the
// build tool running here is the base's, and its rewrite would undo the change's.
func regenerateIn(ctx context.Context, v types.BuildVCS, s candidateSpec, b built, regenerate types.RegenerateFunc, units []string) (string, error) {
	regen, err := outputs(ctx, s.facts, b.touched)
	if err != nil {
		return "", err
	}
	keep, err := regenerateWrites(ctx, v, s, b, regenerate, regen, units)
	if err != nil || len(keep) == 0 {
		return b.Commit, err
	}
	return commitRegenerated(ctx, v, b, keep)
}

// regenerateWrites runs regenerate on regen in b's checkout and returns the declared files
// it rewrote, uncommitted. A file the build tool maintains is put back, and a write
// nothing declares is refused.
func regenerateWrites(ctx context.Context, v types.BuildVCS, s candidateSpec, b built, regenerate types.RegenerateFunc, regen, units []string) ([]string, error) {
	if len(regen) == 0 {
		return nil, nil
	}
	if err := regenerate(ctx, types.Regeneration{Dir: b.Dir, Scratch: b.Scratch, Change: s.change, Paths: regen, Units: units}); err != nil {
		return nil, err
	}
	written, err := v.DirtyFiles(ctx, b.Dir, nil)
	if err != nil || len(written) == 0 {
		return nil, err
	}
	writes, err := s.facts.Classify(ctx, written)
	if err != nil {
		return nil, fmt.Errorf("classify: %w", err)
	}
	var keep, stray []string
	for _, p := range written {
		switch w := writes[p]; {
		case w.Maintained:
			if err := restore(ctx, v, b.Candidate, p); err != nil {
				return nil, err
			}
		case w.Declared():
			keep = append(keep, p)
		default:
			stray = append(stray, p)
		}
	}
	if len(stray) > 0 {
		return nil, &types.RefusedError{Reason: "regeneration wrote files nothing declares it writes: " + strings.Join(stray, ", "), Paths: stray}
	}
	return keep, nil
}

func commitRegenerated(ctx context.Context, v types.BuildVCS, b built, keep []string) (string, error) {
	return v.Commit(ctx, b.Dir, magustypes.CheckoutCommit{CommitMeta: queueMeta("regenerate generated files"), Paths: keep})
}

// generationOf asks the build tool what regenerating outputs runs, against every file c
// changed since baseCommit.
func generationOf(ctx context.Context, v types.ReadVCS, f types.BuildFacts, root, baseCommit string, c types.Change, outputs []string) (types.Generation, error) {
	changed, err := v.RangeFiles(ctx, root, baseCommit, c.Head, nil)
	if err != nil {
		return types.Generation{}, err
	}
	g, err := f.Generation(ctx, outputs, changed)
	if err != nil {
		return types.Generation{}, fmt.Errorf("generation of %s: %w", joinPaths(outputs), err)
	}
	return g, nil
}

// unprovenWhy says why g does not prove a regeneration runs none of a change's code, and
// the paths that say so, falling back to outputs.
func unprovenWhy(g types.Generation, outputs []string) (string, []string) {
	why, paths := g.Unbounded, g.Code
	if why == "" {
		why = "it changes code their regeneration runs"
	}
	if len(paths) == 0 {
		paths = outputs
	}
	return why, paths
}

// restore writes path in cand's checkout back to its content at cand's commit.
func restore(ctx context.Context, v types.BuildVCS, cand types.Candidate, path string) error {
	content, err := v.ReadFileAt(ctx, cand.Dir, cand.Commit, path)
	if err != nil {
		return fmt.Errorf("restore %s: %w", path, err)
	}
	abs := filepath.Join(cand.Dir, filepath.FromSlash(path))
	mode := os.FileMode(0o644)
	if info, err := os.Stat(abs); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(abs, []byte(content), mode)
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
	// declined says, for each of paths auto-resolution tried, why it stayed conflicted:
	// the locations merge3 could not settle, or the classifier's line.
	declined []string
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
