package vcs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Checkpoint is the identity of the working tree a piece of work was handed.
// It resolves and records and never mints: no tag, stash or ref is made, so
// taking one changes nothing and is safe once per lease.
type Checkpoint struct {
	// Revision is the full head revision id, never abbreviated.
	Revision string `json:"revision" yaml:"revision"`
	// Branch is the movable name pointing at Revision. Empty is ordinary.
	Branch string `json:"branch,omitempty" yaml:"branch,omitempty"`
	// Dirty reports uncommitted changes, untracked files included.
	Dirty bool `json:"dirty" yaml:"dirty"`
	// PatchDigest fingerprints the tracked uncommitted patch, empty when clean.
	// It matches internal/changeset.PatchDigest byte for byte.
	PatchDigest string `json:"patch_digest,omitempty" yaml:"patch_digest,omitempty"`
	// UntrackedDigest fingerprints untracked paths and content, empty when
	// there are none. Content counts: an edit leaves a path-only digest alone.
	UntrackedDigest string `json:"untracked_digest,omitempty" yaml:"untracked_digest,omitempty"`
	// VCS is the backend name, which says whose syntax Revision is written in.
	VCS string `json:"vcs,omitempty" yaml:"vcs,omitempty"`
	// Preserved is an opaque handle to a capture of the uncommitted work. It
	// is the one field a checkpoint mints, and only the backend can read it.
	Preserved string `json:"preserved,omitempty" yaml:"preserved,omitempty"`
}

// Token is the checkpoint as one string: Revision for a clean tree,
// `<Revision>+<digest>` for a dirty one. [types.ParseCheckpointToken] reads
// it back. Untracked files fold into the digest, so a tree holding only new
// files does not read as an empty patch.
func (cp Checkpoint) Token() string {
	if !cp.Dirty {
		return cp.Revision
	}
	if cp.UntrackedDigest == "" {
		return fmt.Sprintf("%s+%s", cp.Revision, cp.PatchDigest)
	}
	// Truncated to the width of PatchDigest so every dirty token reads alike.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s", cp.PatchDigest, cp.UntrackedDigest)))
	return fmt.Sprintf("%s+%s", cp.Revision, hex.EncodeToString(sum[:16]))
}
