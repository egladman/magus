package job

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// checkpointDigestLen is the width of a checkpoint token's digest: the 16 bytes of sha256
// [types.VCSCheckpoint.Token] keeps, in lowercase hex.
const checkpointDigestLen = 32

// DeclaredCheckpoint is token as a row declaring it records it: its revision expanded to
// the full revision this checkout's version control resolves it to, its digest kept, so
// the row compares with the base `magus job exec` reads.
//
// It finds a JobCheckpointUnplaceable problem at field checkpoint for a token whose
// revision resolves to nothing, which includes every token where no version control answers
// at the store's root; a revision that is neither this checkout's HEAD nor an ancestor of
// it, since no work here started from it; and a digest that is not the shape
// [types.VCSCheckpoint.Token] writes. At HEAD it also finds one for a digest the live tree
// does not reproduce. At an older revision the patch behind a digest is gone, so that digest
// is kept unverified. An empty token, or the checkpoint row id already holds in rows, comes
// back unchanged: a row may name none, and one it already holds was settled when it was
// written. With a problem, the token comes back as it was given.
func (s *Store) DeclaredCheckpoint(ctx context.Context, rows []types.Job, id, token string) (string, types.JobProblems) {
	return s.declaredCheckpoint(ctx, rows, id, token, false)
}

// declaredCheckpoint is [Store.DeclaredCheckpoint]. With ownCheckout the row is forked into
// a checkout made at its checkpoint, so any revision the repository holds is placeable: its
// work starts there, not from this checkout's HEAD.
func (s *Store) declaredCheckpoint(ctx context.Context, rows []types.Job, id, token string, ownCheckout bool) (string, types.JobProblems) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", nil
	}
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id }); i >= 0 && rows[i].Checkpoint == token {
		return token, nil
	}
	real := hint.VCSCheckpoint.With("-o name")
	unplaceable := func(fix, format string, args ...any) (string, types.JobProblems) {
		return token, types.JobProblems{{Code: types.JobCheckpointUnplaceable, Field: "checkpoint", Message: fmt.Sprintf(format, args...), Fix: fix}}
	}
	realFix := fmt.Sprintf("name the checkpoint `%s` prints, or leave it out", real)
	rev, digest := types.ParseCheckpointToken(token)
	if strings.Contains(token, "+") && !isCheckpointDigest(digest) {
		return unplaceable(realFix, "checkpoint %q carries the digest %q where a checkpoint carries %d lowercase hex characters",
			token, digest, checkpointDigestLen)
	}
	driver, full, err := s.fullRevision(ctx, rev)
	if err != nil {
		return unplaceable(realFix, "checkpoint %q names no revision this repository holds (%v)", token, err)
	}
	head, err := driver.FindCommit(ctx, s.root, "")
	if err != nil {
		return unplaceable(realFix, "checkpoint %q cannot be placed against this checkout's HEAD (%v)", token, err)
	}
	if full != head.ID {
		if ownCheckout {
			return expandedCheckpoint(full, digest), nil
		}
		ancestor, err := driver.IsAncestor(ctx, s.root, full, head.ID)
		switch {
		case err != nil:
			return unplaceable(realFix, "checkpoint %q cannot be placed against this checkout's HEAD %s (%v)", token, shortRevision(head), err)
		case !ancestor:
			return unplaceable(realFix, "checkpoint %q is neither this checkout's HEAD %s nor an ancestor of it, so no work here started from it",
				token, shortRevision(head))
		}
	} else if digest != "" {
		live, err := s.CheckoutToken(ctx)
		if err != nil {
			return unplaceable("name the checkpoint without its digest, or leave it out",
				"checkpoint %q carries a digest, and this checkout's tree could not be read to check it (%v)", token, err)
		}
		if _, liveDigest := types.ParseCheckpointToken(live); liveDigest != digest {
			return unplaceable(realFix, "checkpoint %q is HEAD with the digest %q, and this checkout's tree reads %q",
				token, digest, live)
		}
	}
	return expandedCheckpoint(full, digest), nil
}

func expandedCheckpoint(full, digest string) string {
	if digest == "" {
		return full
	}
	return fmt.Sprintf("%s+%s", full, digest)
}

// fullRevision is the version control answering at the store's root and the full revision
// it resolves rev to.
func (s *Store) fullRevision(ctx context.Context, rev string) (types.VCSDriver, string, error) {
	switch {
	case rev == "":
		return nil, "", errors.New("it has no revision before its digest")
	case s.root == "":
		return nil, "", errors.New("this job store has no checkout to resolve it in")
	}
	driver, why := resolveDriver(ctx, s.root)
	if driver == nil {
		return nil, "", errors.New(why)
	}
	c, err := driver.FindCommit(ctx, s.root, rev)
	if err != nil {
		return nil, "", err
	}
	if c.ID == "" {
		return nil, "", fmt.Errorf("%s resolved to no commit", rev)
	}
	return driver, c.ID, nil
}

// CheckoutToken is the store's checkout as `magus vcs checkpoint -o name` prints it, read
// from the tree: `<rev>`, or `<rev>+<digest>` when it is dirty. It is the one source a
// recorded base, or a checkpoint nobody named, comes from. It fails when the store has no
// checkout, no version control answers there, or the checkout has no revision yet.
func (s *Store) CheckoutToken(ctx context.Context) (string, error) {
	if s.root == "" {
		return "", errors.New("this job store has no checkout to read")
	}
	driver, why := resolveDriver(ctx, s.root)
	if driver == nil {
		return "", errors.New(why)
	}
	cp, err := vcs.Checkpoint(ctx, s.root, types.VCSResolution{VCS: driver}, false)
	if err != nil {
		return "", err
	}
	if cp.Revision == "" {
		return "", errors.New("the checkout has no revision yet")
	}
	return cp.Token(), nil
}

func isCheckpointDigest(digest string) bool {
	return len(digest) == checkpointDigestLen &&
		strings.Trim(digest, "0123456789abcdef") == ""
}

// checkoutTokenIfReadable is [Store.CheckoutToken], empty where the checkout's version
// control cannot say: a row declared there is written without a checkpoint.
func (s *Store) checkoutTokenIfReadable(ctx context.Context) string {
	token, err := s.CheckoutToken(ctx)
	if err != nil {
		return ""
	}
	return token
}

// declaredMerge is merge with the checkpoint it writes onto row id held to
// [Store.DeclaredCheckpoint], storing that checkpoint expanded. A merge declaring a writing
// row (a new one, or one that ended) that names none records the checkout's own; where none
// can be read the row is written without one. A checkpoint it cannot place comes back as a
// problem with merge unchanged.
func (s *Store) declaredMerge(ctx context.Context, rows []types.Job, id string, merge func(*types.Job)) (func(*types.Job), types.JobProblems) {
	next, updates := mergeBase(rows, id, merge)
	merge(&next)
	written := next.Checkpoint
	if !updates && !next.ReadOnly && strings.TrimSpace(written) == "" {
		token := s.checkoutTokenIfReadable(ctx)
		if token == "" {
			return merge, nil
		}
		return func(u *types.Job) {
			merge(u)
			if strings.TrimSpace(u.Checkpoint) == "" {
				u.Checkpoint = token
			}
		}, nil
	}
	full, ps := s.declaredCheckpoint(ctx, rows, id, written, next.Worktree.Standing())
	if len(ps) > 0 || full == written {
		return merge, ps
	}
	return func(u *types.Job) {
		merge(u)
		if u.Checkpoint == written {
			u.Checkpoint = full
		}
	}, nil
}
