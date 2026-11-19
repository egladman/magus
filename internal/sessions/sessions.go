// Package sessions is magus's use of the session store in libs/safetyscissors/session:
// where a repository's store lives, and the records magus adds to it.
//
// The store is a third one beside the two that already exist, and SCOPE is what tells
// the three apart:
//
//   - internal/journal is the EXECUTION JOURNAL, scoped to a run: every line one
//     invocation emitted (exec, output, result), discarded with that invocation.
//   - internal/trail is the ACTIVITY TRAIL, scoped to a machine: consequential
//     actions taken against the server.
//   - the session store holds INVOCATIONS, scoped to a repository: the durable facts
//     of each, folded across worktrees. It answers "what has been happening in this
//     repo lately", which neither of the others can, because one is per-run and the
//     other is per-server.
//
// The record writer, reader, fold and pruning are the module's. What reads magus's
// version control or user state stays here: [Dir] resolves the store, and the kinds
// magus adds (attention, gates, checkpoints, advice, contact) are folded here. An
// attention request straddles two invocation files, so every writer here opens with
// [AttentionTie], which keeps pruning from splitting one.
package sessions

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/vcs"
)

// Dir resolves the per-repository session store,
// <XDG state>/magus/sessions/<repo-basename>-<hash12>.
//
// The hash keys on repository IDENTITY rather than the checkout path, which is the
// whole point: every worktree AND every clone of one repo resolves to the same
// directory, so a session started in one is visible from another. Keyed through
// vcs.StateDir so it agrees with every other per-repo store about what a repo is.
func Dir(root string) (string, error) {
	base, err := xdg.StateDir()
	if err != nil {
		return "", fmt.Errorf("sessions: resolve state dir, set XDG_STATE_HOME to a writable absolute path: %w", err)
	}
	dir, err := vcs.StateDir(base, "sessions", root)
	if err != nil {
		return "", fmt.Errorf("sessions: %w", err)
	}
	return dir, nil
}

// StoreConfig is the [session.Config] every magus writer of the store at dir opens with:
// [session.DefaultRetention], and a prune error logged as a warning, since housekeeping
// never fails a command.
func StoreConfig(dir, invocation string, start session.InvocationStart) session.Config {
	return session.Config{
		Dir: dir, Invocation: invocation, Start: start,
		Retention:   session.DefaultRetention,
		PruneErrors: warnPrune,
	}
}

func warnPrune(ctx context.Context, err error) {
	slog.With(attr.Component("magus")).WarnContext(ctx, "the session store could not prune itself; it keeps growing until a later prune succeeds", attr.Error(err))
}

// openWriter is [session.Open] with the attention tie every magus writer prunes with.
func openWriter(ctx context.Context, dir, invocation string, start session.InvocationStart) (*session.Writer, error) {
	return session.Open(ctx, StoreConfig(dir, invocation, start), session.WithTie(AttentionTie()))
}
