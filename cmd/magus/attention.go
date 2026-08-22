package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/sessionjournal"
	"github.com/egladman/magus/types"
)

// attentionCmd is the queue of blocks waiting on a person: what agents raised, and
// the one command that closes one.
//
// Discovery is automated and disposition is not, which is the whole shape of the
// feature (docs/doctrine.md, "Manual on purpose"). There is no expiry, no
// auto-dispose flag and no severity inference to add later: a request that magus
// could close by itself would not have needed a person, and the queue exists
// precisely for the ones that do.
func attentionCmd(_ context.Context, root string, args []string) error {
	// The dispatcher hands this command the --root FLAG, which is empty unless somebody
	// passed one: attention loads no workspace, so nothing downstream resolves it. Left
	// empty it would key the store on "", and every repository on the machine would
	// share one queue that belongs to none of them.
	root = attentionRoot(root)
	if root == "" {
		return fmt.Errorf("magus attention: no magus workspace found from this directory, and the queue is per-repository; run it inside a workspace, or name one with --root <path>")
	}

	verb, rest := "ls", args
	// A leading flag belongs to the default verb, so `magus attention -o json` reads
	// as a listing rather than an unknown subcommand.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, rest = args[0], args[1:]
	}
	switch verb {
	case "help":
		attentionUsage()
		return nil
	case "ls":
		return attentionList(root, rest)
	case "dispose":
		return attentionDispose(root, rest)
	default:
		return usagef("magus attention: unknown subcommand %q (want ls or dispose); run `magus attention` to list open requests", verb)
	}
}

func attentionUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus attention [ls] [flags]")
	fmt.Fprintln(os.Stderr, "       magus attention dispose <id> [-note <text>]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "List the blocks agents have raised in this repository and close one.")
	fmt.Fprintln(os.Stderr, "A request is opened by `magus notify` when the event's outcome is waiting")
	fmt.Fprintln(os.Stderr, "or permission, and stays open until a person disposes of it. Nothing")
	fmt.Fprintln(os.Stderr, "closes a request automatically.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The queue is keyed by repository identity, so every git worktree of this")
	fmt.Fprintln(os.Stderr, "repo lists and disposes the same requests.")
}

type attentionListOutput struct {
	Requests []sessionjournal.AttentionRequest `json:"requests"`
	Store    string                            `json:"store"`
}

func attentionList(root string, args []string) error {
	rest, err := cmdParse("attention ls", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus attention ls [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "List every open request, oldest first. Disposed requests are not listed;")
			fmt.Fprintln(os.Stderr, "they stay in the journal and are visible with -o json after disposal.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus attention ls: takes no arguments (got %q); close one request with `magus attention dispose <id>`", rest[0])
	}

	dir, err := sessionjournal.Dir(root)
	if err != nil {
		return err
	}
	fold, err := sessionjournal.Read(dir)
	if err != nil {
		return err
	}
	requests := sessionjournal.OpenAttention(fold)

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

func renderAttentionText(requests []sessionjournal.AttentionRequest, dir string) error {
	if len(requests) == 0 {
		// An empty queue is the good state, so this says how a request would get here
		// rather than reporting a fault.
		fmt.Fprintf(os.Stdout, "no open attention requests in %s\n", dir)
		fmt.Fprintln(os.Stdout, "requests arrive through `magus notify`: an agent hook raising an event whose outcome is waiting or permission opens one, and it stays open until someone runs `magus attention dispose <id>`")
		return nil
	}

	now := time.Now()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tAGE\tOUTCOME\tSOURCE\tWHERE\tMESSAGE")
	for _, req := range requests {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			req.ID,
			orDash(formatDur(now.Sub(time.UnixMilli(req.OpenedMs)))),
			orDash(req.Outcome),
			orDash(req.Source),
			orDash(req.Where),
			attentionOneLine(req.Message))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "\n%d open request(s); close one with `magus attention dispose <id> -note <text>`. Nothing here closes on its own.\n", len(requests))
	return nil
}

// attentionOneLine flattens a message onto the row it belongs to. An agent writes
// whatever it likes, newlines included, and one row per request is what makes the
// queue scannable; -o json carries the message verbatim.
func attentionOneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func attentionDispose(root string, args []string) error {
	var note string
	rest, err := cmdParse("attention dispose", args, func(fs *flag.FlagSet) {
		fs.StringVar(&note, "note", "", "Record why the request is being closed, alongside the disposition")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus attention dispose <id> [-note <text>]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Close one open request. The disposition is appended to the session journal,")
			fmt.Fprintln(os.Stderr, "so every worktree of this repo sees the request close. A request closes")
			fmt.Fprintln(os.Stderr, "once: disposing an already-closed id is an error, not a second closure.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usagef("magus attention dispose: needs exactly one request id (got %d); run `magus attention` to list the open ids", len(rest))
	}
	id := rest[0]

	dir, err := sessionjournal.Dir(root)
	if err != nil {
		return err
	}
	fold, err := sessionjournal.Read(dir)
	if err != nil {
		return err
	}
	all := sessionjournal.Attention(fold)
	i := slices.IndexFunc(all, func(r sessionjournal.AttentionRequest) bool { return r.ID == id })
	if i < 0 {
		return fmt.Errorf("magus attention dispose: no request %q in the session journal at %s; run `magus attention` to list the open ids", id, dir)
	}
	req := all[i]
	if req.Disposed {
		return fmt.Errorf("magus attention dispose: request %s was already disposed by session %s at %s; a request closes once and stays closed, so run `magus attention` to see what is still open",
			id, req.DisposedBy, time.UnixMilli(req.DisposedMs).Format(time.RFC3339))
	}

	// A fresh session per disposal, because a disposal IS its own invocation: it is
	// what makes the journal say which run of magus closed the request, and by
	// extension which person was at the keyboard.
	w, err := sessionjournal.Open(dir, journal.NewInvocationID(), sessionjournal.SessionStart{
		Workspace: root,
		Command:   "attention dispose " + id,
		Version:   version,
	})
	if err != nil {
		return err
	}
	if err := w.Append(sessionjournal.KindAttentionDispose, sessionjournal.AttentionDispose{Request: id, Note: note}); err != nil {
		return err
	}

	// Re-read rather than patching the copy in hand: the disposal's timestamp and
	// session are whatever the journal stored, and a hand-assembled answer here could
	// disagree with what every other worktree is about to read.
	if fold, err = sessionjournal.Read(dir); err != nil {
		return err
	}
	all = sessionjournal.Attention(fold)
	if j := slices.IndexFunc(all, func(r sessionjournal.AttentionRequest) bool { return r.ID == id }); j >= 0 {
		req = all[j]
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
	if note != "" {
		fmt.Fprintf(os.Stdout, "  note: %s\n", note)
	}
	return nil
}

// ---- producer ----

// recordAttentionOpen files an agent-raised block as a durable open request, so a
// notification nobody happened to be looking at is still answerable afterwards, from
// any worktree of the repository.
//
// Only waiting and permission open one. Those two outcomes mean the work has STOPPED
// until a person acts; a failure or a finished run is news, and news that queued up
// for disposal would teach people to clear the queue without reading it.
//
// Re-filing a block that is already open is a no-op. An agent hook may fire on every
// prompt, and the queue has to hold one row per block rather than one per attempt -
// see [sessionjournal.RequestID] for how the two are told apart. An event carrying no
// Source.ID opens nothing, for the same reason: there is no request without the
// session that raised it.
func recordAttentionOpen(root string, ev types.Event) error {
	if ev.Outcome != types.OutcomeWaiting && ev.Outcome != types.OutcomePermission {
		return nil
	}
	if ev.Source.ID == "" {
		// An empty agent session is not an id, it is the absence of one, and
		// [sessionjournal.RequestID] would happily digest it: every producer that sent
		// none would collapse onto ONE request, and a single dispose would close blocks
		// nobody had read. No id, no durable request - the same graceful path as no
		// repository, because the notification itself still fires.
		noteMissingAttentionSource()
		return nil
	}
	root = attentionRoot(root)
	if root == "" {
		// No repository, so no queue to join. A notification raised outside a workspace
		// still notifies; it just has nowhere durable to live.
		return nil
	}
	dir, err := sessionjournal.Dir(root)
	if err != nil {
		return err
	}

	source, where := attentionSource(ev.Source), attentionWhere(ev.Where)
	// The AGENT's session, not this magus invocation's: a re-fire is a second magus
	// process, and keying on that would mint a fresh id every time.
	id := sessionjournal.RequestID(ev.Source.ID, source, where, ev.Message)

	fold, err := sessionjournal.Read(dir)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(sessionjournal.OpenAttention(fold), func(req sessionjournal.AttentionRequest) bool { return req.ID == id }) {
		return nil
	}

	w, err := sessionjournal.Open(dir, journal.NewInvocationID(), sessionjournal.SessionStart{
		Workspace: root,
		Command:   "notify",
		Version:   version,
	})
	if err != nil {
		return err
	}
	return w.Append(sessionjournal.KindAttentionOpen, sessionjournal.AttentionOpen{
		Request:  id,
		Outcome:  string(ev.Outcome),
		Severity: string(ev.Severity),
		Source:   source,
		Where:    where,
		Message:  ev.Message,
	})
}

// attentionRoot resolves the repository whose queue a request belongs in.
//
// notify runs with no workspace loaded - it has to work from a hook, in whatever
// directory the agent happens to be in - so root arrives empty unless --root was
// passed. Left that way, every caller would key the store on nothing and share one
// bogus queue across every repository on the machine. An unresolvable root returns
// empty, which the caller reads as "not in a repository".
func attentionRoot(root string) string {
	if root != "" {
		return root
	}
	resolved, err := magus.FindRoot("")
	if err != nil {
		return ""
	}
	return resolved
}

// attentionSource renders an event's producer as one addressable label, so the queue
// column and the request id agree on what "who raised this" means.
func attentionSource(s types.EventSource) string {
	if s.Sub == "" {
		return s.Kind
	}
	return s.Kind + "/" + s.Sub
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
	slog.Warn("magus notify: the attention request was not recorded, so `magus attention` will not list it",
		slog.String("error", err.Error()))
}

// noteMissingAttentionSource reports the one event shape that cannot become a durable
// request. Like [noteAttentionOpenFailure] it leaves the notification itself alone -
// the desktop alert still fires - and says what the producer has to change, because
// an agent whose blocks never reach the queue has no other symptom.
func noteMissingAttentionSource() {
	slog.Warn("magus notify: the event carries no source.id, so no attention request was opened; a request id keys on the agent session that raised the block, and an empty one would merge unrelated producers into a single row",
		slog.String("next", "have the agent wrapper send source.id, the host's own session identifier, in the event envelope"))
}
