package diff

import (
	"crypto/sha256"
	"encoding/hex"
)

// FileHunks is one changed file's hunks, in patch order.
type FileHunks struct {
	Path  string `json:"path"`
	Hunks []Hunk `json:"hunks"`
}

// Hunk is one @@ section: the header line, its body, and the content digest the viewed set
// is keyed by.
type Hunk struct {
	// Index is the 0-based position within the file, which is the coordinate the MCP surface
	// takes on a comment. It is carried explicitly rather than left implicit in the slice
	// position so a caller that filters hunks cannot silently renumber them.
	Index  int    `json:"index"`
	Header string `json:"header"`
	// Lines is the body EXACTLY as it arrived, markers included, because that is what Digest
	// hashes. Rows carries the same body parsed for rendering; the two are not interchangeable,
	// since a context line whose producer dropped the trailing space arrives as "" and would
	// come back from Rows as " ".
	Lines  []string `json:"lines"`
	Digest string   `json:"digest"`

	OldStart int   `json:"old_start"`
	OldCount int   `json:"old_count"`
	NewStart int   `json:"new_start"`
	NewCount int   `json:"new_count"`
	Rows     []Row `json:"rows"`
}

// ParseHunks is the identity view of a patch: paths and hunk digests, without the rendering
// detail. It is what the session store and the MCP surface consume.
//
// A thin projection of Parse rather than a second reader. It used to be its own pass, and the
// two disagreed: this one knew only git's `diff --git` header, so a Mercurial patch parsed to
// zero files here while the richer reader handled it - and this is the one that decides which
// files a changeset contains.
func ParseHunks(patch string) []FileHunks {
	files := Parse(patch)
	if len(files) == 0 {
		return nil
	}
	out := make([]FileHunks, 0, len(files))
	for _, f := range files {
		out = append(out, FileHunks{Path: f.Path, Hunks: f.Hunks})
	}
	return out
}

// HunkCounts reports how many hunks each path has, which is all a validator needs.
func HunkCounts(patch string) map[string]int {
	out := map[string]int{}
	for _, f := range ParseHunks(patch) {
		out[f.Path] = len(f.Hunks)
	}
	return out
}

// PatchDigest is the identity of a whole patch, used to tell "the tree moved" from "the tree
// is the same and we simply looked again".
//
// A session holds a changeset computed at some past moment. Without this, a client that joins
// later cannot tell a current answer from a frozen one, and the party least able to notice -
// an agent, which cannot see the tree - is the one served the stale copy.
func PatchDigest(patch string) string {
	sum := sha256.Sum256([]byte(patch))
	return hex.EncodeToString(sum[:16])
}
