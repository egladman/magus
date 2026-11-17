package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/sessions/brief"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/types"
)

// sessionsDefaultLimit bounds the default listing to a session or two of scrollback.
// The store is grow-only, so an unbounded default would get slower forever.
const sessionsDefaultLimit = 20

// sessionCommand is the whole session family behind one noun. The grouping is the point:
// every subverb reads or writes the one store keyed by repository identity, none needs
// the magusfile to load, and the machine ingest (hook, notify) lives beside the human
// reads instead of burning top-level names no person types.
//
// The listing is NOT the console's Activity app: Activity reads internal/trail
// (actions against the server), this reads internal/sessions (facts a session
// produced). The stores are meant to converge; until they do, each is named after
// what it holds.
func sessionCommand(ctx context.Context, root string, args []string) error {
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
		return invocationList(ctx, root, rest)
	case "load":
		return sessionLoad(ctx, root, rest)
	case "show":
		return sessionShow(ctx, root, rest)
	case "hints":
		return sessionHints(ctx, root, rest)
	case "attention":
		return attentionList(ctx, root, rest)
	case "dispose":
		return attentionDispose(ctx, root, rest)
	case "advise":
		return adviseCommand(root, rest)
	case "advisories":
		return advisoriesCommand(root, rest)
	case "retract":
		return retractCommand(root, rest)
	case "checkpoint":
		return checkpointCommand(ctx, root, os.Stdin, os.Stdout, rest)
	case "notify":
		return notifyCommand(ctx, root, os.Stdin, os.Stdout, rest)
	case "hook":
		// Removed, not renamed-in-place: the verdict was never session-scoped, and the
		// one thing worse than moving it is leaving a second door onto the same rules.
		// Named here rather than falling through to the generic unknown-subcommand
		// message because the caller is usually installed host wiring, not a person,
		// and "unknown subcommand" does not tell whoever reads that log what to re-run.
		return exit.Usagef("magus session hook is now `%s`, which takes the same flags and stdin. Re-run `%s` to rewrite host wiring that still calls the old path", hint.Shell, hint.AgentHarnessInstall)
	default:
		return exit.Usagef("magus session: unknown subcommand %q (want ls, show, hints, load, checkpoint, attention, dispose, advise, advisories, retract, or notify); the bare command lists recent sessions, bounded by --limit and --since. Taking a job is `%s`", verb, hint.JobExec)
	}
}

func sessionUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus session [ls] [--all] [--limit <n>] [--since <when>]")
	fmt.Fprintln(os.Stderr, "       magus session --brief             # this checkout's state, for a session that lost its history")
	fmt.Fprintln(os.Stderr, "       magus session show <session-id>")
	fmt.Fprintln(os.Stderr, "       magus session hints               # uptake per hint id, over the loaded sessions")
	fmt.Fprintln(os.Stderr, "       magus session load [--file <path>]")
	fmt.Fprintln(os.Stderr, "       magus session attention [flags]")
	fmt.Fprintln(os.Stderr, "       magus session dispose <id> [-reason <text>]")
	fmt.Fprintln(os.Stderr, "       magus session advise <text> [--expect <command prefix>]")
	fmt.Fprintln(os.Stderr, "       magus session advisories [--all]")
	fmt.Fprintln(os.Stderr, "       magus session retract <id>")
	fmt.Fprintln(os.Stderr, "       magus session checkpoint [--note <text>]")
	fmt.Fprintln(os.Stderr, "       magus session notify [flags]    # machine: event ingest, wired by agent hosts")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "One store, two sides. Humans read it: `session` lists what recent magus")
	fmt.Fprintln(os.Stderr, "invocations did in this checkout (--all: every checkout of this repository),")
	fmt.Fprintln(os.Stderr, "each with the host session it ran in when a host delivered one; `session show` reports one host")
	fmt.Fprintln(os.Stderr, "session in full, `session attention` lists the blocks agents raised, and")
	fmt.Fprintln(os.Stderr, "`session dispose` closes one - nothing closes a request automatically.")
	fmt.Fprintln(os.Stderr, "Humans write it too: `session checkpoint` records where work stands before")
	fmt.Fprintln(os.Stderr, "you put it down. Agent hosts write it from hooks, through `session notify`,")
	fmt.Fprintln(os.Stderr, "and `session load` takes a normalized event stream extracted from a host's")
	fmt.Fprintln(os.Stderr, "own transcript.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "`session --brief` answers the same question the listing opens with, for a")
	fmt.Fprintln(os.Stderr, "session rather than a person: where this checkout stands, read off disk, for")
	fmt.Fprintln(os.Stderr, "a model whose history a host replaced with a summary.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Run `magus session <subcommand> -h` for each subverb's flags.")
}

// invocationList shows what recent magus invocations did in this checkout, or in every
// checkout of the repository with --all.
func invocationList(ctx context.Context, root string, args []string) error {
	var limit int
	var since string
	var brief, all bool
	rest, err := commandParse("session", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&all, "all", false, "List invocations from every checkout of this repository, not only this one")
		fs.IntVar(&limit, "limit", sessionsDefaultLimit, "Show at most this many invocations (0 for all)")
		fs.StringVar(&since, "since", "", "Show only invocations active since this point: a duration back from now (2h, 45m, 168h) or an RFC3339 timestamp")
		fs.BoolVar(&brief, "brief", false, "Print this checkout's state for a session that lost its history: revision, unpushed commits, classified dirty tree, capped live leases, the last run's failures, guard wiring (--limit and --since do not apply)")
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return exit.Usagef("magus session: takes no arguments (got %q); use --limit to bound the listing and --since to bound its age", rest[0])
	}
	if brief {
		return sessionBriefCommand(ctx, root)
	}
	if limit < 0 {
		return exit.Usagef("magus session: --limit must be zero or more (got %d); 0 lists every invocation", limit)
	}
	cutoff, err := sessions.ParseSince(since)
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
	fold, err := session.ReadAll(ctx, dir)
	if err != nil {
		return err
	}
	summaries := session.Summarize(fold)
	if !all {
		summaries = sessions.InvocationsInCheckout(summaries, root)
	}
	summaries = sessions.InvocationsSince(summaries, cutoff)
	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		return sessions.RenderInvocations(ctx, root, os.Stdout, summaries, fold, dir, !cutoff.IsZero(), promptCacheForCheckout(root, time.Now()))
	case outputName:
		names := make([]string, 0, len(summaries))
		for _, s := range summaries {
			names = append(names, s.Invocation)
		}
		return emitNames(os.Stdout, names)
	}
	out := map[string]any{
		"invocations": summaries,
		"checkpoints": sessions.LatestCheckpoints(fold),
		"skipped":     fold.Skipped,
		"legacy":      fold.Legacy,
		"store":       dir,
	}
	if clock := promptCacheForCheckout(root, time.Now()); len(clock.Providers) > 0 {
		// The prose the text view prints around these numbers stays out of the
		// structured shape: a caller parsing this wants the instants, and phrasing
		// them is the renderer's job.
		out["prompt_cache"] = clock
	}
	return emitFormatted(opts, out)
}

// localOrigin is this process's OS account, entering through e, for a verb with no ctx to
// read the entry point from.
func localOrigin(e jobtypes.EntryPoint) jobtypes.Origin {
	return trail.StampOrigin(context.Background(), jobtypes.Origin{EntryPoint: e})
}

func sessionLoadUsage(fs *flag.FlagSet) func() {
	return func() {
		fmt.Fprintln(os.Stderr, "Usage: magus session load [--file <path>]   # the event stream arrives on stdin")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Load a normalized agent-session event stream into this repository's session")
		fmt.Fprintln(os.Stderr, "store: one JSON object per line, host vocabulary already mapped onto magus's.")
		fmt.Fprintf(os.Stderr, "Every line needs host, session, kind and ref; kind is one of %s.\n", strings.Join(session.EventKinds(), ", "))
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Events are keyed on (host, session, kind, ref), so re-running an adapter over the same")
		fmt.Fprintln(os.Stderr, "transcript loads nothing twice. Events whose cwd belongs to another repository")
		fmt.Fprintln(os.Stderr, "are dropped; worktrees of this one are kept.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "A shell command's text is never stored. It is re-judged against today's guard")
		fmt.Fprintln(os.Stderr, "rules and kept as its program, the verdict, the rule behind it, and a digest.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
		printOwnDefaults(fs)
	}
}

// sessionLoad implements `magus session load`.
func sessionLoad(ctx context.Context, root string, args []string) error {
	var file string
	rest, err := commandParse("session load", args, func(fs *flag.FlagSet) {
		fs.StringVar(&file, "file", "", "Read the event stream from this file instead of stdin")
		fs.Usage = sessionLoadUsage(fs)
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return exit.Usagef("magus session load: takes no arguments (got %q); the stream arrives on stdin, or name a file with --file", rest[0])
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

	summary, err := sessions.ReadLoadStream(in, dir, guard.NewRejudge(ctx, root, loadWorkspaceShellRules))
	if err != nil {
		return err
	}
	cacheDir, _ := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	guard.JoinServedNext(summary.Events, summary.Commands, hint.ReadServedNext(cacheDir))
	result, err := session.LoadEvents(ctx, dir, summary.Events, session.InvocationStart{
		Origin:    localOrigin(jobtypes.EntryPointCLI),
		Workspace: root,
		Command:   "session load",
		Version:   version,
	}, session.WithTie(sessions.AttentionTie()))
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
		sessions.RenderLoadSummary(os.Stdout, summary)
	} else if err := emitRecord(opts, summary, []string{summary.Store}); err != nil {
		return err
	}
	if len(summary.Rejects) > 0 {
		return fmt.Errorf("magus session load: %d line(s) rejected and not loaded: %s",
			len(summary.Rejects), strings.Join(summary.Rejects[:min(len(summary.Rejects), sessions.LoadRejectsShown)], ", "))
	}
	return nil
}

// sessionShow implements `magus session show <id>`.
func sessionShow(ctx context.Context, root string, args []string) error {
	rest, err := commandParse("session show", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus session show <session-id>")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Report one loaded session: what it ran grouped by program, what today's")
			fmt.Fprintln(os.Stderr, "guard rules say about each command, which skills it loaded, and which")
			fmt.Fprintln(os.Stderr, "files it read and wrote, and what its context and spawns cost. A cost")
			fmt.Fprintln(os.Stderr, "section the guard trail could not measure says so.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The id is a host session id, listed by `magus session ls`. Sessions")
			fmt.Fprintln(os.Stderr, "arrive here through `magus session load`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return exit.Usagef("magus session show: needs exactly one session id (got %d); run `%s` to list them", len(rest), hint.Session.String())
	}
	root = resolveRootOrEmpty(root)
	if root == "" {
		return fmt.Errorf("magus session show: no workspace here: the session store is keyed by repository, so run from inside one or pass --root <path>")
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}
	fold, err := session.ReadAll(ctx, dir)
	if err != nil {
		return err
	}
	events := session.AgentEvents(fold, rest[0])
	if len(events) == 0 {
		return fmt.Errorf("magus session show: no loaded events for session %q in %s, load a host transcript with `%s`",
			rest[0], dir, hint.SessionLoad.String())
	}

	out := sessionShowOutput{ShowSummary: sessions.SummarizeSession(rest[0], events)}
	tr, err := sessionTrailHere(ctx, root, rest[0])
	if err != nil {
		return err
	}
	out.Trail = tr
	out.Cost = trail.SummarizeCost(out.Trail, sessionModelTable(root))
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitRecord(opts, out, []string{rest[0]})
	}
	renderSessionShow(os.Stdout, out)
	return nil
}

// sessionModelTable is the model rows in effect for the workspace at root: those its
// harnesses publish, then agents.models over them. A workspace that will not load has no
// harness rows, and the cost section then reports no facts for a model rather than failing
// the report. The workspace is opened apart from the process's shared handle, which a
// session read never needs.
func sessionModelTable(root string) model.Table {
	rows := guard.HarnessModelRows(context.Background(), root, func(ctx context.Context) (types.WorkspaceRepository, error) {
		return magus.Inspect(ctx, root, append(cliOptions(), workspace.WithLoadedConfig(globalCfg), magus.WithVersion(version))...)
	})
	return model.InEffect(rows(), globalCfg.Agents.ModelOverrides())
}

// directiveStore opens the advisories store of the repository root names.
func directiveStore(root string) (sessions.DirectiveStore, error) {
	root, err := attentionRoot(root)
	if err != nil {
		return sessions.DirectiveStore{}, err
	}
	return sessions.OpenDirectiveStore(root)
}

func adviseCommand(root string, args []string) error {
	var flags *flaggen.SessionAdviseFlags
	rest, err := commandParse("session advise", args, func(fs *flag.FlagSet) {
		flags = flaggen.BindSessionAdvise(fs)
	})
	if err != nil {
		return err
	}
	expect := flags.Expect
	text := strings.TrimSpace(strings.Join(rest, " "))
	if text == "" {
		return exit.Usagef(`magus session advise: needs the text to show every session, as in magus session advise "push what you have"`)
	}
	if err := requirePerson(context.Background(), "session advise records a person's words"); err != nil {
		return err
	}
	store, err := directiveStore(root)
	if err != nil {
		return err
	}
	a, err := store.Post(context.Background(), text, expect, localOrigin(jobtypes.EntryPointCLI).User, time.Now())
	if err != nil {
		return err
	}
	return emitDirective(a, "posted")
}

func retractCommand(root string, args []string) error {
	rest, err := commandParse("session retract", args, func(*flag.FlagSet) {})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return exit.Usagef("magus session retract: needs exactly one advisory id (got %d); `magus session advisories` lists them", len(rest))
	}
	if err := requirePerson(context.Background(), "session retract records that a person withdrew their words"); err != nil {
		return err
	}
	store, err := directiveStore(root)
	if err != nil {
		return err
	}
	a, err := store.Retract(context.Background(), rest[0], localOrigin(jobtypes.EntryPointCLI).User, time.Now())
	if errors.Is(err, sessions.ErrNoDirective) {
		return fmt.Errorf("magus session retract: run `magus session advisories --all` to list every id: %w", err)
	}
	if err != nil {
		return fmt.Errorf("magus session retract: %w", err)
	}
	return emitDirective(a, "retracted")
}

func emitDirective(a sessions.Directive, verb string) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitRecord(opts, a, []string{a.ID})
	}
	fmt.Fprintf(os.Stdout, "%s %s\n  %s\n", verb, a.ID, sessions.OneLine(a.Text))
	return nil
}

func advisoriesCommand(root string, args []string) error {
	var flags *flaggen.SessionAdvisoriesFlags
	rest, err := commandParse("session advisories", args, func(fs *flag.FlagSet) {
		flags = flaggen.BindSessionAdvisories(fs)
	})
	if err != nil {
		return err
	}
	all := flags.All
	if len(rest) > 0 {
		return exit.Usagef("magus session advisories: takes no arguments (got %q)", rest[0])
	}
	store, err := directiveStore(root)
	if err != nil {
		return err
	}
	dir := store.Dir()
	loaded, err := store.Load()
	if err != nil {
		return err
	}
	shown := make([]sessions.Directive, 0, len(loaded))
	for _, a := range loaded {
		if all || a.Open() {
			shown = append(shown, a)
		}
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		sessions.RenderAdvisoriesText(os.Stdout, shown, dir)
		return nil
	case outputName:
		names := make([]string, 0, len(shown))
		for _, a := range shown {
			names = append(names, a.ID)
		}
		return emitNames(os.Stdout, names)
	}
	return emitFormatted(opts, sessions.AdvisoriesOutput{Advisories: shown, Store: dir})
}

// sessionShowOutput is the JSON shape of `session show`: the loaded events of the session
// beside what this checkout's trail and the cost model add to them.
type sessionShowOutput struct {
	sessions.ShowSummary
	// Trail is what THIS checkout's guard trail recorded for the session: the join
	// between the host's transcript and the observations magus made while it ran. Absent
	// when the trail here holds nothing for it, which is the case for a session that ran
	// in another worktree.
	Trail *trail.SessionTrail `json:"trail,omitempty"`
	// Cost is what the session's context and spawns cost. The sections the trail could not
	// measure are named in its not_measured map rather than left out or zeroed.
	Cost *trail.SessionCost `json:"cost,omitempty"`
}

func renderSessionShow(w io.Writer, s sessionShowOutput) {
	sessions.RenderShow(w, s.ShowSummary)
	if s.Trail != nil {
		trail.RenderSessionTrail(w, s.Trail)
	}
	if s.Cost != nil {
		trail.RenderCost(w, s.Cost)
	}
}

// checkoutCacheDir is the cache dir of the checkout at root, or "" when it cannot be resolved.
func checkoutCacheDir(root string) string {
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	if err != nil {
		return ""
	}
	return cacheDir
}

func sessionTrailHere(ctx context.Context, root, session string) (*trail.SessionTrail, error) {
	cacheDir := checkoutCacheDir(root)
	if cacheDir == "" {
		return nil, nil
	}
	return trail.SessionTrailHere(ctx, cacheDir, session)
}

func promptCacheForCheckout(root string, now time.Time) sessions.PromptCacheClock {
	cacheDir := checkoutCacheDir(root)
	if cacheDir == "" {
		return sessions.PromptCacheClock{}
	}
	return trail.PromptCacheForCheckout(cacheDir, now)
}

// The helpers below keep the names that cmd files outside this one still call.

func orDash(s string) string { return sessions.OrDash(s) }

func sessionHintsUsage(fs *flag.FlagSet) func() {
	return func() {
		fmt.Fprintln(os.Stderr, "Usage: magus session hints")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Per hint id: how many times it was served, how often the command was run")
		fmt.Fprintf(os.Stderr, "within the next %d calls, how often another magus verb was run instead, and\n", sessions.NextLookahead)
		fmt.Fprintln(os.Stderr, "how often the same command was repeated.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "It reads the sessions this repository has loaded, so a store with no")
		fmt.Fprintln(os.Stderr, "transcripts in it reports nothing. Load one with `magus session load`.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
		printOwnDefaults(fs)
	}
}

// sessionHints implements `magus session hints`.
func sessionHints(ctx context.Context, root string, args []string) error {
	rest, err := commandParse("session hints", args, func(fs *flag.FlagSet) {
		fs.Usage = sessionHintsUsage(fs)
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return exit.Usagef("magus session hints: takes no arguments (got %q)", rest[0])
	}

	root = resolveRootOrEmpty(root)
	if root == "" {
		return errors.New("magus session hints: no workspace here: the session store is keyed by repository, so run from inside one or pass --root <path>")
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}
	fold, err := session.ReadAll(ctx, dir)
	if err != nil {
		return err
	}

	report := sessions.HintUptakeReport{
		Definition: sessions.HintUptakeDefinition,
		Floor:      sessions.HintFloor,
		Lookahead:  sessions.NextLookahead,
		Hints:      sessions.HintUptake(fold),
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputText {
		return sessions.RenderHintUptake(os.Stdout, report)
	}
	names := make([]string, len(report.Hints))
	for i, h := range report.Hints {
		names[i] = h.ID
	}
	return emitRecord(opts, report, names)
}

// briefMemoryMB is the host-capacity claim session --brief takes while it Inspects
// the workspace. Measured 2026-10-03 on this repository: a nested brief peaked near
// 280MB RSS (~64MB heap inuse). Concurrent Inspect samples in panic reports ran
// higher; the declaration seats one brief without inviting a stack of them onto a
// laptop that already holds a go-build and a VM.
const briefMemoryMB = 512

// sessionBriefCommand implements `magus session --brief`.
func sessionBriefCommand(ctx context.Context, root string) error {
	root = resolveRootOrEmpty(root)
	if root == "" {
		return fmt.Errorf("magus session --brief: no workspace here: the brief is read out of a checkout, so run from inside one or pass --root <path>")
	}
	// Claim the host budget before Inspect: the load is what panics measured stacking
	// across parallel hooks. A full budget refuses (exit 75) so a sessionStart hook
	// keeps env-only rather than outspending the machine next to a go-build.
	release, err := admitSessionBrief(ctx, processBrokerClient(), root)
	if err != nil {
		return err
	}
	defer release()
	// The magusfile is optional here for the reason `session checkpoint` makes it
	// optional: the states where it will not load are exactly the ones where a
	// reader most needs to be told where the work stands.
	var ws types.WorkspaceRepository
	if loaded, err := inspectWorkspace(ctx, root); err == nil {
		ws, root = loaded, loaded.Root()
	}
	payload := brief.Gather(ctx, root, ws, briefReaders())

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		fmt.Print(payload.Text())
		return nil
	case outputName:
		// The revision, matching `magus session checkpoint -o name`: the one value
		// that identifies the state this brief describes. A tree with no revision to
		// report prints nothing rather than a blank line, which a caller reading
		// names line by line would take for an entry.
		if payload.Revision == "" {
			return nil
		}
		return emitNames(os.Stdout, []string{payload.Revision})
	}
	return emitFormatted(opts, payload)
}

// admitSessionBrief takes the host-capacity claim for the workspace Inspect that
// follows. broker: off skips; best-effort admits unarbitrated when no broker answers;
// required refuses (MGS3022).
func admitSessionBrief(ctx context.Context, admit cache.MachineAdmitter, root string) (func(), error) {
	if globalCfg.Broker.Resolved() == types.BrokerOff {
		return func() {}, nil
	}
	required := globalCfg.Broker.Resolved() == types.BrokerRequired
	return cache.AdmitMachine(ctx, admit, budget.Claim{
		Project:  ".",
		Target:   "session-brief",
		MemoryMB: briefMemoryMB,
		Dir:      root,
	}, required, globalCfg.BudgetWait)
}

// briefReaders is how the brief reads the job store and the process's configuration.
func briefReaders() brief.Readers {
	return brief.Readers{
		OpenJobs: func(ctx context.Context, root string) (*job.Store, error) { return openJobs(ctx, root, jobOpen{}) },
		ActingLease: func(ctx context.Context, root string) string {
			lease, _, _ := guard.CheckoutLease(ctx, root, jobPlace)
			return lease
		},
		PromptCache: promptCacheForCheckout,
		CacheDir: func(root string) (string, error) {
			return magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
		},
		ResolveRoot: resolveRootOrEmpty,
		ConsoleURL:  consoleRootURL,
		InCheckout:  sessions.InvocationsInCheckout,
	}
}

// readJobRows lists the repository's job store once for both the leases and the tree.
func readJobRows(ctx context.Context, root string) ([]jobtypes.Job, error) {
	store, err := openJobs(ctx, root, jobOpen{})
	if err != nil {
		return nil, err
	}
	return store.List()
}
