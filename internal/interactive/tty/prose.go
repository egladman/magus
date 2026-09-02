package tty

import (
	"fmt"
	"io"
	"strings"
)

// The column budget for prose. Terminal width is the honest answer when there
// is a terminal, but a paragraph set to the full width of a wide window is
// unreadable, so proseMax caps it. proseFallback is the width when there is no
// terminal to measure: a CI log and a redirected file have no width, and 80 is
// what the tree's hand-wrapped text already assumed.
const (
	proseMax      = 100
	proseFallback = 80
)

// Prose writes sentences to w as one paragraph folded to w's width.
//
// It takes the sentences separately so the source carries one per line and a
// grep for any of them matches. Hand-wrapping a paragraph across consecutive
// Fprintln calls splits it at whatever column the author's editor was showing,
// which fixes the line breaks to one terminal width and leaves no whole
// sentence to search for.
func Prose(w io.Writer, p Probe, sentences ...string) {
	ProseItem(w, p, "", sentences...)
}

// ProseItem writes label and its sentences as one paragraph whose continuation
// lines hang under the label, the shape a flag or subcommand list wants.
//
// label carries its own padding, so alignment across a list stays the caller's
// to choose and the indent is whatever the label occupies.
func ProseItem(w io.Writer, p Probe, label string, sentences ...string) {
	for _, line := range foldProse(label, strings.Join(nonEmpty(sentences), " "), proseWidth(w, p)) {
		fmt.Fprintln(w, line)
	}
}

// nonEmpty drops the empty sentences so joining never doubles a space.
func nonEmpty(parts []string) []string {
	kept := parts[:0:0]
	for _, s := range parts {
		if strings.TrimSpace(s) != "" {
			kept = append(kept, s)
		}
	}
	return kept
}

// proseWidth is the column budget for prose written to w.
func proseWidth(w io.Writer, p Probe) int {
	fd, ok := Fd(w)
	if !ok || !p.IsTerminal(fd) {
		return proseFallback
	}
	width, _, err := p.Size(fd)
	if err != nil || width <= 0 {
		return proseFallback
	}
	return min(width, proseMax)
}

// foldProse folds body into lines of at most width display columns, prefixing
// the first with lead and the rest with as many spaces as lead occupies.
//
// A word wider than the budget takes a line of its own rather than being cut:
// the long words in this tree's output are paths, URLs, and commands to run,
// and a reader has to be able to copy one whole.
func foldProse(lead, body string, width int) []string {
	words := strings.Fields(body)
	if len(words) == 0 {
		if lead == "" {
			return nil
		}
		return []string{strings.TrimRight(lead, " ")}
	}
	hang := strings.Repeat(" ", Cols(lead))
	var lines []string
	line := lead + words[0]
	for _, word := range words[1:] {
		if Cols(line)+1+Cols(word) > width && Cols(line) > Cols(lead) {
			lines = append(lines, line)
			line = hang + word
			continue
		}
		line += " " + word
	}
	return append(lines, line)
}
