package server

import (
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/proc"
)

// serverChildArgs turns a `server start` argv into the detached child's: the `start` verb
// becomes --foreground, and every flag the person passed (a --server-address, say) rides
// along so the child binds where the parent waits.
func serverChildArgs(argv []string) []string {
	out := make([]string, 0, len(argv)+1)
	replaced := false
	prev := ""
	for _, a := range argv {
		if !replaced && a == hint.ServerStart.Leaf() && prev == "server" {
			out = append(out, "--foreground")
			replaced = true
			prev = a
			continue
		}
		out = append(out, a)
		prev = a
	}
	return out
}

// servingSuffix names the workspaces a running server has loaded, or "" when it has none yet.
//
// One socket per user serves every workspace, so "already running" answered the question the
// caller asked and not the one they meant. Starting the server from a second worktree returns 0
// with nothing loaded from THIS tree, and the console then shows the tree it was started in,
// which reads as the command having worked. The roots are already on the status wire; the message
// never said them.
//
// The workspace this call was made from is not marked, deliberately: a server loads a workspace
// lazily on first use, so "not listed" means "not loaded yet" far more often than it means
// "wrong server", and flagging it would raise an alarm about the ordinary case.
func servingSuffix(st *proc.StatusReply) string {
	if st == nil || len(st.Workspaces) == 0 {
		return ""
	}
	roots := make([]string, 0, len(st.Workspaces))
	for _, w := range st.Workspaces {
		roots = append(roots, w.Root)
	}
	slices.Sort(roots)
	return ", serving " + strings.Join(roots, ", ")
}
