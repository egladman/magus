package job

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// checkpointDigestLen is the width of a checkpoint token's digest: the 16 bytes of sha256
// [types.VCSCheckpoint.Token] keeps, in lowercase hex.
const checkpointDigestLen = 32

// DeclaredCheckpoint is token as a row declaring it records it: its revision expanded to
// the full revision this checkout's version control resolves it to, its digest kept, so
// the row compares with the base `magus job exec` reads.
//
// It refuses a token whose revision resolves to nothing, which includes every token where
// no version control answers at the store's root, and one whose digest is not the shape
// [types.VCSCheckpoint.Token] writes. An empty token, or the checkpoint row id already
// holds in rows, comes back unchanged: a row may name none, and one it already holds was
// settled when it was written.
func (s *Store) DeclaredCheckpoint(ctx context.Context, rows []types.Job, id, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", nil
	}
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id }); i >= 0 && rows[i].Checkpoint == token {
		return token, nil
	}
	real := hint.VCSCheckpoint.With("-o name")
	rev, digest := types.ParseCheckpointToken(token)
	if strings.Contains(token, "+") && !isCheckpointDigest(digest) {
		return "", fmt.Errorf("job: checkpoint %q carries the digest %q where a checkpoint carries %d lowercase hex characters; `%s` prints a real one",
			token, digest, checkpointDigestLen, real)
	}
	full, err := s.fullRevision(ctx, rev)
	if err != nil {
		return "", fmt.Errorf("job: checkpoint %q names no revision this repository holds: %w; `%s` prints a real one", token, err, real)
	}
	if digest == "" {
		return full, nil
	}
	return full + "+" + digest, nil
}

func (s *Store) fullRevision(ctx context.Context, rev string) (string, error) {
	switch {
	case rev == "":
		return "", errors.New("it has no revision before its digest")
	case s.root == "":
		return "", errors.New("this job store has no checkout to resolve it in")
	}
	driver, why := resolveDriver(ctx, s.root)
	if driver == nil {
		return "", errors.New(why)
	}
	c, err := driver.FindCommit(ctx, s.root, rev)
	if err != nil {
		return "", err
	}
	if c.ID == "" {
		return "", fmt.Errorf("%s resolved to no commit", rev)
	}
	return c.ID, nil
}

func isCheckpointDigest(digest string) bool {
	return len(digest) == checkpointDigestLen &&
		strings.Trim(digest, "0123456789abcdef") == ""
}

// declaredMerge is merge with the checkpoint it writes onto row id held to
// [Store.DeclaredCheckpoint], storing that checkpoint expanded.
func (s *Store) declaredMerge(ctx context.Context, rows []types.Job, id string, merge func(*types.Job)) (func(*types.Job), error) {
	var next types.Job
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id }); i >= 0 {
		next = rows[i].Clone()
	}
	merge(&next)
	written := next.Checkpoint
	full, err := s.DeclaredCheckpoint(ctx, rows, id, written)
	if err != nil || full == written {
		return merge, err
	}
	return func(u *types.Job) {
		merge(u)
		if u.Checkpoint == written {
			u.Checkpoint = full
		}
	}, nil
}
