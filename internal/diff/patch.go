package diff

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// FileHunks is one changed file's hunks, in patch order.
type FileHunks struct {
	Path  string
	Hunks []Hunk
}

// Hunk is one @@ section: the header line, its body, and the content digest the viewed set
// is keyed by.
type Hunk struct {
	// Index is the 0-based position within the file, which is the coordinate the MCP surface
	// takes on a comment. It is carried explicitly rather than left implicit in the slice
	// position so a caller that filters hunks cannot silently renumber them.
	Index  int
	Header string
	Lines  []string
	Digest string
}

// ParseHunks splits a unified patch into per-file hunks.
//
// This exists so the agent half of the diff can address a coordinate it can SEE. The MCP
// surface takes a 0-based hunk index on a comment and used to validate nothing, because the
// handler had no parsed changeset to check against - so a comment landed at a plausible
// coordinate that had never been verified, on a file that might not even be in the change.
//
// Deliberately shallow. It reads `diff --git` and `@@` boundaries and keeps line text; it does
// not model renames, modes, or binary payloads, because nothing downstream of it asks. The
// console's parse.ts is the rich parser and it stays the rich parser - duplicating that here
// would be a second implementation to drift, and drift between the two is exactly what makes
// a shared session dishonest.
func ParseHunks(patch string) []FileHunks {
	var out []FileHunks
	var cur *FileHunks
	var hunk *Hunk

	flushHunk := func() {
		if cur == nil || hunk == nil {
			return
		}
		hunk.Digest = HunkDigest(cur.Path, hunk.Lines)
		cur.Hunks = append(cur.Hunks, *hunk)
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}

	// A patch ends with a newline, and splitting on it yields a trailing empty element that is
	// not a line of the diff. Keeping it would append a phantom "" to the final hunk and change
	// that hunk's digest - which would silently unmark the last hunk of every changeset for a
	// reader who had marked it in the console.
	lines := strings.Split(patch, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			cur = &FileHunks{Path: pathFromGitHeader(line)}
		case strings.HasPrefix(line, "@@"):
			if cur == nil {
				continue
			}
			flushHunk()
			hunk = &Hunk{Index: len(cur.Hunks), Header: line}
		default:
			if hunk != nil {
				hunk.Lines = append(hunk.Lines, line)
			}
		}
	}
	flushFile()
	return out
}

// pathFromGitHeader reads the b-side path out of a `diff --git a/x b/x` line.
//
// LastIndex of " b/" rather than a split, because a path may contain spaces and the a-side may
// contain " b/" as ordinary text. This matches how the CLI already reads the same header, and
// the two must agree or the annotations describe different files than the hunks do.
func pathFromGitHeader(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	cut := strings.LastIndex(rest, " b/")
	if cut < 0 {
		return ""
	}
	return strings.TrimPrefix(rest[cut+1:], "b/")
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
