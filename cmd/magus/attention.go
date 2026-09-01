package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// The attention queue: blocks waiting on a person - what agents raised, and the one
// command that closes one. Both subverbs live under sessionCmd.
//
// Discovery is automated and disposition is not, which is the whole shape of the
// feature (docs/design.md, "Manual on purpose"). There is no expiry, no
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
		return "", fmt.Errorf("magus session: no magus workspace found from this directory, and the queue is per-repository; run it inside a workspace, or name one with --root <path>")
	}
	return root, nil
}

type attentionListOutput struct {
	Requests []sessions.AttentionRequest `json:"requests"`
	Store    string                      `json:"store"`
}

func attentionList(root string, args []string) error {
	root, err := attentionRoot(root)
	if err != nil {
		return err
	}
	rest, err := cmdParse("session attention", args, func(fs *flag.FlagSet) {
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
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus session attention: takes no arguments (got %q); close one request with `magus session dispose <id>`", rest[0])
	}

	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return err
	}
	requests := sessions.AttentionQueue(fold)

	// -q answers the question instead of printing it, before the output format is even
	// resolved: the caller asked for no output, so the format it would have used cannot
	// matter. errSilent carries the status without a message, because an empty queue is
	// the GOOD state and printing an error for it would train people to ignore the
	// command that reports a real block.
	if global.quiet {
		if len(requests) == 0 {
			return errSilent{exitCode: 1}
		}
		return nil
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		return renderAttentionText(requests, dir)
	case outputName:
		for _, req := range requests {
			fmt.Println(req.ID)
		}
		return nil
	}
	return emitFormatted(opts, attentionListOutput{Requests: requests, Store: dir})
}

func renderAttentionText(requests []sessions.AttentionRequest, dir string) error {
	if len(requests) == 0 {
		// An empty queue is the good state, so this says how a request would get here
		// rather than reporting a fault.
		fmt.Fprintf(os.Stdout, "no open attention requests in %s\n", dir)
		fmt.Fprintln(os.Stdout, "requests arrive through `magus session notify`: an agent hook raising an event whose outcome is waiting or permission opens one, and it stays open until someone runs `magus session dispose <id>`")
		return nil
	}

	now := time.Now()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tAGE\tOUTCOME\tSOURCE\tDELEGATION\tWHERE\tMESSAGE")
	for _, req := range requests {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			req.ID,
			orDash(formatDur(now.Sub(time.UnixMilli(req.OpenedMs)))),
			orDash(req.Outcome),
			orDash(req.Source),
			orDash(req.Delegation),
			orDash(req.Where),
			attentionOneLine(req.Message))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "\n%d open request(s); close one with `magus session dispose <id> -reason <text>`. Nothing here closes on its own.\n", len(requests))
	return nil
}

// attentionOneLine flattens a message onto the row it belongs to. An agent writes
// whatever it likes, newlines included, and one row per request is what makes the
// queue scannable; -o json carries the message verbatim.
func attentionOneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func attentionDispose(root string, args []string) error {
	root, err := attentionRoot(root)
	if err != nil {
		return err
	}
	var reason string
	rest, err := cmdParse("session dispose", args, func(fs *flag.FlagSet) {
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
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usagef("magus session dispose: needs exactly one request id (got %d); run `magus session attention` to list the open ids", len(rest))
	}

	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}
	req, err := sessions.DisposeRequest(dir, rest[0], reason, sessions.SessionStart{
		Workspace: root,
		Version:   version,
	})
	if err != nil {
		return disposeError(err, rest[0], dir)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputName {
		fmt.Println(req.ID)
		return nil
	}
	if opts.Format != outputText {
		return emitFormatted(opts, req)
	}
	fmt.Fprintf(os.Stdout, "disposed %s, open %s, raised by %s from session %s\n",
		req.ID,
		orDash(formatDur(time.Since(time.UnixMilli(req.OpenedMs)))),
		orDash(req.Source),
		req.Session)
	fmt.Fprintf(os.Stdout, "  %s\n", attentionOneLine(req.Message))
	if reason != "" {
		fmt.Fprintf(os.Stdout, "  reason: %s\n", reason)
	}
	return nil
}

// disposeError gives each refusal from [sessions.DisposeRequest] its next step. The
// store decides WHICH refusal applies - that is the identity contract - and this decides
// what a person does about it, which is why the store path and the listing command are
// added here rather than baked into the sentinel.
func disposeError(err error, ref, dir string) error {
	var (
		ambiguous *sessions.AmbiguousRequestError
		disposed  *sessions.DisposedError
	)
	switch {
	case errors.Is(err, sessions.ErrNoRequest):
		return fmt.Errorf("magus session dispose: no request matches %q in the session store at %s; run `magus session attention` to list the open ids", ref, dir)
	case errors.As(err, &ambiguous):
		return fmt.Errorf("magus session dispose: %w; name one of them, or add enough characters to tell them apart", ambiguous)
	case errors.As(err, &disposed):
		return fmt.Errorf("magus session dispose: %w; a request closes once and stays closed, so run `magus session attention` to see what is still open", disposed)
	}
	return err
}

// ---- producer ----

// recordAttentionOpen normalizes an event into an [sessions.AttentionOpen] and files it.
// The write lifecycle - dedupe, id derivation, the record itself - belongs to
// [sessions.OpenRequest]; what stays here is the two gates that decide whether an event
// is a request at all, and the flattening of the event onto the store's string fields.
//
// Only waiting and permission open one. Those two outcomes mean the work has STOPPED
// until a person acts; a failure or a finished run is news, and news that queued up
// for disposal would teach people to clear the queue without reading it.
func recordAttentionOpen(root string, ev types.Event) error {
	if ev.Outcome != types.OutcomeWaiting && ev.Outcome != types.OutcomePermission {
		return nil
	}
	if ev.Source.ID == "" {
		// An empty agent session is not an id, it is the absence of one, and
		// [sessions.RequestID] would happily digest it: every producer that sent
		// none would collapse onto ONE request, and a single dispose would close blocks
		// nobody had read. No id, no durable request - the same graceful path as no
		// repository, because the notification itself still fires.
		noteMissingAttentionSource()
		return nil
	}
	root = resolveRootOrEmpty(root)
	if root == "" {
		// No repository, so no queue to join. A notification raised outside a workspace
		// still notifies; it just has nowhere durable to live.
		return nil
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}

	open := sessions.AttentionOpen{
		Outcome:  string(ev.Outcome),
		Severity: string(ev.Severity),
		Source:   sessions.SourceLabel(ev.Source.Kind, ev.Source.Sub),
		Where:    attentionWhere(ev.Where),
		// Not an input to the id, on purpose - see sessions.RequestID. It rides the payload so
		// the queue can say WHOSE work is blocked without the row's identity moving when a
		// fleet re-partitions.
		Delegation: trail.DelegationFromEnv(),
		Message:    ev.Message,
	}
	_, _, err = sessions.OpenRequest(dir, ev.Source.ID, open, sessions.SessionStart{
		Workspace: root,
		Version:   version,
	})
	return err
}

// resolveRootOrEmpty resolves the repository a per-repository store belongs to,
// falling back to workspace discovery when the caller passed no --root.
//
// notify runs with no workspace loaded - it has to work from a hook, in whatever
// directory the agent happens to be in - so root arrives empty unless --root was
// passed. Left that way, every caller would key the store on nothing and share one
// bogus queue across every repository on the machine. An unresolvable root returns
// empty, which the caller reads as "not in a repository".
func resolveRootOrEmpty(root string) string {
	if root != "" {
		return root
	}
	resolved, err := magus.FindRoot("")
	if err != nil {
		return ""
	}
	return resolved
}

// attentionWhere renders where a person has to go to act. The workspace is always
// part of it: two worktrees of one repo share this store, and a block raised in one
// checkout is not the same block as the identical one raised in another.
func attentionWhere(where *types.EventLocation) string {
	if where == nil {
		return ""
	}
	if where.Project != nil && where.Project.Path != "" && where.Project.Path != "." {
		return where.Workspace.Value + " [" + where.Project.Path + "]"
	}
	return where.Workspace.Value
}

// noteAttentionOpenFailure reports a queue write that did not happen, without failing
// the notification. notify is invoked from agent hooks, where a non-zero exit
// interrupts the very session the notification exists to help - but a request that
// silently never reached the queue is a block nobody will ever be shown, so it says
// so rather than swallowing it.
func noteAttentionOpenFailure(err error) {
	slog.Warn("magus session notify: the attention request was not recorded, so `magus session attention` will not list it",
		slog.String("error", err.Error()))
}

// noteMissingAttentionSource reports the one event shape that cannot become a durable
// request. Like [noteAttentionOpenFailure] it leaves the notification itself alone -
// the desktop alert still fires - and says what the producer has to change, because
// an agent whose blocks never reach the queue has no other symptom.
func noteMissingAttentionSource() {
	slog.Warn("magus session notify: the event carries no source.id, so no attention request was opened; a request id keys on the agent session that raised the block, and an empty one would merge unrelated producers into a single row",
		slog.String("next", "have the agent wrapper send source.id, the host's own session identifier, in the event envelope"))
}
