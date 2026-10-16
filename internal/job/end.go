package job

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// endSource is the attention source of the request an end files for a person to confirm
// it takes down live descendants that hold work.
const endSource = "job/end"

// WrittenPaths reports which of paths, relative to the checkout at root, the workspace
// there declares as a target's output or that magus maintains itself: the `magus
// describe file` roles output and maintained. Ending a job restores those before judging
// whether its checkout holds work. The caller supplies it because loading a workspace
// lives above this package.
type WrittenPaths func(ctx context.Context, root string, paths []string) ([]string, error)

// descendant is a live row below one an end names, with the work it holds.
type descendant struct {
	row types.Job
	// uncommitted are the paths inside its write paths that its checkout has not committed.
	uncommitted []string
	// unread is why its checkout's status could not be read, "" when it was.
	unread string
}

// describeDescendant reads the work row holds in its checkout. A row nobody took has no
// checkout and holds none.
func describeDescendant(ctx context.Context, row types.Job) descendant {
	d := descendant{row: row}
	if where := heldCheckout(row); where != "" {
		d.uncommitted, d.unread = uncommitted(ctx, where, row)
	}
	return d
}

// holdsWork reports whether ending d would discard work only a person may discard. A
// status that cannot be read counts: nothing shows the checkout is clean.
func (d descendant) holdsWork() bool {
	return len(d.uncommitted) > 0 || d.unread != "" || runningCheck(d.row) != ""
}

func (d descendant) String() string {
	row := d.row
	where := "never taken"
	if at := heldCheckout(row); at != "" {
		where = "held in " + at
	}
	s := fmt.Sprintf("%s (%s, %s holder, %s)", row.ID, row.State, row.Holder.OrSession(), where)
	var work []string
	if n := len(d.uncommitted); n > 0 {
		work = append(work, fmt.Sprintf("%d uncommitted path(s) in its write paths, first %s", n, d.uncommitted[0]))
	}
	if d.unread != "" {
		work = append(work, d.unread)
	}
	if inv := runningCheck(row); inv != "" {
		work = append(work, "its check is still running as invocation "+inv)
	}
	if len(work) == 0 {
		return s
	}
	return s + ": " + strings.Join(work, "; ")
}

// heldCheckout is the checkout row's holder works in: the one its fork made, else the one
// it was taken in, "" when nobody took it.
func heldCheckout(row types.Job) string {
	if row.Worktree.Standing() {
		return row.Worktree.Root
	}
	return row.CheckoutRoot
}

// runningCheck is the invocation of row's run that started and has not ended, "" for none.
func runningCheck(row types.Job) string {
	if r := row.LastRun; r != nil && r.Invocation != "" && r.Ended == 0 {
		return r.Invocation
	}
	return ""
}

// uncommitted are the paths inside row's write paths that the checkout at root holds
// uncommitted, and why its status could not be read when it could not. A directory no
// version control answers in holds nothing to commit.
func uncommitted(ctx context.Context, root string, row types.Job) ([]string, string) {
	drv, _ := resolveDriver(ctx, root)
	if drv == nil {
		return nil, ""
	}
	if _, err := drv.Root(ctx, root); err != nil {
		return nil, ""
	}
	dirty, err := drv.DirtyFiles(ctx, root, nil)
	if err != nil {
		return nil, "the status of " + root + " cannot be read: " + err.Error()
	}
	return slices.DeleteFunc(dirty, func(p string) bool {
		return !slices.ContainsFunc(row.WritePaths, func(w string) bool { return covers(w, p) })
	}), ""
}

// liveBelow are the live rows under id. With result, the end keeps id live, so only the
// rows still editing hold it; without one, id ends and every live row under it ends with it.
func liveBelow(rows []types.Job, id string, result bool) []types.Job {
	return slices.DeleteFunc(types.JobDescendants(rows, id), func(r types.Job) bool {
		if result {
			return !r.State.Editing()
		}
		return !r.State.Live()
	})
}

// refuseLiveDescendants refuses ending id while live rows below it would end unseen, or,
// when result is set, while rows below it are still working.
func refuseLiveDescendants(ctx context.Context, rows []types.Job, id string, result bool) error {
	live := liveBelow(rows, id, result)
	if len(live) == 0 {
		return nil
	}
	descs := make([]descendant, len(live))
	for i, r := range live {
		descs[i] = describeDescendant(ctx, r)
	}
	if result {
		return fmt.Errorf("job: %s's result is refused: %d job(s) below it are still working: %s: collect each with `%s` or end it with `%s`, then file the result",
			id, len(descs), joinDescendants(descs), hint.JobWait.With("<id>"), hint.JobExit.With("<id>"))
	}
	return unnamedError(id, descs)
}

// unnamedError refuses ending id, which would end the live rows in descs with it.
func unnamedError(id string, descs []descendant) error {
	ids := []string{id}
	for _, d := range descs {
		ids = append(ids, d.row.ID)
	}
	return fmt.Errorf("job: ending %s would end %d live job(s) below it: %s: name each to end it too: `%s`",
		id, len(descs), joinDescendants(descs), hint.JobExit.With(ids...))
}

func joinDescendants(descs []descendant) string {
	parts := make([]string, len(descs))
	for i, d := range descs {
		parts[i] = d.String()
	}
	return strings.Join(parts, "; ")
}

// Abandon ends each of ids as no_return, as [Exit] does with no result, every descendant
// before its ancestors, and returns the rows it ended and one error for each id it did not.
//
// A job whose live descendants are not all among ids is refused, naming each: ending it
// would end them unseen. Restating them in ids ends them too, unless one of them holds
// uncommitted work in its write paths or a running check. Then the end needs a person, who
// disposes the attention request the refusal names (the guard denies an agent that), and
// the same call proceeds afterwards.
func Abandon(ctx context.Context, store *Store, ids []string) ([]types.Job, []error) {
	rows, err := store.List()
	if err != nil {
		return nil, []error{err}
	}
	var unique []string
	for _, id := range ids {
		if !slices.Contains(unique, id) {
			unique = append(unique, id)
		}
	}
	ids = unique
	var refused []error
	blocked := map[string]bool{}
	var holding []descendant
	for _, id := range ids {
		var unnamed []descendant
		for _, r := range liveBelow(rows, id, false) {
			d := describeDescendant(ctx, r)
			switch {
			case !slices.Contains(ids, r.ID):
				unnamed = append(unnamed, d)
			case d.holdsWork() && !slices.ContainsFunc(holding, func(h descendant) bool { return h.row.ID == r.ID }):
				holding = append(holding, d)
			}
		}
		if len(unnamed) > 0 {
			refused = append(refused, unnamedError(id, unnamed))
			blocked[id] = true
		}
	}
	if len(holding) > 0 {
		if err := store.confirmEnd(ctx, ids, holding); err != nil {
			refused = append(refused, err)
			for _, d := range holding {
				blocked[d.row.ID] = true
				for _, a := range types.JobAncestors(rows, d.row.ID) {
					blocked[a.ID] = blocked[a.ID] || slices.Contains(ids, a.ID)
				}
			}
		}
	}
	order := slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return blocked[id] })
	slices.SortStableFunc(order, func(a, b string) int {
		return cmp.Compare(len(types.JobAncestors(rows, b)), len(types.JobAncestors(rows, a)))
	})
	var ended []types.Job
	for _, id := range order {
		stored, err := Exit(ctx, store, id, nil, nil)
		if err != nil {
			refused = append(refused, err)
			continue
		}
		ended = append(ended, stored)
	}
	return ended, refused
}

// confirmEnd is nil once a person disposed the attention request asking to end ids, which
// takes down the descendants in holding and the work they hold. Until then it files that
// request and refuses. The request's identity is the ids alone, so every attempt at the
// same end lands on one request; a dry-run store files none.
func (s *Store) confirmEnd(ctx context.Context, ids []string, holding []descendant) error {
	ending := slices.Sorted(slices.Values(ids))
	held := make([]string, len(holding))
	var files []types.FileRef
	for i, d := range holding {
		held[i] = d.row.ID
		for _, p := range d.uncommitted {
			files = append(files, types.FileRef{Value: path.Join(heldCheckout(d.row), p)})
		}
	}
	slices.Sort(held)
	dir, err := sessions.Dir(s.root)
	if err != nil {
		return fmt.Errorf("job: ending %s takes down work only a person may discard, and the attention queue that asks one cannot be placed: %w", strings.Join(ending, " "), err)
	}
	open := sessions.AttentionOpen{
		Outcome:  string(types.OutcomePermission),
		Severity: string(types.SeverityWarning),
		Source:   endSource,
		Where:    strings.Join(ending, " "),
		Lease:    s.Actor().Lease,
		Files:    files,
		Message: fmt.Sprintf("ending %s also ends %s, which hold uncommitted work or a running check; a person disposes this request (`%s`) to confirm, and the same end then proceeds",
			strings.Join(ending, " "), strings.Join(held, " "), hint.SessionDispose.With("<request>")),
	}
	id := sessions.RequestID(endSource, open)
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return fmt.Errorf("job: ending %s needs a person's confirmation, which cannot be read: %w", open.Where, err)
	}
	if slices.ContainsFunc(sessions.Attention(fold), func(r sessions.AttentionRequest) bool { return r.ID == id && r.Disposed }) {
		return nil
	}
	if !s.dryRun {
		if _, _, err := sessions.OpenRequest(dir, endSource, open, sessions.InvocationStart{Origin: trail.LocalOrigin(ctx), Workspace: s.root}); err != nil {
			return fmt.Errorf("job: ending %s needs a person's confirmation, and the request for one cannot be filed: %w", open.Where, err)
		}
	}
	return fmt.Errorf("job: ending %s takes down work only a person may discard: %s: a person disposes attention request %s (`%s`), which an agent may not, then the same end proceeds",
		open.Where, joinDescendants(holding), id, hint.SessionDispose.With(id))
}

// treeEndings are the rows of out that end only because an ancestor in out ends: neither
// the sweep nor prune ends them on their own as rows stand.
func (w *sweeper) treeEndings(rows []types.Job, out []Ending, window time.Duration) ([]types.Job, error) {
	flagged := types.JobList{Jobs: rows}.Flag(w.now, window)
	x := indexRows(rows)
	var taken []types.Job
	for _, e := range out {
		row := x.byID[e.ID]
		reason, err := w.reason(x, rows, row)
		if err != nil {
			return nil, err
		}
		if reason == "" && w.pruneReason(row, flagged, window) == "" {
			taken = append(taken, row)
		}
	}
	return taken, nil
}

// refuseTreeEndings refuses a prune that ends rows only because an ancestor ends, unless
// restated names each of them, and refuses until a person confirms when one it names
// holds work (see [Store.confirmEnd]). An id in restated that names no such row is refused
// too: a restatement is of what the prune showed.
func (s *Store) refuseTreeEndings(ctx context.Context, taken []types.Job, restated []string) error {
	var unstated, holding []descendant
	for _, row := range taken {
		d := describeDescendant(ctx, row)
		switch {
		case !slices.Contains(restated, row.ID):
			unstated = append(unstated, d)
		case d.holdsWork():
			holding = append(holding, d)
		}
	}
	for _, id := range restated {
		if !slices.ContainsFunc(taken, func(r types.Job) bool { return r.ID == id }) {
			return fmt.Errorf("job: prune does not end %s with an ancestor, so there is nothing to restate: `magus job prune --dry-run` lists what it ends", id)
		}
	}
	if len(unstated) > 0 {
		ids := make([]string, len(unstated))
		for i, d := range unstated {
			ids[i] = d.row.ID
		}
		return fmt.Errorf("job: prune would end %d live job(s) only because an ancestor it ends does: %s: restate each to end it too: `magus job prune %s`",
			len(unstated), joinDescendants(unstated), strings.Join(append(slices.Clone(restated), ids...), " "))
	}
	if len(holding) == 0 {
		return nil
	}
	return s.confirmEnd(ctx, restated, holding)
}

// EndCheckouts removes the checkout each ended job owns once removing it loses nothing, as
// [Store.EndWorktrees] does, and does the same for the linked checkout of this repository
// that each job named in ended was taken in, when no other live job is held there and
// this call does not run in it. With
// written, the paths in a checkout that magus wrote, and Go files whose only change is
// gofmt's, are restored first, so they neither keep the checkout nor count as work.
//
// A fork's checkout records what became of it on its row, and the rows whose record
// changed are returned. A linked checkout no row records comes back on a copy of its row
// whose Worktree says what became of it; the store is not written for it.
func (s *Store) EndCheckouts(ctx context.Context, written WrittenPaths, ended ...string) ([]types.Job, error) {
	rows, err := s.List()
	if err != nil {
		return nil, err
	}
	here, _ := filepath.Abs(s.root)
	var changed []types.Job
	for _, row := range rows {
		if !row.State.Terminal() {
			continue
		}
		if !row.Worktree.Standing() {
			if row.Worktree != nil || !slices.Contains(ended, row.ID) {
				continue
			}
			// Ended from inside it, as a holder abandoning its own row is, nothing records
			// it for a later call to retry, so it is left as the person who made it left it.
			wt, ok := s.linkedCheckout(ctx, row)
			if !ok || (here != "" && within(here, wt.Root)) {
				continue
			}
			wt.Kept = endCheckout(ctx, rows, here, row, wt, written)
			if wt.Kept == "" {
				wt.Removed = time.Now().Unix()
			}
			copied := row.Clone()
			copied.Worktree = &wt
			changed = append(changed, copied)
			continue
		}
		wt := row.Worktree
		kept := endCheckout(ctx, rows, here, row, *wt, written)
		if kept != "" && kept == wt.Kept {
			continue
		}
		now := time.Now().Unix()
		stored, err := s.writeWorktree(ctx, row.ID, func(w *types.JobWorktree) {
			w.Kept = kept
			if kept == "" {
				w.Removed = now
			}
		})
		if err != nil {
			return changed, err
		}
		changed = append(changed, stored)
	}
	return changed, nil
}

// endCheckout removes wt, the checkout ended job row worked in, and returns "", or returns
// why it stays: this call runs in it, a live job is held in it, or it holds work.
func endCheckout(ctx context.Context, rows []types.Job, here string, row types.Job, wt types.JobWorktree, written WrittenPaths) string {
	if here != "" && within(here, wt.Root) {
		return "this call runs in it; `" + hint.JobWait.With(row.ID) + "` from another checkout removes it"
	}
	for _, other := range rows {
		if other.State.Live() && samePath(heldCheckout(other), wt.Root) {
			return fmt.Sprintf("job %s (%s) is held in it", other.ID, other.State)
		}
	}
	return removeWorktree(ctx, row.ID, wt, written)
}

// linkedCheckout is the checkout row was taken in as one to remove, when it is a separate
// linked checkout of this store's repository: registered by it and not its primary.
func (s *Store) linkedCheckout(ctx context.Context, row types.Job) (types.JobWorktree, bool) {
	if row.CheckoutRoot == "" {
		return types.JobWorktree{}, false
	}
	source, err := filepath.Abs(s.root)
	if err != nil {
		return types.JobWorktree{}, false
	}
	drv, _ := resolveDriver(ctx, source)
	if drv == nil {
		return types.JobWorktree{}, false
	}
	registered, err := drv.RegisteredCheckouts(ctx, source)
	if err != nil {
		return types.JobWorktree{}, false
	}
	i := slices.IndexFunc(registered, func(c types.RegisteredCheckout) bool { return !c.Primary && samePath(c.Root, row.CheckoutRoot) })
	if i < 0 {
		return types.JobWorktree{}, false
	}
	return types.JobWorktree{Root: row.CheckoutRoot, Source: source, Base: checkpointRevision(row.Checkpoint)}, true
}

// restoreWritten returns each of dirty, paths in the checkout at root, that magus wrote to
// what the checkout's revision holds, deleting one the revision does not track, and
// returns the paths it restored. A path is magus's when written names it, or when it is a
// Go file whose change is only gofmt's.
func restoreWritten(ctx context.Context, drv types.VCSDriver, root string, dirty []string, written WrittenPaths) ([]string, error) {
	paths, err := written(ctx, root, slices.Clone(dirty))
	if err != nil {
		return nil, err
	}
	for _, p := range dirty {
		if !slices.Contains(paths, p) && formatOnly(ctx, drv, root, p) {
			paths = append(paths, p)
		}
	}
	var restored []string
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		body, err := drv.ReadFileAt(ctx, root, "", p)
		if err != nil {
			tracked, terr := drv.TrackedFiles(ctx, root, []string{p})
			if terr != nil || len(tracked) > 0 {
				return restored, fmt.Errorf("%s: %w", p, errors.Join(err, terr))
			}
			if err := os.RemoveAll(full); err != nil {
				return restored, err
			}
			restored = append(restored, p)
			continue
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(full); err == nil {
			mode = info.Mode().Perm()
		}
		if err := file.WriteFileAtomic(full, []byte(body), mode); err != nil {
			return restored, err
		}
		restored = append(restored, p)
	}
	return restored, nil
}

// formatOnly reports whether the Go file p in the checkout at root differs from its
// committed content only as gofmt rewrites it.
func formatOnly(ctx context.Context, drv types.VCSDriver, root, p string) bool {
	if path.Ext(p) != ".go" {
		return false
	}
	committed, err := drv.ReadFileAt(ctx, root, "", p)
	if err != nil {
		return false
	}
	current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return false
	}
	formatted, err := format.Source([]byte(committed))
	return err == nil && committed != string(current) && bytes.Equal(formatted, current)
}
