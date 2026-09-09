package main

import (
	"bufio"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/repoid"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// sessionsDefaultLimit bounds the default listing to a session or two of scrollback.
// The store is grow-only, so an unbounded default would get slower forever.
const sessionsDefaultLimit = 20

// sessionCmd is the whole session family behind one noun. The grouping is the point:
// every subverb reads or writes the one store keyed by repository identity, none needs
// the magusfile to load, and the machine ingest (hook, notify) lives beside the human
// reads instead of burning top-level names no person types.
//
// The listing is NOT the console's Activity surface: Activity reads internal/trail
// (actions against the daemon), this reads internal/sessions (facts a session
// produced). The stores are meant to converge; until they do, each is named after
// what it holds.
func sessionCmd(ctx context.Context, root string, args []string) error {
	verb, rest := "ls", args
	// A leading flag belongs to the default verb, so `magus session -o json` and
	// `magus session --since 2h` read as listings rather than unknown subcommands.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, rest = args[0], args[1:]
	}
	switch verb {
	case "help":
		sessionUsage()
		return nil
	case "ls":
		return sessionList(ctx, root, rest)
	case "load":
		return sessionLoad(root, rest)
	case "show":
		return sessionShow(root, rest)
	case "attention":
		return attentionList(root, rest)
	case "dispose":
		return attentionDispose(root, rest)
	case "checkpoint":
		return checkpointCmd(ctx, root, os.Stdin, os.Stdout, rest)
	case "hook":
		return hookCmd(ctx, os.Stdin, os.Stdout, rest)
	case "notify":
		return notifyCmd(ctx, root, os.Stdin, os.Stdout, rest)
	default:
		return usagef("magus session: unknown subcommand %q (want ls, show, load, checkpoint, attention, dispose, hook, or notify); the bare command lists recent sessions, bounded by --limit and --since", verb)
	}
}

func sessionUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus session [ls] [--limit <n>] [--since <when>]")
	fmt.Fprintln(os.Stderr, "       magus session show <session-id>")
	fmt.Fprintln(os.Stderr, "       magus session load [--file <path>]")
	fmt.Fprintln(os.Stderr, "       magus session attention [flags]")
	fmt.Fprintln(os.Stderr, "       magus session dispose <id> [-reason <text>]")
	fmt.Fprintln(os.Stderr, "       magus session checkpoint [--note <text>]")
	fmt.Fprintln(os.Stderr, "       magus session hook [flags]      # machine: guard verdicts, wired by agent hosts")
	fmt.Fprintln(os.Stderr, "       magus session notify [flags]    # machine: event ingest, wired by agent hosts")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "One store, two sides. Humans read it: `session` lists what recent sessions")
	fmt.Fprintln(os.Stderr, "did across every worktree of this repository, `session show` reports one of")
	fmt.Fprintln(os.Stderr, "them in full, `session attention` lists the blocks agents raised, and")
	fmt.Fprintln(os.Stderr, "`session dispose` closes one - nothing closes a request automatically.")
	fmt.Fprintln(os.Stderr, "Humans write it too: `session checkpoint` records where work stands before")
	fmt.Fprintln(os.Stderr, "you put it down. Agent hosts write it from hooks, through `session hook`")
	fmt.Fprintln(os.Stderr, "and `session notify`, and `session load` takes a normalized event stream")
	fmt.Fprintln(os.Stderr, "extracted from a host's own transcript.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Run `magus session <subcommand> -h` for each subverb's flags.")
}

// sessionList shows what recent magus sessions did, folded across every worktree of
// this repository.
func sessionList(ctx context.Context, root string, args []string) error {
	var limit int
	var since string
	rest, err := cmdParse("session", args, func(fs *flag.FlagSet) {
		fs.IntVar(&limit, "limit", sessionsDefaultLimit, "Show at most this many sessions (0 for all)")
		fs.StringVar(&since, "since", "", "Show only sessions active since this point: a duration back from now (2h, 45m, 168h) or an RFC3339 timestamp")
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus session: takes no arguments (got %q); use --limit to bound the listing and --since to bound its age", rest[0])
	}
	if limit < 0 {
		return usagef("magus session: --limit must be zero or more (got %d); 0 lists every session", limit)
	}
	cutoff, err := parseSince(since)
	if err != nil {
		return err
	}

	root = resolveRootOrEmpty(root)
	if root == "" {
		return fmt.Errorf("magus session: no workspace here: the session store is keyed by repository, so run from inside one or pass --root <path>")
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return err
	}
	summaries := sessions.Summarize(fold)
	summaries = sessionsSince(summaries, cutoff)
	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		return renderSessionsText(ctx, root, summaries, fold, dir, !cutoff.IsZero())
	case outputName:
		for _, s := range summaries {
			fmt.Println(s.Session)
		}
		return nil
	}
	return emitFormatted(opts, map[string]any{
		"sessions":    summaries,
		"checkpoints": sessions.LatestCheckpoints(fold),
		"skipped":     fold.Skipped,
		"store":       dir,
	})
}

// filtered says a --since cutoff was applied, which is what separates an empty store
// from a store whose sessions are all older than the window. Reporting the second as the
// first sends a person looking for a broken producer.
func renderSessionsText(ctx context.Context, root string, summaries []sessions.Summary, fold sessions.Fold, dir string, filtered bool) error {
	renderCheckpoints(ctx, root, os.Stdout, sessions.LatestCheckpoints(fold))
	if len(summaries) == 0 {
		if filtered {
			fmt.Fprintf(os.Stdout, "no sessions in that window; %s holds %d session file(s)\n", dir, fold.Sessions)
			fmt.Fprintln(os.Stdout, "widen --since, or drop it to list every session")
			return nil
		}
		// An empty store is the normal state before any run, so this says how facts
		// get there rather than reporting a fault.
		fmt.Fprintf(os.Stdout, "no sessions recorded yet in %s\n", dir)
		fmt.Fprintln(os.Stdout, "magus records one as soon as a `"+hint.Run.String()+"` finishes a target in any worktree of this repository")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	bySpan := make(map[string]string, len(summaries))
	for _, s := range summaries {
		if s.SpanID != "" {
			bySpan[s.SpanID] = s.Session
		}
	}
	fmt.Fprintln(tw, "SESSION\tLAST\tHOST\tLEASE\tSPAWNER\tPARENT\tFACTS\tEVENTS\tTARGETS")
	for _, s := range summaries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			s.Session,
			time.UnixMilli(s.LastMs).Format("2006-01-02 15:04:05"),
			orDash(s.Host),
			orDash(s.Lease),
			orDash(s.Spawner),
			orDash(sessionParent(bySpan, s.ParentSpanID)),
			s.Facts,
			// A dash rather than a zero: no loaded events is the ordinary state, and a
			// column of zeros reads as a producer that broke.
			orDash(loadedEvents(s.Events)),
			orDash(summarizeTargets(s.Targets)))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if open := len(sessions.AttentionQueue(fold)); open > 0 {
		// A reader of the history is often looking for what needs them; one line
		// points at the other view of the same store.
		fmt.Fprintf(os.Stdout, "\n%d attention request(s) open; `%s` lists them\n", open, hint.SessionAttention)
	}

	if fold.Skipped > 0 {
		// Surfaced rather than swallowed: a skipped line is a fact that happened and
		// cannot be shown, which is exactly the thing a silent reader would misreport
		// as "nothing happened".
		fmt.Fprintf(os.Stdout, "\n%d unreadable line(s) skipped (a session killed mid-write leaves a partial line; the rest of its records still read)\n", fold.Skipped)
	}
	return nil
}

// checkpointsShown bounds the block. A checkpoint from three weeks ago is still where that
// work sits, so the list is bounded by count rather than by age, and the tail is pointed at
// rather than dropped silently.
//
// It is NOT unbounded in time, whatever this comment used to say: sessions.Prune drops a
// whole session file 30 days after its newest record, with an exemption for open attention
// requests and none for checkpoints. A line of work parked for a month loses every
// checkpoint it had, silently. Either that exemption is owed or this block should say so.
const checkpointsShown = 3

// renderCheckpoints prints unfinished work above the history, because "where was I" is
// the question a person opens this listing with, and the history answers a different one.
//
// It is deliberately not filtered by --since. That flag bounds what RAN recently; a
// checkpoint records what has not finished, and hiding an old one would hide the case
// this exists for.
func renderCheckpoints(ctx context.Context, root string, w io.Writer, checkpoints []sessions.CheckpointRecord) {
	if len(checkpoints) == 0 {
		return
	}
	fmt.Fprintln(w, "Checkpoints, most recent first:")
	for _, r := range checkpoints[:min(len(checkpoints), checkpointsShown)] {
		fmt.Fprintf(w, "  %s  %s\n", r.At.Format("2006-01-02 15:04:05"), checkpointWhere(r.Checkpoint))
		if r.Note != "" {
			fmt.Fprintf(w, "    %s\n", r.Note)
		}
		if r.HostSession != "" {
			fmt.Fprintf(w, "    session %s\n", r.HostSession)
		}
		if r.Transcript != "" {
			fmt.Fprintf(w, "    transcript %s\n", r.Transcript)
		}
	}
	if extra := len(checkpoints) - checkpointsShown; extra > 0 {
		fmt.Fprintf(w, "  and %d more; `%s` lists them all\n", extra, hint.Session.With("-o", "json"))
	}
	printCheckpointInspect(ctx, root, w, checkpoints[0].Checkpoint)
	fmt.Fprintln(w)
}

// printCheckpointInspect prints the command that shows what changed since the newest
// checkpoint, already substituted, for THIS repository's backend.
//
// Composed by the DRIVER, never here. magus drives git, Mercurial, Sapling and Jujutsu,
// and each spells this differently; a reader who assembles one from memory is guessing
// which of the four they are in, and the same guess is what a skill hardcoding `git
// diff` teaches them to make. DiffCommands is the one method that already knows, and
// until now it had a single caller.
//
// Best-effort and silent on failure: a revisionless checkpoint has no base to diff from,
// and a listing that refuses to print because a VCS probe failed would be worse than one
// that prints what it has. Only for the newest, because that is the one a reader acts on
// and each call costs a subprocess.
func printCheckpointInspect(ctx context.Context, root string, w io.Writer, c sessions.Checkpoint) {
	if root == "" || c.Tree.Revision == "" {
		return
	}
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return
	}
	hints, err := res.VCS.DiffCommands(ctx, root, c.Tree.Revision)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "  what changed since the newest:\n    %s\n", hints.CLI)
	if hints.GUI != "" {
		fmt.Fprintf(w, "    %s\n", hints.GUI)
	}
}

// checkpointWhere reads one back as a phrase. The revision leads because it is the fact
// a reader acts on: a checkout that cannot resolve it is looking at work that was never
// pushed, which no branch name would have revealed.
//
// A checkpoint with no revision is not a broken record. A tree before its first commit,
// or under no VCS at all, still has a location worth naming, and the workspace is it.
func checkpointWhere(c sessions.Checkpoint) string {
	var b strings.Builder
	if c.Host != "" {
		fmt.Fprintf(&b, "%s ", c.Host)
	}
	if c.Tree.Revision == "" {
		b.WriteString(c.Workspace)
		return b.String()
	}
	b.WriteString(shortRev(c.Tree.Revision))
	if c.Tree.Branch != "" {
		fmt.Fprintf(&b, " on %s", c.Tree.Branch)
	}
	if c.Tree.Dirty {
		b.WriteString(", uncommitted changes")
	}
	return b.String()
}

// sessionParent names the session a span id belongs to, falling back to the raw id when this
// store holds no record of it. The id is the honest answer rather than a blank: the parent may
// have run in a repository this store does not cover, or before the retention window, and
// "unknown to magus" is a different fact from "spawned by nobody".
//
// Resolved for the TEXT view only. -o json carries the ids, because a consumer joining sessions
// wants the identity rather than this reader's guess at a name for it.
func sessionParent(bySpan map[string]string, parentSpanID string) string {
	if session, ok := bySpan[parentSpanID]; ok {
		return session
	}
	return parentSpanID
}

// summarizeTargets renders a session's targets as "<target> <project> (<outcome>)",
// collapsing a repeated target so a session that ran one target over forty projects
// does not render forty times.
func summarizeTargets(targets []sessions.TargetResult) string {
	seen := map[string]bool{}
	var out []string
	for _, t := range targets {
		label := t.Target
		if t.Project != "" {
			label += " " + t.Project
		}
		label += " (" + t.Outcome
		if t.Replayed {
			label += ", cached"
		}
		label += ")"
		if seen[label] {
			continue
		}
		seen[label] = true
		out = append(out, label)
	}
	return strings.Join(out, ", ")
}

func loadedEvents(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// parseSince turns a --since value into the earliest last-fact time worth listing. An
// empty value returns the zero time, which admits everything.
//
// Two spellings, because the question has two shapes: "what has happened lately" is a
// duration back from now, and "what happened since the incident" is an instant. A bare
// duration is accepted as written rather than requiring a leading "-": the flag already
// says "since", and a caller who wrote --since -2h would be asking for the future.
func parseSince(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d < 0 {
			d = -d
		}
		return time.Now().Add(-d), nil
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, nil
	}
	return time.Time{}, usagef("magus session: --since %q is neither a duration nor an RFC3339 timestamp; write a duration back from now (2h, 45m, 168h) or an instant (2006-01-02T15:04:05Z)", raw)
}

// sessionsSince drops the sessions whose last fact predates cutoff.
//
// It filters on LastMs rather than StartedMs so a long-lived session that is still
// working stays listed. The alternative hides exactly the session a person asking "what
// is happening lately" most wants: one that began before the window and has not stopped.
func sessionsSince(summaries []sessions.Summary, cutoff time.Time) []sessions.Summary {
	if cutoff.IsZero() {
		return summaries
	}
	ms := cutoff.UnixMilli()
	out := make([]sessions.Summary, 0, len(summaries))
	for _, s := range summaries {
		if s.LastMs >= ms {
			out = append(out, s)
		}
	}
	return out
}


// `magus session load` and `magus session show`: the read and write sides of a
// host transcript loaded into the session store.
//
// The guard's trail records what the hook SAW. A transcript records what actually
// ran, including the commands no hook was wired for, the skills a session loaded,
// and what the hook printed back. Neither store answers "did this session comply"
// alone, and the join is what makes the question answerable at all.
//
// Extraction is not here and never will be: a per-host recipe the reader owns
// turns a transcript into the stream below, exactly as `magus agent adoption`
// takes a corpus rather than reading a host's logs. magus takes normalized events
// in its own vocabulary, so a host renaming a tool costs its reader one config
// line instead of costing magus a release.

// loadEvent is one line of the stream. The field names are the guard envelope's
// where they overlap, since a recipe author is already reading that contract.
type loadEvent struct {
	Host       string `json:"host"`
	Session    string `json:"session"`
	Ts         int64  `json:"ts"`
	Cwd        string `json:"cwd"`
	Kind       string `json:"kind"`
	Ref        string `json:"ref"`
	Text       string `json:"text"`
	Transcript string `json:"transcript"`
	Outcome    struct {
		Exit        int  `json:"exit"`
		Denied      bool `json:"denied"`
		Interrupted bool `json:"interrupted"`
	} `json:"outcome"`
}

// loadMaxLineBytes bounds one event. A hook's output is the largest thing a line
// legitimately carries and it is prose, so a line past this was produced by a
// recipe piping something else entirely.
const loadMaxLineBytes = 1 << 20

// loadHookTextCap bounds what a hook.output event stores. The text is magus's own
// prose, so the head of it identifies which advisory or denial fired; the rest is
// the same paragraph every session already has.
const loadHookTextCap = 2000

// loadRejectsShown bounds the diagnostics a failed load prints. A recipe that
// emits one bad line emits thousands, and the first few say which field is wrong.
const loadRejectsShown = 5

type sessionLoadSummary struct {
	Loaded   int            `json:"loaded"`
	Deduped  int            `json:"deduped"`
	Dropped  int            `json:"dropped_other_repo"`
	Rejected int            `json:"rejected"`
	ByKind   map[string]int `json:"by_kind,omitempty"`
	Store    string         `json:"store"`
}

func sessionLoadUsage(fs *flag.FlagSet) func() {
	return func() {
		fmt.Fprintln(os.Stderr, "Usage: magus session load [--file <path>]   # the event stream arrives on stdin")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Load a normalized agent-session event stream into this repository's session")
		fmt.Fprintln(os.Stderr, "store: one JSON object per line, host vocabulary already mapped onto magus's.")
		fmt.Fprintln(os.Stderr, "Every line needs host, session, kind and ref; kind is one of "+strings.Join(sessions.EventKinds, ", ")+".")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Events are keyed on (host, session, ref), so re-running a recipe over the same")
		fmt.Fprintln(os.Stderr, "transcript loads nothing twice. Events whose cwd belongs to another repository")
		fmt.Fprintln(os.Stderr, "are dropped; worktrees of this one are kept.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "A shell command's text is never stored. It is re-judged against today's guard")
		fmt.Fprintln(os.Stderr, "rules and kept as its program, the verdict, the rule behind it, and a digest.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
		fs.PrintDefaults()
	}
}

// sessionLoad implements `magus session load`.
func sessionLoad(root string, args []string) error {
	var file string
	rest, err := cmdParse("session load", args, func(fs *flag.FlagSet) {
		fs.StringVar(&file, "file", "", "Read the event stream from this file instead of stdin")
		fs.Usage = sessionLoadUsage(fs)
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus session load: takes no arguments (got %q); the stream arrives on stdin, or name a file with --file", rest[0])
	}
	root = resolveRootOrEmpty(root)
	if root == "" {
		return fmt.Errorf("magus session load: no workspace here: the session store is keyed by repository, so run from inside one or pass --root <path>")
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}

	in := io.Reader(os.Stdin)
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("magus session load: open %s: %w", file, err)
		}
		defer func() { _ = f.Close() }()
		in = f
	}

	events, summary, rejects, err := readLoadStream(in, dir)
	if err != nil {
		return err
	}
	result, err := sessions.LoadEvents(dir, events, sessions.SessionStart{
		Workspace: root,
		Command:   "session load",
		Version:   version,
	})
	if err != nil {
		return err
	}
	summary.Loaded, summary.Deduped, summary.Store = result.Loaded, result.Deduped, dir
	summary.ByKind = result.ByKind

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputText {
		renderLoadSummary(os.Stdout, summary)
	} else if err := emitFormatted(opts, summary); err != nil {
		return err
	}
	if len(rejects) > 0 {
		return fmt.Errorf("magus session load: %d line(s) rejected and not loaded:\n  %s",
			len(rejects), strings.Join(rejects[:min(len(rejects), loadRejectsShown)], "\n  "))
	}
	return nil
}

// readLoadStream decodes the stream, judges what it must, and returns the events
// worth storing alongside the counts and the per-line diagnostics.
//
// A rejected line does not stop the read. A recipe emitting one bad shape emits it
// for a whole transcript, and loading the rest is what lets the reader fix the
// recipe and re-run without losing what already worked.
func readLoadStream(in io.Reader, dir string) ([]sessions.LoadEvent, sessionLoadSummary, []string, error) {
	var summary sessionLoadSummary
	var events []sessions.LoadEvent
	var rejects []string

	sameRepo := repoScope(dir)
	rootOf := checkoutRoots()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), loadMaxLineBytes)
	for line := 1; sc.Scan(); line++ {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var ev loadEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			rejects = append(rejects, fmt.Sprintf("line %d: not a JSON object: %v", line, err))
			continue
		}
		if reason := validateLoadEvent(ev); reason != "" {
			rejects = append(rejects, fmt.Sprintf("line %d: %s", line, reason))
			continue
		}
		if ev.Cwd != "" && !sameRepo(ev.Cwd) {
			summary.Dropped++
			continue
		}
		// A host names files by absolute path; graph file nodes are keyed by the
		// path inside the checkout, and every worktree of one repository shares
		// that layout. Storing the checkout-relative path is what lets the
		// @session shard land the event on a node.
		if (ev.Kind == sessions.EventFileRead || ev.Kind == sessions.EventFileWrite) && filepath.IsAbs(ev.Text) {
			if root := rootOf(ev.Cwd); root != "" {
				if rel, err := filepath.Rel(root, ev.Text); err == nil && !strings.HasPrefix(rel, "..") {
					ev.Text = filepath.ToSlash(rel)
				}
			}
		}
		events = append(events, sessions.LoadEvent{Session: ev.Session, Event: storedEvent(ev)})
	}
	if err := sc.Err(); err != nil {
		return nil, summary, rejects, fmt.Errorf("magus session load: read stream: %w", err)
	}
	summary.Rejected = len(rejects)
	return events, summary, rejects, nil
}

func validateLoadEvent(ev loadEvent) string {
	switch {
	case ev.Host == "":
		return "no host"
	case ev.Session == "":
		return "no session"
	case ev.Ref == "":
		return "no ref, so the event cannot be deduplicated"
	case !sessions.ValidEventKind(ev.Kind):
		return fmt.Sprintf("kind %q is not one of %s", ev.Kind, strings.Join(sessions.EventKinds, ", "))
	}
	if err := sessions.ValidSessionID(ev.Session); err != nil {
		return err.Error()
	}
	return ""
}

// storedEvent is what a wire event becomes on disk. A shell command loses its text
// here and nowhere else, so this is the one function to read when asking whether a
// command line can reach the store.
func storedEvent(ev loadEvent) sessions.AgentEvent {
	out := sessions.AgentEvent{
		Host:        ev.Host,
		Event:       ev.Kind,
		Ref:         ev.Ref,
		At:          ev.Ts,
		Transcript:  ev.Transcript,
		Exit:        ev.Outcome.Exit,
		Denied:      ev.Outcome.Denied,
		Interrupted: ev.Outcome.Interrupted,
	}
	switch ev.Kind {
	case sessions.EventShellCommand:
		sum := sha256.Sum256([]byte(ev.Text))
		out.Digest = hex.EncodeToString(sum[:])
		out.Program, out.Verdict, out.Rule = rejudgeCommand(ev.Text)
	case sessions.EventHookOutput:
		out.Text = ev.Text
		if len(out.Text) > loadHookTextCap {
			out.Text = out.Text[:loadHookTextCap]
		}
	default:
		out.Text = ev.Text
	}
	return out
}

// rejudgeCommand runs a past command through today's rules and reports what they
// would say now: the program, the verdict, and the rule or advisory behind it.
//
// It calls the pure rule set rather than the hook, so no subprocess runs and no
// live workspace state is read. The rules that DO read the filesystem (the sibling
// checkout, the stale binary notice) are deliberately skipped: they describe the
// machine at the moment of the call, and applying today's machine to a command
// from three weeks ago would be inventing a verdict nobody ever saw.
//
// The rule's argument is dropped along with the text: it renders the resolved
// argv, which is the content this whole path exists to keep out of the store.
func rejudgeCommand(text string) (program, verdict, rule string) {
	program = commandProgram(text)
	v := evaluateBashGuard(text)
	switch {
	case v.Deny != "":
		return program, "deny", string(v.Rule.Name)
	case v.Context != "":
		return program, "advise", string(v.Kind)
	}
	return program, "pass", ""
}

// commandProgram names the program a line runs, wrappers already peeled. A line
// the shell parser cannot read still has a first word worth grouping by.
func commandProgram(text string) string {
	if cmds, parsed := parseGuardCommands(text); parsed && len(cmds) > 0 {
		return cmds[0].Name
	}
	if fields := strings.Fields(text); len(fields) > 0 {
		return path.Base(fields[0])
	}
	return ""
}

// repoScope reports whether a cwd belongs to the repository whose store is dir,
// memoized because a stream names a handful of checkouts across thousands of
// events and each answer costs a config read.
//
// A checkout that no longer exists identifies as its own path, so events from a
// DELETED worktree are dropped as another repository's. That is the largest known
// hole in a load: the phase 0 measurement found 45% of commands were run in
// worktrees that had since been removed. Closing it needs an identity the deleted
// path can still be resolved through, which nothing records today.
func repoScope(dir string) func(string) bool {
	seen := map[string]bool{}
	return func(cwd string) bool {
		if match, ok := seen[cwd]; ok {
			return match
		}
		other, err := sessions.Dir(cwd)
		match := err == nil && other == dir
		seen[cwd] = match
		return match
	}
}

// checkoutRoots resolves a cwd to the checkout that contains it: the nearest
// ancestor holding a .git entry, which is a file in a worktree and a directory
// in the main checkout. Memoized for the same reason repoScope is. Empty when
// nothing above the cwd is a checkout.
func checkoutRoots() func(string) string {
	seen := map[string]string{}
	return func(cwd string) string {
		if root, ok := seen[cwd]; ok {
			return root
		}
		root := repoid.CheckoutRoot(cwd)
		seen[cwd] = root
		return root
	}
}

func renderLoadSummary(w io.Writer, s sessionLoadSummary) {
	fmt.Fprintf(w, "loaded %d, deduped %d, dropped %d (another repository), rejected %d\n",
		s.Loaded, s.Deduped, s.Dropped, s.Rejected)
	for _, kind := range sessions.EventKinds {
		if n := s.ByKind[kind]; n > 0 {
			fmt.Fprintf(w, "  %-14s %d\n", kind, n)
		}
	}
	fmt.Fprintf(w, "store: %s\n", s.Store)
	if s.Loaded > 0 {
		fmt.Fprintf(w, "read one back with `%s`\n", hint.SessionShow.With("<session>"))
	}
}

// countedName is one repeated thing and how often it appeared.
type countedName struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// commandGroup is one program a session ran, with what today's rules say about
// the commands it ran under that name.
//
// Deny counts what the rules WOULD refuse now; Denied counts what the host
// recorded as actually refused. The gap between them is the audit: a command the
// rules deny that ran anyway was never judged, because the guard was not wired,
// was too old to judge, or the rule arrived after the command did.
type commandGroup struct {
	Program string   `json:"program"`
	Count   int      `json:"count"`
	Pass    int      `json:"pass"`
	Advise  int      `json:"advise"`
	Deny    int      `json:"deny"`
	Denied  int      `json:"denied"`
	Rules   []string `json:"rules,omitempty"`
}

type sessionShowOutput struct {
	Session     string         `json:"session"`
	Host        string         `json:"host,omitempty"`
	Events      int            `json:"events"`
	FirstMs     int64          `json:"first_ms,omitempty"`
	LastMs      int64          `json:"last_ms,omitempty"`
	ByKind      map[string]int `json:"by_kind,omitempty"`
	Commands    []commandGroup `json:"commands,omitempty"`
	Skills      []countedName  `json:"skills,omitempty"`
	Read        []countedName  `json:"files_read,omitempty"`
	Written     []countedName  `json:"files_written,omitempty"`
	HookOutputs int            `json:"hook_outputs"`
	Transcript  string         `json:"transcript,omitempty"`
}

// sessionShow implements `magus session show <id>`.
func sessionShow(root string, args []string) error {
	rest, err := cmdParse("session show", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus session show <session-id>")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Report one loaded session: what it ran grouped by program, what today's")
			fmt.Fprintln(os.Stderr, "guard rules say about each command, which skills it loaded, and which")
			fmt.Fprintln(os.Stderr, "files it read and wrote.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The id is a host session id, listed by `magus session ls`. Sessions")
			fmt.Fprintln(os.Stderr, "arrive here through `magus session load`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usagef("magus session show: needs exactly one session id (got %d); run `"+hint.Session.String()+"` to list them", len(rest))
	}
	root = resolveRootOrEmpty(root)
	if root == "" {
		return fmt.Errorf("magus session show: no workspace here: the session store is keyed by repository, so run from inside one or pass --root <path>")
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return err
	}
	events := sessions.AgentEvents(fold, rest[0])
	if len(events) == 0 {
		return fmt.Errorf("magus session show: no loaded events for session %q in %s; load a host transcript with `%s`",
			rest[0], dir, hint.SessionLoad.String())
	}

	out := summarizeSession(rest[0], events)
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitFormatted(opts, out)
	}
	renderSessionShow(os.Stdout, out)
	return nil
}

func summarizeSession(session string, events []sessions.AgentEvent) sessionShowOutput {
	out := sessionShowOutput{Session: session, Events: len(events), ByKind: map[string]int{}}
	byProgram := map[string]*commandGroup{}
	skills, read, written := map[string]int{}, map[string]int{}, map[string]int{}

	for _, ev := range events {
		out.ByKind[ev.Event]++
		if out.Host == "" {
			out.Host = ev.Host
		}
		if out.Transcript == "" {
			out.Transcript = ev.Transcript
		}
		if ev.At > 0 {
			if out.FirstMs == 0 || ev.At < out.FirstMs {
				out.FirstMs = ev.At
			}
			if ev.At > out.LastMs {
				out.LastMs = ev.At
			}
		}
		switch ev.Event {
		case sessions.EventShellCommand:
			g := byProgram[ev.Program]
			if g == nil {
				g = &commandGroup{Program: ev.Program}
				byProgram[ev.Program] = g
			}
			g.Count++
			switch ev.Verdict {
			case "deny":
				g.Deny++
			case "advise":
				g.Advise++
			default:
				g.Pass++
			}
			if ev.Denied {
				g.Denied++
			}
			if ev.Rule != "" && !slices.Contains(g.Rules, ev.Rule) {
				g.Rules = append(g.Rules, ev.Rule)
			}
		case sessions.EventSkillLoad:
			skills[ev.Text]++
		case sessions.EventFileRead:
			read[ev.Text]++
		case sessions.EventFileWrite:
			written[ev.Text]++
		case sessions.EventHookOutput:
			out.HookOutputs++
		}
	}

	for _, g := range byProgram {
		slices.Sort(g.Rules)
		out.Commands = append(out.Commands, *g)
	}
	slices.SortFunc(out.Commands, func(a, b commandGroup) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return strings.Compare(a.Program, b.Program)
	})
	out.Skills, out.Read, out.Written = counted(skills), counted(read), counted(written)
	return out
}

// counted renders a tally most-frequent first, ties broken by name so two runs
// over one session render identically.
func counted(tally map[string]int) []countedName {
	out := make([]countedName, 0, len(tally))
	for name, n := range tally {
		out = append(out, countedName{Name: name, Count: n})
	}
	slices.SortFunc(out, func(a, b countedName) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// showListCap bounds the file and skill lists in the text view. The tail is a
// session's whole reach, which is a different question with its own surface.
const showListCap = 10

func renderSessionShow(w io.Writer, s sessionShowOutput) {
	fmt.Fprintf(w, "%s  host %s  %d event(s)\n", s.Session, orDash(s.Host), s.Events)
	if s.FirstMs > 0 {
		fmt.Fprintf(w, "%s to %s\n",
			time.UnixMilli(s.FirstMs).Format("2006-01-02 15:04:05"),
			time.UnixMilli(s.LastMs).Format("2006-01-02 15:04:05"))
	}
	if s.Transcript != "" {
		fmt.Fprintf(w, "transcript %s\n", s.Transcript)
	}
	fmt.Fprintln(w)
	for _, kind := range sessions.EventKinds {
		if n := s.ByKind[kind]; n > 0 {
			fmt.Fprintf(w, "  %-14s %d\n", kind, n)
		}
	}
	if len(s.Commands) > 0 {
		fmt.Fprintln(w, "\nCommands by program, judged against today's rules:")
		for _, g := range s.Commands {
			fmt.Fprintf(w, "  %-12s %4d  pass %d  advise %d  deny %d  (host recorded %d denied)",
				orDash(g.Program), g.Count, g.Pass, g.Advise, g.Deny, g.Denied)
			if len(g.Rules) > 0 {
				fmt.Fprintf(w, "  %s", strings.Join(g.Rules, ", "))
			}
			fmt.Fprintln(w)
		}
	}
	renderCounted(w, "Skills loaded", s.Skills)
	renderCounted(w, "Files read", s.Read)
	renderCounted(w, "Files written", s.Written)
	fmt.Fprintf(w, "\nhook outputs: %d\n", s.HookOutputs)
}

func renderCounted(w io.Writer, title string, items []countedName) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s:\n", title)
	for _, item := range items[:min(len(items), showListCap)] {
		fmt.Fprintf(w, "  %4d  %s\n", item.Count, item.Name)
	}
	if extra := len(items) - showListCap; extra > 0 {
		fmt.Fprintf(w, "  and %d more; `%s` lists them all\n", extra, hint.SessionShow.With("<session>", "-o", "json"))
	}
}
