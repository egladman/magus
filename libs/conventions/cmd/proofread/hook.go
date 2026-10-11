package main

import (
	"strings"

	"github.com/egladman/magus/libs/conventions/proofread"
)

// scissors is the line git writes above the diff of a verbose commit. It and
// everything after it is not part of the message.
const scissors = "# ------------------------ >8 ------------------------"

// fromFiles reports whether the text kind judges is read from the files in
// args. A commit message is read from stdin unless a message file is named,
// the way a VCS hook passes it.
func fromFiles(kind proofread.Kind, args []string) bool {
	return readsFiles(kind) || (kind == proofread.KindCommitMessage && len(args) > 0)
}

// stripCommitComments returns text as git records it: without the lines that
// start with "#" and without the scissors line and what follows it. origin
// holds, for each line of kept, the 1-based line it had in text.
func stripCommitComments(text string) (kept string, origin []int) {
	var b strings.Builder

	for i, line := range strings.SplitAfter(text, "\n") {
		if strings.HasPrefix(line, scissors) {
			break
		}

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		b.WriteString(line)

		origin = append(origin, i+1)
	}

	return b.String(), origin
}

// commitMessageFindings judges the message file text, which a VCS hook passes,
// without its comment lines. A finding's line is its line in the file.
func commitMessageFindings(name, text string, opts []proofread.Option) []finding {
	out := []finding{}

	kept, origin := stripCommitComments(text)
	if strings.TrimSpace(kept) == "" {
		return out
	}

	atFile := func(line int) int {
		if line < 1 || line > len(origin) {
			return line
		}

		return origin[line-1]
	}

	for _, f := range proofread.JudgeText(kept, proofread.KindCommitMessage, opts...) {
		f.Line, f.EndLine = atFile(f.Line), atFile(f.EndLine)
		out = append(out, toFinding(name, sourceAt(name, f.Line), proofread.KindCommitMessage, f))
	}

	return out
}
