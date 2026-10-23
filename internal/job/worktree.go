package job

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// binaryName is the file a workspace runs its own magus from, at its root.
const binaryName = "magus"

// MirroredBinary is the magus a fork places in the checkout it makes: exe when it is the
// forking checkout's own ./magus, so the job's hooks judge with the binary its forker
// runs, and "" when exe is anywhere else, such as on PATH, where the job finds it too.
func MirroredBinary(exe, root string) string {
	if exe == "" || root == "" {
		return ""
	}
	ran, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	local, err := os.Stat(filepath.Join(root, binaryName))
	if err != nil || !os.SameFile(ran, local) {
		return ""
	}
	return exe
}

// ForkWorktree makes the checkout a fork of job id names at dir, from the revision
// checkpoint names (the forking checkout's when it names none), and places binary in it when binary is
// not "". binary is the running magus (see [MirroredBinary]), whose build ctx carries, and it
// is placed only in a checkout at the commit it was built at, or at one that differs from it
// in no file a binary holds (see [buildInputsDiffer]): anywhere else its guard cannot
// load the tree, so the checkout is made with no binary and wt.Binary stays "" for
// [CheckoutBootstrap] to fill. made is false when the job already owns the checkout at dir,
// which is returned as it stands. A job owning another standing checkout, a checkpoint
// carrying uncommitted changes the new checkout would not hold, a binary whose commit is
// unrecorded, unknown to the repository or carries uncommitted changes, and a version
// control that cannot make one are refused.
//
// Nothing is written to the store: the caller records the result once the fork lands, and
// discards it with [Store.DiscardWorktree] when the fork is refused.
func (s *Store) ForkWorktree(ctx context.Context, id, dir, checkpoint, binary string) (wt types.JobWorktree, made bool, err error) {
	dir = filepath.Clean(dir)
	rows, err := s.List()
	if err != nil {
		return types.JobWorktree{}, false, err
	}
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id }); i >= 0 && rows[i].Worktree.Standing() {
		owned := *rows[i].Worktree
		if samePath(owned.Root, dir) {
			owned.Kept = ""
			return owned, false, nil
		}
		return types.JobWorktree{}, false, fmt.Errorf("job: %s already owns the checkout %s, which magus removes once the job ends, so fork it with `--checkout %s` or let it end first",
			id, owned.Root, owned.Root)
	}
	revision, digest := types.ParseCheckpointToken(checkpoint)
	if digest != "" {
		return types.JobWorktree{}, false, fmt.Errorf("job: %s's checkpoint %s carries uncommitted changes, which a new checkout made at %s would not hold: commit them, then fork at the commit",
			id, checkpoint, revision)
	}
	if revision == "" {
		// A job that names no checkpoint starts from what its forker is on; the forker's
		// uncommitted changes stay where they are.
		token, err := s.CheckoutToken(ctx)
		if err != nil {
			return types.JobWorktree{}, false, fmt.Errorf("%s names a checkout and no checkpoint, and the forking checkout's revision cannot be read: %w", id, err)
		}
		revision, _ = types.ParseCheckpointToken(token)
	}
	source, err := filepath.Abs(s.root)
	if err != nil {
		return types.JobWorktree{}, false, err
	}
	drv, why := resolveDriver(ctx, source)
	if drv == nil {
		return types.JobWorktree{}, false, fmt.Errorf("job: %s names a checkout, and %s", id, why)
	}
	base, err := drv.FindCommit(ctx, source, revision)
	if err != nil {
		return types.JobWorktree{}, false, fmt.Errorf("job: %s's checkout starts at %s, which %s cannot find: %w", id, revision, drv.Name(), err)
	}
	if binary != "" {
		build := types.MagusBuildFromContext(ctx)
		switch {
		case build.Commit == "" || build.Commit == "unknown":
			return types.JobWorktree{}, false, fmt.Errorf("job: %s's checkout would get %s, which records no commit it was built from, so whether it loads the checkout at %s cannot be told: rebuild ./magus with `magus run go-build .`",
				id, binary, base.ID)
		case strings.HasSuffix(build.Version, "-dirty"):
			return types.JobWorktree{}, false, fmt.Errorf("job: %s's checkout would get %s, built from changes to %s nobody committed, which a checkout at %s would not hold: commit them and rebuild ./magus first",
				id, binary, build.Commit, base.ID)
		}
		built, err := drv.FindCommit(ctx, source, build.Commit)
		if err != nil {
			return types.JobWorktree{}, false, fmt.Errorf("job: %s's checkout would get %s, built at %s, which %s cannot find: %w", id, binary, build.Commit, drv.Name(), err)
		}
		// Built elsewhere, its guard could not load the checkout unless nothing it compiles
		// differs; CheckoutBootstrap builds one there instead.
		if built.ID != base.ID && buildInputsDiffer(ctx, drv, source, built.ID, base.ID) {
			binary = ""
		}
	}
	if err := drv.CreateCheckout(ctx, source, dir, base.ID); err != nil {
		return types.JobWorktree{}, false, fmt.Errorf("job: %s's checkout at %s: %w", id, dir, err)
	}
	wt = types.JobWorktree{Root: dir, Source: source, Base: base.ID, Created: time.Now().Unix()}
	if binary != "" {
		placed, err := placeBinary(binary, dir)
		if err != nil {
			return types.JobWorktree{}, false, errors.Join(fmt.Errorf("placing %s in %s: %w", binary, dir, err), drv.RemoveCheckout(ctx, source, dir))
		}
		wt.Binary = placed
	}
	return wt, true, nil
}

// buildInputsDiffer reports whether a binary built at built could differ from one built at
// base. Every changed path counts except a test file and the changelog fragments, which no
// binary holds; a diff that cannot be read counts as differing, so the caller prints the
// bootstrap line instead of placing a binary on a guess.
func buildInputsDiffer(ctx context.Context, drv types.VCSDriver, source, built, base string) bool {
	// RangeFiles measures from the merge base, so both directions cover the two commits
	// whichever is the ancestor.
	for _, pair := range [][2]string{{built, base}, {base, built}} {
		changed, err := drv.RangeFiles(ctx, source, pair[0], pair[1], nil)
		if err != nil {
			return true
		}
		for _, p := range changed {
			if !strings.HasSuffix(p, "_test.go") && !strings.HasPrefix(p, "changes/") {
				return true
			}
		}
	}
	return false
}

// CheckoutBootstrapScript builds ./magus in a checkout at the checkout's own revision.
const CheckoutBootstrapScript = "hack/dev/bootstrap-checkout.buzz"

// CheckoutBootstrap is the command that builds ./magus in dir, a checkout ForkWorktree made
// at a commit no placed binary was built at.
func CheckoutBootstrap(dir string) string {
	return "./magus buzz " + CheckoutBootstrapScript + " -- --checkout " + dir
}

// DiscardWorktree removes a checkout [Store.ForkWorktree] just made for a fork that was then
// refused, before anything ran in it.
func (s *Store) DiscardWorktree(ctx context.Context, wt types.JobWorktree) error {
	drv, why := resolveDriver(ctx, wt.Source)
	if drv == nil {
		return fmt.Errorf("job: the checkout %s stays, since %s", wt.Root, why)
	}
	return drv.RemoveCheckout(ctx, wt.Source, wt.Root)
}

// placeBinary copies bin into dir by temp and rename, never in place: on darwin a later
// exec of an executable rewritten in place is killed.
func placeBinary(bin, dir string) (string, error) {
	body, err := os.ReadFile(bin)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, binaryName)
	return dst, file.WriteFileAtomic(dst, body, 0o755)
}

// RecordWorktree records wt as the checkout job id owns.
func (s *Store) RecordWorktree(ctx context.Context, id string, wt types.JobWorktree) (types.Job, error) {
	return s.writeWorktree(ctx, id, func(w *types.JobWorktree) { *w = wt })
}

// writeWorktree rewrites the worktree record of row id, the one field no declaration
// carries: see [types.Job.Worktree].
func (s *Store) writeWorktree(ctx context.Context, id string, fn func(*types.JobWorktree)) (types.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var stored types.Job
	err := s.update(ctx, func(f *jobsFile) error {
		i := slices.IndexFunc(f.Jobs, func(r types.Job) bool { return r.ID == id })
		if i < 0 {
			return fmt.Errorf("%w: %q", ErrUnknownJob, id)
		}
		if err := readOnly(f.Jobs[i]); err != nil {
			return err
		}
		row := f.Jobs[i].Clone()
		if row.Worktree == nil {
			row.Worktree = &types.JobWorktree{}
		}
		fn(row.Worktree)
		f.Jobs[i] = row
		stored = row.Clone()
		return nil
	})
	return stored, err
}

// WorkingCheckout is where row's holder works: the checkout it owns, else root. A row
// forked with --checkout is taken there and nowhere else (see [WrongCheckout]).
func WorkingCheckout(root string, row types.Job) string {
	if row.Worktree.Standing() {
		return row.Worktree.Root
	}
	return root
}

// WrongCheckout refuses taking row anywhere but the checkout it owns, nil when it owns
// none or here is that checkout.
func WrongCheckout(row types.Job, here string) error {
	if !row.Worktree.Standing() || samePath(row.Worktree.Root, here) {
		return nil
	}
	return heldError{fmt.Sprintf("job: %s owns the checkout %s, which its fork made for it, and is taken there and nowhere else: run `%s` in %s",
		row.ID, row.Worktree.Root, hint.JobExec.With(row.ID), row.Worktree.Root)}
}

// EndWorktrees removes the checkout each ended job owns, once removing it loses nothing,
// and records on the row that it did, or why it kept it. It returns the rows whose record
// changed. A checkout this store's own workspace sits in is kept, saying so, for a later
// call from elsewhere, since removing it would pull the directory out from under the caller.
// It is [Store.EndCheckouts] restoring nothing and given no ended job.
func (s *Store) EndWorktrees(ctx context.Context) ([]types.Job, error) {
	return s.EndCheckouts(ctx, nil)
}

// removeWorktree removes wt, the checkout ended job id worked in, and returns "", or
// returns why it may not: it holds a change nobody committed, or a commit nothing but it
// names. A fact that cannot be read keeps it. With written, what magus wrote in it is
// restored first (see [restoreWritten]).
func removeWorktree(ctx context.Context, id string, wt types.JobWorktree, written WrittenPaths) string {
	drv, why := resolveDriver(ctx, wt.Source)
	if drv == nil {
		return "whether it holds work cannot be read: " + why
	}
	if _, err := os.Stat(wt.Root); errors.Is(err, fs.ErrNotExist) {
		if err := drv.RemoveCheckout(ctx, wt.Source, wt.Root); err != nil {
			return "its directory is gone, and its registration cannot be removed: " + err.Error()
		}
		return ""
	}
	dirty, err := drv.DirtyFiles(ctx, wt.Root, nil)
	if err != nil {
		return "its status cannot be read: " + err.Error()
	}
	dirty = slices.DeleteFunc(dirty, func(p string) bool { return wt.Binary != "" && p == binaryName })
	if len(dirty) > 0 && written != nil {
		restored, err := restoreWritten(ctx, drv, wt.Root, dirty, written)
		if err != nil {
			return "restoring what magus wrote in it failed: " + err.Error()
		}
		dirty = slices.DeleteFunc(dirty, func(p string) bool { return slices.Contains(restored, p) })
	}
	if len(dirty) > 0 {
		return fmt.Sprintf("%d path(s) in it hold uncommitted work (%s): commit them on a branch or hand them back, and `%s` removes it",
			len(dirty), listPaths(dirty), hint.JobWait.With(id))
	}
	var bases []string
	if wt.Base != "" {
		bases = append(bases, wt.Base)
	}
	stranded, err := drv.StrandedRevisions(ctx, wt.Root, bases...)
	if err != nil {
		return "which of its commits a branch or bookmark holds cannot be read: " + err.Error()
	}
	if len(stranded) > 0 {
		return fmt.Sprintf("%d commit(s) in it, newest %.12s, are on no branch, bookmark, remote or its base %.12s; name them with a branch or bookmark", len(stranded), stranded[0], wt.Base)
	}
	if err := drv.RemoveCheckout(ctx, wt.Source, wt.Root); err != nil {
		return "removing it failed: " + err.Error()
	}
	return ""
}

// samePath compares two absolute paths by where they land, symlinks resolved.
func samePath(a, b string) bool {
	return a != "" && b != "" && resolved(a) == resolved(b)
}

// within reports whether p is root or lies under it, symlinks resolved.
func within(p, root string) bool {
	rel, err := filepath.Rel(resolved(root), resolved(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
