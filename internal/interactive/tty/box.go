package tty

import "strings"

// The box every interactive surface draws itself in.
//
// These are free functions rather than region methods because the box is a
// LOOK, not a place: the run's pinned band draws one at the bottom of the
// terminal and the picker draws one wherever the cursor happens to be, and the
// two must be the same box or the terminal reads as two different products.
// Duplicating the glyphs and the arithmetic in each surface is how they drift.
//
// Every width here is in COLUMNS, measured with cols, because the glyphs are
// multi-byte and every budget in this package is column-denominated.

// boxRule renders a horizontal rule of inner columns between two corners, with
// optional captions set into it.
//
// A caption sits one glyph in from its corner with the rule resuming after it -
// which is what lets a status line or a way out ride the frame instead of
// spending a whole row. Clipped, never wrapped: a rule is one row by
// definition, so a caption too long for the terminal loses its tail rather than
// the box losing its shape.
func boxRule(inner int, lc, rc, left, right string, dim func(string) string) string {
	if inner < 1 {
		return ""
	}
	if left == "" && right == "" {
		return dim(lc + strings.Repeat(boxH, inner) + rc)
	}
	lead := " " + ClipVisible(left, max(inner-4, 0)) + " "
	if left == "" {
		lead = ""
	}
	tail := ""
	if right != "" {
		tail = " " + ClipVisible(right, max(inner-cols(lead)-3, 0)) + " "
	}
	gap := inner - cols(lead) - cols(tail) - 1
	if gap < 0 {
		gap = 0
	}
	return dim(lc + boxH + lead + strings.Repeat(boxH, gap) + tail + rc)
}

// boxWrap puts one row of already-laid-out content between the vertical edges,
// padding it so the right edge lands in the same column on every row.
//
// content is measured, not counted: it may carry its own escape sequences, and
// spending columns on those is how the border ends up ragged.
func boxWrap(content string, inner int, dim func(string) string) string {
	if inner < 1 {
		return content
	}
	pad := inner - cols(content)
	if pad < 0 {
		pad = 0
	}
	return dim(boxV) + content + strings.Repeat(" ", pad) + dim(boxV)
}

// plain is the no-styling dim function, for a surface drawing to something that
// does not want colour.
func plain(s string) string { return s }
