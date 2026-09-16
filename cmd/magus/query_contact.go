package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

// printSessionContact appends what the loaded agent sessions did to the file a node
// describes, or prints nothing when none of them reached it.
//
// WHY EXPLAIN CARRIES THIS. explain's job is one node's provenance, and it already prints
// the git half: vcs_commits, vcs_last_author, vcs_last_modified. Printing that a file was
// last changed by a person three weeks ago while staying silent about the four agent
// sessions that wrote it yesterday is not a neutral omission, because a reader draws a
// conclusion from the silence, and the conclusion is wrong. The two halves answer one
// question and belong in one place.
//
// It reads the session store rather than the @session overlay on purpose. That overlay is
// a lazy shard keyed to the symbol layer, so reaching it for a single node would load
// every code symbol in the workspace to learn about one file; the store is where the
// overlay's own input comes from, and a per-path read of it is cheap.
// sessionContact is what the loaded sessions did to the file a node describes, or nil.
//
// ONE resolver behind both output branches. The text view and the JSON view answering
// from separate lookups is how two renderings of one fact drift, and this one is already
// delicate: it is eligible for file nodes only, and it reads a store that may not exist.
func sessionContact(root string, node types.KnowledgeNode) *sessions.PathContact {
	if node.Kind != types.KindFile {
		return nil
	}
	path := nodeSourcePath(node)
	if path == "" {
		return nil
	}
	dir, err := sessions.Dir(resolveRootOrEmpty(root))
	if err != nil {
		return nil
	}
	c := sessions.ReadPathContact(dir, path)
	if !c.Touched() {
		return nil
	}
	return &c
}

func printSessionContact(w io.Writer, root string, node types.KnowledgeNode) {
	contact := sessionContact(root, node)
	if contact == nil {
		return
	}
	c := *contact
	fmt.Fprintf(w, "\nagent sessions: %s over %s\n", countedContact(c), countOf(c.Sessions, "session"))
	fmt.Fprintf(w, "  last touched %s; `magus session show <id>` reads what one did\n", humanAge(c.Last))
	if c.Denials > 0 {
		// "operations", not "writes": Denials counts refused reads too, and naming them
		// writes would report an edit that was never attempted.
		fmt.Fprintf(w, "  %s the host refused\n", countOf(c.Denials, "operation"))
	}
}

// nodeSourcePath is the file a node is about, or "" for a node that is about no file.
//
// Source carries `path:line` provenance for a node minted from a position inside a file,
// so the line is cut: contact is recorded against files, and a symbol's line number would
// match nothing.
func nodeSourcePath(node types.KnowledgeNode) string {
	src := node.Source
	if src == "" {
		return ""
	}
	if i := strings.LastIndex(src, ":"); i > 0 {
		if rest := src[i+1:]; rest != "" && strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
			src = src[:i]
		}
	}
	return src
}

// countedContact renders the reads and writes, naming only what happened. A file that was
// read and never written reads differently from one that was edited, and that difference
// is the whole reason to print the line.
func countedContact(c sessions.PathContact) string {
	var parts []string
	if c.Writes > 0 {
		parts = append(parts, countOf(c.Writes, "write"))
	}
	if c.Reads > 0 {
		parts = append(parts, countOf(c.Reads, "read"))
	}
	return strings.Join(parts, ", ")
}

// countOf renders "1 write" / "4 writes". Distinct from this package's plural, which
// picks between two words a caller supplies; this one owns the count as well, because
// every use here is a number followed by a regular noun.
func countOf(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// humanAge renders how long ago something happened, coarsely. The reader's question is
// "is this current work or history", which days answer and minutes do not.
func humanAge(t time.Time) string {
	if t.IsZero() {
		return "at an unrecorded time"
	}
	switch d := time.Since(t); {
	case d < time.Hour:
		return "within the hour"
	case d < 24*time.Hour:
		return countOf(int(d/time.Hour), "hour") + " ago"
	default:
		return countOf(int(d/(24*time.Hour)), "day") + " ago"
	}
}
