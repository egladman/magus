package vcs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

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
	cp.UntrackedDigest = untrackedDigest(ctx, dir, res)
	return cp, nil
}

// untrackedDigest fingerprints the untracked files' paths and content, "" when there are
// none or when the backend cannot answer.
//
// Derived from two driver methods rather than a new one: DirtyFiles reports every changed
// path INCLUDING untracked, TrackedFiles says which of those the backend tracks, and the
// difference is what nothing else in a checkpoint can see. Both are on every backend, so
// this is agnostic by construction rather than by four parallel implementations.
//
// BEST-EFFORT, and that is a real choice. A checkpoint's job is to record where the work
// is; refusing to record one because an untracked file was deleted mid-probe, or because
// a backend lacks TrackedFiles, would lose the revision too - and the revision is the
// half a reader can still act on. A missing digest reads as "not measured", which is what
// an empty string already means for PatchDigest.
func untrackedDigest(ctx context.Context, dir string, res types.VCSResolution) string {
	reporter, ok := res.VCS.(types.TrackedFileReporter)
	if !ok {
		return ""
	}
	changed, err := res.VCS.DirtyFiles(ctx, dir, nil)
	if err != nil || len(changed) == 0 {
		return ""
	}
	tracked, err := reporter.TrackedFiles(ctx, dir, changed)
	if err != nil {
		return ""
	}
	isTracked := make(map[string]bool, len(tracked))
	for _, p := range tracked {
		isTracked[p] = true
	}
	untracked := make([]string, 0, len(changed))
	for _, p := range changed {
		if !isTracked[p] {
			untracked = append(untracked, p)
		}
	}
	if len(untracked) == 0 {
		return ""
	}
	// Sorted, because DirtyFiles order is the backend's and a digest that moved with it
	// would report a change nobody made.
	sort.Strings(untracked)

	h := sha256.New()
	for _, rel := range untracked {
		// The PATH is hashed even when its content cannot be read, so a file that appears
		// and vanishes between the listing and the read still moves the digest rather than
		// being silently equivalent to absent.
		fmt.Fprintf(h, "%s\x00", rel)
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		// hash.Hash.Write is documented never to return an error.
		_, _ = h.Write(body)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:patchDigestBytes])
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
