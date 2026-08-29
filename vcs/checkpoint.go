package vcs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/egladman/magus/types"
)

// Checkpoint resolves what is true in dir right now: the head revision, the branch
// carrying it, whether the tree is dirty, and a digest of the uncommitted patch.
//
// It READS. Nothing here writes a tag, a stash, a ref, or a file, and nothing about
// the tree is different afterwards - so a caller recording one per lease
// pays only the cost of the probes, and a checkpoint nobody kept has cost nothing.
// magus emits the facts; whoever holds the ledger decides what they mean.
//
// One Metadata call covers revision, branch and dirtiness, so a clean tree spawns no
// second round of processes. The patch is read only when the tree is dirty, which is
// also the only case where a digest would say anything.
func Checkpoint(ctx context.Context, dir string, res types.VCSResolution) (types.VCSCheckpoint, error) {
	if res.VCS == nil {
		return types.VCSCheckpoint{}, errors.New("vcs checkpoint: no VCS resolved for this workspace; there is no revision to record")
	}
	meta, err := res.VCS.Metadata(ctx, dir)
	if err != nil {
		return types.VCSCheckpoint{}, fmt.Errorf("vcs checkpoint: %w", err)
	}
	cp := types.VCSCheckpoint{
		Revision: meta.ID,
		Branch:   meta.Ref,
		Dirty:    meta.IsDirty,
		VCS:      res.Name,
	}
	if !cp.Dirty {
		return cp, nil
	}
	patch, err := res.VCS.DirtyDiff(ctx, dir, nil)
	if err != nil {
		return types.VCSCheckpoint{}, fmt.Errorf("vcs checkpoint: %w", err)
	}
	cp.PatchDigest = patchDigest(patch)
	return cp, nil
}

// patchDigestBytes is how much of the hash the digest keeps: 16 bytes, rendered as
// 32 hex chars - the width internal/diff.PatchDigest uses, which is the whole point
// (see patchDigest below). Still short enough to sit in a ledger cell.
const patchDigestBytes = 16

// patchDigest fingerprints a patch: sha256 over the text, first patchDigestBytes
// bytes, hex - 32 characters.
//
// The algorithm deliberately matches the review session's patch digest
// (internal/diff.PatchDigest: hex over sum[:16], NOT 16 hex characters), so the two
// identities stay comparable: a checkpoint recorded when work was handed out and a
// review session opened over the same tree must produce the SAME string, or neither
// can be used to check the other. It is reimplemented rather than shared because the
// review package is a cross-branch import, and the four lines are cheaper than the
// coupling; if they ever diverge, this is the site that has to move.
func patchDigest(patch string) string {
	sum := sha256.Sum256([]byte(patch))
	return hex.EncodeToString(sum[:patchDigestBytes])
}
