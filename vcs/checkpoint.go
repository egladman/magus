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
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	typesvcs "github.com/egladman/magus/types/vcs"
)

// Checkpoint resolves what is true in dir right now: the head revision, the branch
// carrying it, whether the tree is dirty, and a digest of the uncommitted patch.
//
// It READS. Nothing here writes a tag, a stash, a ref, or a file, and nothing about
// the tree is different afterwards, so a caller recording one per lease
// pays only the cost of the probes, and a checkpoint nobody kept has cost nothing.
// magus emits the facts; whoever holds the ledger decides what they mean.
//
// preserve is the exception, and opt-in for that reason: it captures the uncommitted work
// through [types.VCSDriver.Preserve] and fills VCSCheckpoint.Preserved with a handle that
// restores it. The working copy stays untouched, but the backend gains an object.
//
// One Metadata call covers revision, branch and dirtiness, so a clean tree spawns no
// second round of processes. The patch is read only when the tree is dirty, which is
// also the only case where a digest would say anything.
func Checkpoint(ctx context.Context, dir string, res types.VCSResolution, preserve bool) (typesvcs.Checkpoint, error) {
	if res.VCS == nil {
		return typesvcs.Checkpoint{}, errors.New("vcs checkpoint: no VCS resolved for this workspace, there is no revision to record")
	}
	meta, err := res.VCS.Metadata(ctx, dir)
	if err != nil {
		return typesvcs.Checkpoint{}, fmt.Errorf("vcs checkpoint: %w", err)
	}
	cp := typesvcs.Checkpoint{
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
		return typesvcs.Checkpoint{}, fmt.Errorf("vcs checkpoint: %w", err)
	}
	cp.PatchDigest = patchDigest(patch)
	cp.UntrackedDigest = untrackedDigest(ctx, dir, res)
	if preserve {
		// Recorded BEFORE the error is weighed: hg and sl both return a handle alongside a
		// failure on purpose, since the shelf or snapshot commit exists from that point and
		// on sl the working copy can be parked on it. Dropping the handle would leave a
		// minted object with nothing pointing at it.
		handle, err := res.VCS.Preserve(ctx, dir)
		cp.Preserved = handle
		if err != nil {
			return cp, fmt.Errorf("vcs checkpoint: %w", err)
		}
	}
	return cp, nil
}

// PrunePreserved drops every git capture in dir whose content reads the
// same in res.Base, and reports the handles it dropped. A capture with a
// change the base lacks stays, however old.
//
// Only git can prove a capture landed. Mercurial and Sapling captures
// accumulate and Jujutsu mints none; all answer with an empty list.
func PrunePreserved(ctx context.Context, dir string, res types.VCSResolution) ([]string, error) {
	if res.VCS == nil {
		return nil, errors.New("vcs prune-preserved: no VCS resolved for this workspace, there is nothing to prune")
	}
	if _, ok := res.VCS.(gitVCS); !ok {
		return nil, nil
	}
	if res.Base == "" {
		return nil, errors.New("vcs prune-preserved: no base to judge a capture landed against")
	}
	return pruneLandedGit(ctx, dir, res.Base)
}

// pruneLandedGit deletes the refs Preserve anchored whose content landed in
// into. A ref it cannot judge stays, and the failure is returned.
func pruneLandedGit(ctx context.Context, dir, into string) ([]string, error) {
	opts := gitOpts{Isolated: true}
	listed, err := gitOutput(ctx, dir, opts, "for-each-ref", "--sort=committerdate",
		"--format=%(refname) %(objectname) %(contents:subject)", preservedRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("vcs prune-preserved: for-each-ref: %w", err)
	}
	var (
		dropped []string
		failed  []error
	)
	for line := range strings.SplitSeq(listed, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), " ", 3)
		if len(fields) < 3 || fields[2] != preserveMessage {
			continue
		}
		name, sha := fields[0], fields[1]
		unlanded, err := gitVCS{}.UnlandedPaths(ctx, dir, sha, into)
		if err != nil {
			failed = append(failed, fmt.Errorf("capture %s: %w", sha, err))
			continue
		}
		if len(unlanded) > 0 {
			continue
		}
		if _, err := gitOutput(ctx, dir, opts, "update-ref", "-d", name, sha); err != nil {
			failed = append(failed, fmt.Errorf("update-ref -d %s: %w", name, err))
			continue
		}
		dropped = append(dropped, strings.TrimPrefix(name, preservedRefPrefix))
	}
	if err := errors.Join(failed...); err != nil {
		return dropped, fmt.Errorf("vcs prune-preserved: kept what it could not judge: %w", err)
	}
	return dropped, nil
}

// Capture is one automatic preservation of a dirty checkout.
type Capture struct {
	Dir             string `json:"dir"`
	Revision        string `json:"revision"`
	PatchDigest     string `json:"patch_digest"`
	UntrackedDigest string `json:"untracked_digest"`
	Handle          string `json:"handle"`
	VCS             string `json:"vcs"`
	At              int64  `json:"at"`
}

func capturesLedger() (file.JSONL[Capture], error) {
	state, err := xdg.StateDir()
	if err != nil {
		return file.JSONL[Capture]{}, fmt.Errorf("vcs preserve: no state directory for the capture ledger: %w", err)
	}
	return file.JSONL[Capture]{
		Path: filepath.Join(state, "magus", "captures.jsonl"),
		Key:  func(c Capture) string { return c.Dir },
	}, nil
}

// PreserveChanged captures the uncommitted work in dir unless the last
// capture already holds exactly this work; captured is false then and
// cp.Preserved is the earlier handle. A clean tree captures nothing. Each
// capture is recorded in the ledger, and a failure to record is an error.
func PreserveChanged(ctx context.Context, dir string, res types.VCSResolution) (cp typesvcs.Checkpoint, captured bool, err error) {
	cp, err = Checkpoint(ctx, dir, res, false)
	if err != nil || !cp.Dirty {
		return cp, false, err
	}
	key := dir
	if resolved, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		key = resolved
	}
	ledger, err := capturesLedger()
	if err != nil {
		return cp, false, err
	}
	entries, err := ledger.Load()
	if err != nil {
		return cp, false, fmt.Errorf("vcs preserve: read the capture ledger: %w", err)
	}
	for _, e := range entries {
		if e.Dir == key && e.Revision == cp.Revision && e.PatchDigest == cp.PatchDigest && e.UntrackedDigest == cp.UntrackedDigest {
			cp.Preserved = e.Handle
			return cp, false, nil
		}
	}
	handle, perr := res.VCS.Preserve(ctx, dir)
	cp.Preserved = handle
	if handle != "" {
		entry := Capture{Dir: key, Revision: cp.Revision, PatchDigest: cp.PatchDigest,
			UntrackedDigest: cp.UntrackedDigest, Handle: handle, VCS: res.Name, At: time.Now().Unix()}
		if uerr := ledger.Update(ctx, func([]Capture) []Capture { return []Capture{entry} }); uerr != nil {
			perr = errors.Join(perr, fmt.Errorf("record capture %s: %w", handle, uerr))
		}
	}
	if perr != nil {
		return cp, handle != "", fmt.Errorf("vcs preserve: %w", perr)
	}
	return cp, handle != "", nil
}

// untrackedDigest fingerprints the untracked files' paths and content, "" when there are
// none or when the backend cannot answer.
//
// Derived from two existing driver methods rather than a new one: DirtyFiles reports every
// changed path INCLUDING untracked, TrackedFiles says which of those are tracked, and the
// difference is the untracked set. Both exist on every backend, so this needs no
// per-backend implementation.
//
// BEST-EFFORT: refusing a checkpoint because an untracked file vanished mid-probe, or
// because a backend lacks TrackedFiles, would lose the revision too, and the revision is
// the half a reader can still act on. A missing digest reads as "not measured", the same
// as an empty PatchDigest.
func untrackedDigest(ctx context.Context, dir string, res types.VCSResolution) string {
	changed, err := res.VCS.DirtyFiles(ctx, dir, nil)
	if err != nil || len(changed) == 0 {
		return ""
	}
	tracked, err := res.VCS.TrackedFiles(ctx, dir, changed)
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
		// The PATH is hashed even when the content cannot be read, so a file that vanishes
		// between the listing and the read still moves the digest.
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
// 32 hex chars: the width internal/changeset.PatchDigest uses, which is the whole point
// (see patchDigest below). Still short enough to sit in a ledger cell.
const patchDigestBytes = 16

// patchDigest fingerprints a patch: sha256 over the text, first patchDigestBytes
// bytes, hex: 32 characters.
//
// The algorithm deliberately matches the review session's patch digest
// (internal/changeset.PatchDigest: hex over sum[:16], NOT 16 hex characters), so the two
// identities stay comparable: a checkpoint recorded when work was handed out and a
// review session opened over the same tree must produce the SAME string, or neither
// can be used to check the other. It is reimplemented rather than shared because the
// review package is a cross-branch import, and the four lines are cheaper than the
// coupling; if they ever diverge, this is the site that has to move.
func patchDigest(patch string) string {
	sum := sha256.Sum256([]byte(patch))
	return hex.EncodeToString(sum[:patchDigestBytes])
}
