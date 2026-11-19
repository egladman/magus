package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/types"
)

// The attention queue: blocks waiting on a person (what agents raised, and the one
// command that closes one). Both subverbs live under sessionCommand.
//
// Discovery is automated and disposition is not, which is the whole shape of the
// feature (docs/design/judgment/manual-on-purpose.md). There is no expiry, no
// auto-dispose flag and no severity inference to add later: a request that magus
// could close by itself would not have needed a person, and the queue exists
// precisely for the ones that do.

// attentionRoot resolves the repository the queue belongs to. The dispatcher hands
// these subverbs the --root FLAG, which is empty unless somebody passed one: the queue
// loads no workspace, so nothing downstream resolves it. Left empty it would key the
// store on "", and every repository on the machine would share one queue that belongs
// to none of them.
func attentionRoot(root string) (string, error) {
	root = resolveRootOrEmpty(root)
	if root == "" {
		return "", fmt.Errorf("magus session: no magus workspace found from this directory, and the queue is per-repository, run it inside a workspace or name one with --root <path>")
	}
	return root, nil
}

type attentionListOutput struct {
	Requests []sessions.AttentionRequest `json:"requests"`
	Store    string                      `json:"store"`
}

func attentionList(ctx context.Context, root string, args []string) error {
	root, err := attentionRoot(root)
	if err != nil {
		return err
	}
	rest, err := commandParse("session attention", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus session attention [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "List every open request, oldest first. Disposed requests are not listed;")
			fmt.Fprintln(os.Stderr, "they stay in the session store and are visible with -o json after disposal.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "With -q, print nothing and report the queue as an exit status: 0 when at")
			fmt.Fprintln(os.Stderr, "least one request is open, 1 when the queue is empty. Nothing is wrong in")
			fmt.Fprintln(os.Stderr, "either case; the status answers `is anyone waiting on me` for a shell")
			fmt.Fprintln(os.Stderr, "prompt or a wrapper script.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return exit.Usagef("magus session attention: takes no arguments (got %q); close one request with `%s`", rest[0], hint.SessionDispose.With("<id>"))
	}

	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}
	fold, err := session.ReadAll(ctx, dir)
	if err != nil {
		return err
	}
	requests := sessions.AttentionQueue(fold)

	// -q answers the question instead of printing it, before the output format is even
	// resolved: the caller asked for no output, so the format it would have used cannot
	// matter. exit.Reported carries the status without a message, because an empty queue is
	// the GOOD state and printing an error for it would train people to ignore the
	// command that reports a real block.
	if global.quiet {
		if len(requests) == 0 {
			return exit.Reported(exit.Failure)
		}
		return nil
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		return sessions.RenderAttentionText(os.Stdout, requests, dir)
	case outputName:
		names := make([]string, 0, len(requests))
		for _, req := range requests {
			names = append(names, req.ID)
		}
		return emitNames(os.Stdout, names)
	case outputJSON, outputJSONL, outputTemplate, outputYAML:
	}
	return emitFormatted(opts, attentionListOutput{Requests: requests, Store: dir})
}

func attentionDispose(ctx context.Context, root string, args []string) error {
	root, err := attentionRoot(root)
	if err != nil {
		return err
	}
	var reason string
	rest, err := commandParse("session dispose", args, func(fs *flag.FlagSet) {
		fs.StringVar(&reason, "reason", "", "Record why the request is being closed, alongside the disposition")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus session dispose <id> [-reason <text>]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Close one open request. The disposition is appended to the session store,")
			fmt.Fprintln(os.Stderr, "so every worktree of this repo sees the request close. A request closes")
			fmt.Fprintln(os.Stderr, "once: disposing an already-closed id is an error, not a second closure.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "<id> may be any unambiguous prefix of a request id, the way a short")
			fmt.Fprintln(os.Stderr, "revision names a commit. An ambiguous prefix is refused and lists what it")
			fmt.Fprintln(os.Stderr, "matched.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return exit.Usagef("magus session dispose: needs exactly one request id (got %d); run `%s` to list the open ids", len(rest), hint.SessionAttention.String())
	}

	// Disposing closes a request that exists to reach a PERSON (docs/design/judgment/manual-on-purpose.md,
	// "Manual on purpose"): an agent answering its own block, or another agent's,
	// removes the person the queue exists to reach. The guard denies this outright
	// (rule agent-sign-off), but it fails OPEN where it is not wired, the same gap
	// --ack closed this way first (diff.go).
	if err := requirePerson(ctx, "session dispose records that a person closed this request"); err != nil {
		return err
	}

	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}
	req, err := sessions.DisposeRequest(ctx, dir, rest[0], reason, session.InvocationStart{
		Origin:    localOrigin(ctx, jobtypes.EntryPointCLI),
		Workspace: root,
		Version:   version,
	})
	if err != nil {
		return sessions.DisposeError(err, rest[0], dir)
	}
	if err := job.ReleaseRequestDisposed(ctx, root, req); err != nil {
		slog.WarnContext(ctx, fmt.Sprintf("disposed %s, but its holder's row still carries it", req.ID), attr.Error(err), attr.Unchecked())
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitRecord(opts, req, []string{req.ID})
	}
	fmt.Fprintf(os.Stdout, "disposed %s, open %s, raised by %s from invocation %s\n",
		req.ID,
		orDash(sessions.FormatDur(time.Since(time.UnixMilli(req.OpenedMs)))),
		orDash(req.Source),
		req.Invocation)
	fmt.Fprintf(os.Stdout, "  %s\n", sessions.OneLine(req.Message))
	if reason != "" {
		fmt.Fprintf(os.Stdout, "  reason: %s\n", reason)
	}
	return nil
}

// resolveRootOrEmpty resolves the repository a per-repository store belongs to,
// falling back to workspace discovery when the caller passed no --root.
//
// notify runs with no workspace loaded (it has to work from a hook, in whatever
// directory the agent happens to be in), so root arrives empty unless --root was
// passed. Left that way, every caller would key the store on nothing and share one
// bogus queue across every repository on the machine. An unresolvable root returns
// empty, which the caller reads as "not in a repository".
func resolveRootOrEmpty(root string) string {
	if root != "" {
		return root
	}
	resolved, err := workspace.FindRoot("")
	if err != nil {
		return ""
	}
	return resolved
}

// noteAttentionOpenFailure reports a queue write that did not happen, without failing
// the notification. notify is invoked from agent hooks, where a non-zero exit
// interrupts the very session the notification exists to help, but a request that
// silently never reached the queue is a block nobody will ever be shown, so it says
// so rather than swallowing it.
func noteAttentionOpenFailure(err error) {
	slog.With(attr.Component("session notify")).Warn(fmt.Sprintf("the attention request was not recorded, so `%s` will not list it", hint.SessionAttention.String()),
		attr.Error(err))
}

// fileAttention files an event as an attention request of the repository at root; see
// [job.FileAttention] for which events open one.
func fileAttention(ctx context.Context, root string, ev types.Event) (held bool, err error) {
	return job.FileAttention(ctx, resolveRootOrEmpty(root), ev, localOrigin(ctx, jobtypes.EntryPointHook), version, jobPlace)
}
