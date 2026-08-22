package main

import "log/slog"

// withSessionJournal adds this invocation's session-fact handler to the capture
// fan-out, returning handlers unchanged when the store cannot be resolved.
//
// verb is the subcommand ("run", "affected") the recorded command line begins with;
// args is the rest of it.
//
// It is shared rather than written per command on purpose: `magus sessions` is a view
// of the repository, so a target result must land in the store in the same shape no
// matter which command produced it. Two copies of the wiring would drift, and the
// drift would read as a gap in the history rather than as a bug.
func withSessionJournal(handlers []slog.Handler, root, verb string, args []string) []slog.Handler {
	h := beginSessionJournal(root, append([]string{verb}, args...), version)
	if h == nil {
		return handlers
	}
	return append(handlers, h)
}
