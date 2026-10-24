package magus

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/vcs"
)

// readRef reads ref through read from this checkout's output store and, on a miss, from
// the other checkouts that may hold it ([Magus.searchOtherCheckouts]), with a notice
// naming the checkout that answered, since its output was not this checkout's.
func (m *Magus) readRef(ctx context.Context, ref string, read func(cacheDir string) error) error {
	err := read(resolveCacheDir(m.ws.Root, m.cfg))
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	from, err := m.searchOtherCheckouts(ctx, ref, read)
	if err == nil {
		slog.InfoContext(ctx, fmt.Sprintf("read %s from the output store of %s, another checkout of this repository", ref, from),
			attr.Notice(""), attr.Component("magus"))
	}
	return err
}

// searchOtherCheckouts calls read with the cache dir of each other checkout of this
// repository that may hold ref, until one does not miss, and returns that checkout. The
// candidates are the checkouts the repo-wide session store records as having run ref,
// newest first, then the primary checkout when this is a linked worktree, for a run no
// fact recorded, then, for a ref no fact records, each sibling linked worktree whose store
// changed in the last day. A checkout sharing this one's cache dir is skipped: that store
// already missed. Nothing is written to another checkout.
//
// A miss is a *refMiss, which unwraps to the *cache.RefNotFoundError `magus query`
// renders under MGS8001. Any other error from read, or ctx ending, ends the search.
func (m *Magus) searchOtherCheckouts(ctx context.Context, ref string, read func(cacheDir string) error) (string, error) {
	var unread string
	var unreadGone bool
	var consulted []string
	recorded := m.recordedCheckouts(ctx, ref)
	candidates := append(slices.Clone(recorded), unrecordedCheckouts(m.ws.Root, ref, recorded)...)
	self := m.CacheDir()
	for _, root := range candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Only a recorded checkout whose store goes unread is worth naming: one read here
		// already missed, and the primary fallback never claimed the ref.
		nameable := unread == "" && slices.Contains(recorded, root)
		if _, err := os.Stat(root); err != nil {
			if nameable {
				unread, unreadGone = root, true
			}
			continue
		}
		// The other checkout's own config decides where its store lives, so this
		// checkout's loaded config does not apply.
		dir, err := ResolveCacheDir(root)
		if err != nil {
			if nameable {
				unread = root
			}
			continue
		}
		if dir == self {
			continue
		}
		err = read(dir)
		if errors.Is(err, fs.ErrNotExist) {
			consulted = append(consulted, root)
			continue
		}
		if err != nil {
			return "", err
		}
		return root, nil
	}
	miss := &refMiss{
		err:       &cache.RefNotFoundError{Ref: ref, Stores: []string{cache.LocalStore}, UnreadCheckout: unread, UnreadCheckoutGone: unreadGone},
		consulted: consulted,
	}
	miss.foldInto(miss.err)
	return "", miss
}

// unrecordedCheckouts are the checkouts worth trying for ref beyond the recorded ones: the
// primary checkout when root is a linked worktree, then, for a ref no fact records, root's
// counterparts in the other linked worktrees whose store changed in the last day, newest store
// first. Reading every one of this repository's 455 took 1.3s on a miss; the last day's 0.35s.
func unrecordedCheckouts(root, ref string, recorded []string) []string {
	primary, ok := primaryCheckoutRoot(root)
	if !ok {
		return nil
	}
	var roots []string
	if !slices.Contains(recorded, primary) {
		roots = append(roots, primary)
	}
	// No fact records a guard verdict: the hook stores it in whichever checkout it judged in.
	if cache.LooksLikeRef(ref) {
		return roots
	}
	const recent = 24 * time.Hour
	checkout, others, err := vcs.Checkouts(root)
	if err != nil {
		return roots
	}
	rel, err := filepath.Rel(checkout, root)
	if err != nil {
		return roots
	}
	type store struct {
		root     string
		modified time.Time
	}
	var stores []store
	for _, other := range others {
		sibling := filepath.Join(other, rel)
		if sibling == primary {
			continue
		}
		dir, err := ResolveCacheDir(sibling)
		if err != nil {
			continue
		}
		if info, err := os.Stat(dir); err == nil && time.Since(info.ModTime()) < recent {
			stores = append(stores, store{root: sibling, modified: info.ModTime()})
		}
	}
	slices.SortFunc(stores, func(a, b store) int { return b.modified.Compare(a.modified) })
	for _, s := range stores {
		roots = append(roots, s.root)
	}
	return roots
}

// refMiss is a ref that neither this checkout's output store nor any other checkout's
// resolved, with what the search learned about who has it: a ref lives in the output store
// of the checkout that ran it, so another worktree of the repository reads it there.
type refMiss struct {
	// err is the miss as the error `magus query` renders under MGS8001, built once so
	// errors.As hands back the same value every time.
	err       *cache.RefNotFoundError
	consulted []string // other checkouts whose store was read and did not hold the ref
}

func (r *refMiss) Error() string { return r.err.Error() }

// Unwrap is fs.ErrNotExist to errors.Is.
func (r *refMiss) Unwrap() error { return r.err }

// foldInto adds the miss to e, an error a lookup that ran after this one built, so its
// message names the checkouts too. Their stores follow e's first, this checkout's.
func (r *refMiss) foldInto(e *cache.RefNotFoundError) {
	e.UnreadCheckout, e.UnreadCheckoutGone = r.err.UnreadCheckout, r.err.UnreadCheckoutGone
	if len(e.Stores) == 0 {
		return
	}
	stores := []string{e.Stores[0]}
	for _, root := range r.consulted {
		stores = append(stores, "checkout "+root)
	}
	e.Stores = append(stores, e.Stores[1:]...)
}

// recordedCheckouts lists the other checkouts the repo-wide session store records as
// having run the ref ref names, newest first. Only checkouts that recorded the ref are
// named, so a repository with hundreds of worktrees costs one store read, never a walk of
// them. A ref that is not shaped like an output ref names none, and reads nothing.
func (m *Magus) recordedCheckouts(ctx context.Context, ref string) []string {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if !cache.LooksLikeRef(ref) {
		return nil
	}
	dir, err := sessions.Dir(m.ws.Root)
	if err != nil {
		slog.WarnContext(ctx, "other checkouts were not consulted for "+ref, attr.Error(err))
		return nil
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		slog.WarnContext(ctx, "other checkouts were not consulted for "+ref, attr.Error(err))
		return nil
	}
	self := resolvedPath(m.ws.Root)
	var out []string
	for _, s := range sessions.Summarize(fold) {
		if s.Workspace == "" || slices.Contains(out, s.Workspace) || resolvedPath(s.Workspace) == self {
			continue
		}
		if slices.ContainsFunc(s.Targets, func(t sessions.TargetResult) bool { return recordedRefMatches(t.Ref, ref) }) {
			out = append(out, s.Workspace)
		}
	}
	return out
}

// recordedRefMatches reports whether a fact's ref is the one a user's ref names: the user
// may type a prefix of it, or an attempt id that extends it. Both must be shaped like a
// ref, as the output store requires, so a bare "out" names nothing.
func recordedRefMatches(recorded, ref string) bool {
	return cache.LooksLikeRef(recorded) && cache.LooksLikeRef(ref) &&
		(strings.HasPrefix(recorded, ref) || strings.HasPrefix(ref, recorded))
}

// resolvedPath is path with symlinks resolved, or path itself when it does not resolve
// (a checkout removed from disk).
func resolvedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// primaryCheckoutRoot is root's counterpart in the primary checkout when root sits in a
// linked git worktree, keeping a workspace nested below its checkout nested. The primary
// is the checkout whose .git is a directory; vcs.Checkouts lists it first when it exists.
// There is none from the primary checkout itself, or in a bare repository.
func primaryCheckoutRoot(root string) (string, bool) {
	checkout, others, err := vcs.Checkouts(root)
	if err != nil || len(others) == 0 || hasGitDir(checkout) || !hasGitDir(others[0]) {
		return "", false
	}
	rel, err := filepath.Rel(checkout, root)
	if err != nil {
		return "", false
	}
	return filepath.Join(others[0], rel), true
}

func hasGitDir(checkout string) bool {
	info, err := os.Stat(filepath.Join(checkout, ".git"))
	return err == nil && info.IsDir()
}
