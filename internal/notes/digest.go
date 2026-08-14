package notes

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// digestLen is how much of the hash is kept. 16 hex chars is 64 bits: collision-free in
// any realistic store, and short enough that a human editing frontmatter by hand is not
// staring at a wall of hex.
const digestLen = 16

// Digest fingerprints the anchored source so a CHANGE is detectable, not merely a
// deletion.
//
// The existence check answers the easy question - a rename or a delete, which the anchor
// itself already reports. This answers the harder and more common one: the code is still
// there and quietly stopped meaning what the note says. That case is the reason the whole
// store needs a gate, because it is invisible to a reader who trusts the note.
//
// NORMALIZATION IS THE WHOLE DESIGN, and it is tuned in one direction on purpose. A
// fingerprint that fires on reformatting produces false drift; the flags get ignored; and
// an ignored gate is worse than no gate at all, because the store then looks checked. So:
//
//   - leading and trailing whitespace per line is dropped (re-indentation is free)
//   - runs of internal whitespace collapse to one space (alignment is free - gofmt
//     realigning struct tags or trailing comments must not read as an edit)
//   - blank lines are dropped entirely (spacing is free)
//
// What it deliberately does NOT do is normalize tokens. Renaming a variable, changing a
// literal, or reordering statements all change the digest, because all of them can change
// what the code means, and a note about it deserves a second look.
//
// The result is a whitespace-insensitive, token-sensitive fingerprint - the same trade
// Fiberplane's Drift makes with a tree-sitter AST hash, reached without a parser per
// language. magus indexes any language SCIP can, so a parser-based fingerprint would work
// for a few and silently do nothing for the rest.
func Digest(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		norm := normalizeLine(line)
		if norm == "" {
			continue
		}
		b.WriteString(norm)
		b.WriteByte('\n')
	}
	// No early return for empty input, deliberately. "" is reserved for "cannot compute",
	// and content that is genuinely empty must hash like anything else - otherwise
	// truncating an anchored file to nothing produces "", ResolveAnchors reads that as no
	// opinion, and the single most destructive edit is the one drift can never see.
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:digestLen]
}

// normalizeLine trims the ends and collapses every internal whitespace run to one space.
func normalizeLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// DigestRange fingerprints lines [start, end] of src, both 1-based and inclusive.
//
// Out-of-range bounds yield "" rather than an error or a panic: the caller's line numbers
// come from a symbol index that can lag the file on disk, and a stale index must degrade
// to "cannot fingerprint this" instead of taking a verify run down or, worse, fingerprinting
// the wrong lines and reporting confident nonsense.
func DigestRange(src string, start, end int) string {
	if start < 1 || end < start {
		return ""
	}
	lines := strings.Split(src, "\n")
	// An end past EOF is PROOF the index is stale, so it degrades to "cannot fingerprint"
	// like every other out-of-range case. Clamping instead would fingerprint a truncated
	// range and report confident drift against lines that no longer mean anything.
	if start > len(lines) || end > len(lines) {
		return ""
	}
	return Digest(lines[start-1 : end])
}
