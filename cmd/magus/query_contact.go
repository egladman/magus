package main

import (
	"io"

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
func printSessionContact(w io.Writer, root string, node types.KnowledgeNode) {
	if contact := sessionContact(root, node); contact != nil {
		sessions.PrintContact(w, *contact)
	}
}

// sessionContact is what the loaded sessions did to the file a node describes, or nil.
func sessionContact(root string, node types.KnowledgeNode) *sessions.PathContact {
	return sessions.ContactForNode(node, func() (string, error) { return sessions.Dir(resolveRootOrEmpty(root)) })
}
