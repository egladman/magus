package diff

import (
	"fmt"
	"strings"
)

// SanitizeBidi rewrites the characters a renderer OBEYS but a reader cannot see, and reports
// whether it changed anything.
//
// The attack it exists for is Trojan Source: a bidirectional override reorders how a line is drawn
// without touching the bytes a compiler reads, so the reviewer and the toolchain can be made to
// disagree about what the line says. The same trick works with invisible characters, which hide a
// difference rather than reordering one.
//
// Escaped rather than stripped, and this is the whole design. Stripping would make the line render
// honestly and silently misreport the file's contents - a reviewer would approve bytes magus never
// showed them, which is the failure this is meant to prevent rather than a milder version of it.
// Escaping says exactly where the character is and what it was.
//
// It is applied to DISPLAY and never to the bytes anything else keys on: a hunk digest, a read
// receipt and a remark's quote anchor all address the file as it really is, so a changed rendition
// must not become a changed identity.
func SanitizeBidi(line string) (string, bool) {
	if !strings.ContainsFunc(line, deceptive) {
		return line, false
	}
	var b strings.Builder
	b.Grow(len(line))
	for _, r := range line {
		if deceptive(r) {
			fmt.Fprintf(&b, "<U+%04X>", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), true
}

// deceptive reports whether r changes what a line looks like without being visible itself.
//
// Two families, both from the Unicode security guidance rather than invented here:
//
//   - U+202A..U+202E and U+2066..U+2069 are the bidirectional embeddings, overrides and isolates.
//     These are the reordering half.
//   - The zero-width and directional marks (U+200B..U+200F), the word joiner (U+2060) and the
//     byte-order mark (U+FEFF) are the invisible half: they hide a difference between two lines
//     that look identical, which is how a homoglyph or a lookalike identifier goes unnoticed.
//
// Ordinary control characters are deliberately NOT here. A tab is not deceptive, and a diff full of
// escaped tabs is a diff nobody reads - which would cost more review than the attack does.
func deceptive(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	case r >= 0x200B && r <= 0x200F, r == 0x2060, r == 0xFEFF:
		return true
	default:
		return false
	}
}
