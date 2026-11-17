package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/difftui"
	"github.com/egladman/magus/internal/job/evidence"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/review"
	reviewimpact "github.com/egladman/magus/internal/review/impact"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types"
	difftypes "github.com/egladman/magus/types/diff"
)

// cliTokens is the shell's token store as the review routes use it.
type cliTokens struct{}

func (cliTokens) Mint(req auth.MintRequest) (string, auth.Token, error) { return mintToken(req, false) }

func (cliTokens) Revoke(id string) error {
	store, err := openTokenStore()
	if err != nil {
		return err
	}
	_, err = store.Revoke(types.GrantOperator, id)
	return err
}

// diffCommand implements `magus diff`: the working tree's changes, annotated and ordered by
// what they can break.
//
// It is the TERMINAL client of the same annotation join the console's Diff app reads
// and an agent joins over MCP. One computation, three transports, so a person reviewing in a
// terminal and an agent pairing with them are looking at the same ranking rather than two
// tools' opinions of one changeset.
func diffCommand(ctx context.Context, root string, args []string) error {
	var rf *flaggen.DiffFlags
	var gates difftypes.Options
	rest, err := commandParse("diff", args, func(fs *flag.FlagSet) {
		rf = flaggen.BindDiff(fs)
		fs.Float64Var(&gates.MinShare, flaggen.FlagDiffConformanceMinShare, 0,
			"The share of a conformance norm's declarations, above 0 and at most 1, that must agree before it is reported; defaults to knowledge.conformance.gates.min_share")
		fs.Usage = func() { diffUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}
	// compat(until: the advice action reads knowledge.conformance.gates): the action passes its own
	// 5 and 0.8 on every run, so a flag overrides the configured gate even when nobody chose it.
	gates.MinCohort = rf.ConformanceMinCohort
	if gates.MinCohort < 0 || gates.MinShare < 0 || gates.MinShare > 1 {
		return exit.Usagef("magus diff: --conformance-min-cohort must be at least 1 and --conformance-min-share above 0 and at most 1")
	}
	rf.Thread = strings.TrimSpace(rf.Thread)
	if rf.Thread != "" && (rf.Ack || rf.Unread || rf.Impact) {
		return exit.Usagef("magus diff: --thread narrows the review to one thread, so it cannot be combined with --ack, --unread or --impact, which answer for the whole changeset")
	}
	// EVERY positional is a path that narrows the changeset, whichever source it came from.
	// The source itself is always a flag (--rev, --patch, or the working tree by default),
	// and one rule for what a word means beats two.
	//
	// This is also what retires --ack's overload. It used to claim the positionals for itself
	// because reading happens in whatever the reader already uses (vim, magit, a pager, an
	// IDE), and magus has no business requiring its own viewer to record that it happened. It
	// no longer needs to claim anything: the paths mean the same thing to the ack and to the
	// view, which is why the ack can now narrow a range review too.
	scopePaths, err := review.ScopeFromArgs(rest)
	if err != nil {
		return err
	}
	src, err := review.SourceFromFlags(rf.Patch, rf.Rev)
	if err != nil {
		return err
	}
	if len(scopePaths) > 0 && !src.Addressable() {
		// Narrowing happens at the SOURCE, through the backend's own pathspec, so everything
		// downstream is already scoped. A patch somebody handed over has no source to ask, and
		// re-emitting a filtered patch is a different feature from reading one. Refused rather
		// than ignored: silently reading the whole patch after being asked for part of it is
		// how a reader concludes they have seen a file they have not.
		return exit.Usagef("magus diff: %s is already the changeset somebody chose, so it cannot be narrowed to %s. "+
			"Narrow it before handing it over, or read the working tree or a range instead",
			src.Label, strings.Join(scopePaths, ", "))
	}
	if rf.Watch && src.Kind != review.SourceWorkingTree {
		// stdin is consumed once and a patch file is a snapshot someone handed us; re-reading
		// either on every tree change would re-render identical output forever.
		return exit.Usagef("magus diff: --watch reads the working tree, so it cannot be combined with %s", src.Label)
	}
	if rf.Ack && !src.Addressable() {
		// A receipt fingerprints content magus can name, and a patch describes files that may
		// not be here at all. Accepting one would record receipts against whatever the working
		// tree happens to hold: an acknowledgement of something nobody read.
		//
		// A revision range IS nameable, so it is admitted: its receipts fingerprint the file at
		// that revision, never the reader's own checkout.
		return exit.Usagef("magus diff: --ack fingerprints content magus can address, so it cannot be combined with %s", src.Label)
	}
	if rf.Ack && rf.Watch {
		return exit.Usagef("magus diff: --ack records once and returns, so it cannot be combined with a live view")
	}
	// --reason is optional, deliberately. Requiring a sentence the way spells.allow_shadow does
	// would not hold here: an allow_shadow entry is written a handful of times in a repository's
	// life, while a changeset ack is written daily and becomes a form field, teaching the
	// reader to type something they do not mean.
	//
	// The two controls that hold are the ones nobody can satisfy by typing faster: the agent
	// guard, and the terminal check below.
	if rf.Ack {
		if err := requirePerson(ctx, "diff --ack records that a person read this"); err != nil {
			return err
		}
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	term := review.TUITerm{
		Reads:  interactive.IsInteractiveTTY(),
		Paints: termkit.IsTerminalWriter(os.Stdout, termkit.SystemProbe),
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	tui := review.WantsTUI(review.TUIGate{
		Tui: rf.Tui, Enabled: m.DiffTUIEnabled(), Ack: rf.Ack, Impact: rf.Impact, Watch: rf.Watch,
		Baseline: rf.Baseline, Text: opts.Format == outputText,
	}, src, term)

	if tui && (gates.MinCohort != 0 || gates.MinShare != 0) {
		// The viewer can join a server's review, which ran with the server's gates, so the
		// flags could not be honored there.
		return exit.Usagef("magus diff: --conformance-min-cohort and --conformance-min-share apply to the printed report; add --no-tui")
	}
	render := func() error { return renderDiff(ctx, m, src, opts, rf, gates, root, tui, rf.Impact, scopePaths) }
	if rf.Watch {
		return watchDiff(ctx, m, render)
	}
	return render()
}

// requirePerson refuses a command recording a person's act without an
// interactive terminal. The guard denies it to an agent but fails open where
// unwired. Exit 2: the flags are fine, the caller is not a person.
func requirePerson(ctx context.Context, records string) error {
	if interactive.IsInteractiveTTY() {
		return nil
	}
	slog.ErrorContext(ctx, records+", so it needs an interactive terminal",
		attr.Notice(""), attr.Component("magus"))
	return exit.Reported(exit.Usage)
}

// renderDiff reads one patch and emits it in the requested format.
//
// impact is ADDITIVE: with it off, every byte emitted here is what this command emitted
// before the flag existed, which is what lets a script parsing `magus diff -o json` keep
// working and what keeps the flag honest about being context rather than a gate.
func renderDiff(ctx context.Context, m *magus.Magus, src review.Source, opts output.Options, rf *flaggen.DiffFlags, gates difftypes.Options, rootOverride string, tui, impact bool, scopePaths []string) error {
	patch, base, err := src.ReadPatch(ctx, m, scopePaths)
	if err != nil {
		return err
	}
	if rf.Thread != "" && !tui {
		return printThread(ctx, m, threadPrint{
			opts:  opts,
			id:    rf.Thread,
			patch: patch,
			annotate: func(ctx context.Context, paths []string) (difftypes.Diff, error) {
				// The record never reads the reading order, and ranking the whole changeset is not cheap.
				g := gates
				g.Patch, g.SkipOrder = patch, true
				return annotateDiff(ctx, m, review.ReadContent(ctx, m, src), paths, base, rf.Baseline, g)
			},
			anchors: func(ctx context.Context, rev difftypes.Diff) ([]review.AnchorHit, error) {
				return impactAnchors(ctx, rootOverride, rev)
			},
		})
	}
	if strings.TrimSpace(patch) == "" {
		// An empty input is a STATE, and saying so beats printing an empty table that reads as
		// a failure to find anything. The sentence differs by source: a clean tree is good
		// news, an empty patch file is probably a mistake.
		if opts.Format == outputText {
			if src.Kind == review.SourceWorkingTree {
				fmt.Println("clean: every change is committed")
			} else {
				fmt.Printf("empty: %s carries no changes\n", src.Label)
			}
			return nil
		}
		return emitFormatted(opts, difftypes.Diff{Base: base})
	}

	paths := review.ChangedPathsFromPatch(patch)
	// Bytes went in and no file came out, so this is not an empty changeset; it is a patch
	// magus could not read, and the two must never print the same thing. Reporting "0 files to
	// read" at exit 0 is the worst available answer: a reader checking whether they had
	// anything left to review is told no, and believes it.
	//
	// Scoped to a patch the caller handed us. A working tree's patch and a revision range's both
	// come from whichever VCS adapter is active, and refusing there would turn "a backend spells
	// its headers a third way" into a hard failure of the whole command: a worse bug than the
	// one being fixed.
	if len(paths) == 0 && !src.Addressable() {
		if strings.Contains(patch, "\x1b[") {
			return fmt.Errorf("magus diff: %s is colorized, so its headers carry escape sequences "+
				"and no longer begin a line, which is what a VCS emits when it thinks it is writing "+
				"to a terminal, exactly the case when magus is its pager, so turn color off "+
				"for the diff it hands over: `hg --config color.mode=off`, `jj --config ui.color=never`, "+
				"or `--color=never` on any of them", src.Label)
		}
		return fmt.Errorf("magus diff: %s has content but no file headers magus can read, "+
			"it expects a unified diff (`diff --git a/x b/x`, or a `--- a/x` / `+++ b/x` pair)", src.Label)
	}
	// Resolved once: every receipt this command mints or reports on must agree about which
	// tree it is talking about, and a second resolve is a second chance to disagree.
	content := review.ReadContent(ctx, m, src)
	if tui {
		return runDiffTUI(ctx, m, content, diffTUIRequest{
			patch: patch, base: base, riskBase: src.RiskBase(), paths: paths, label: src.Label,
			showGenerated: rf.Generated, unread: rf.Unread, thread: rf.Thread,
		})
	}
	gates.Patch, gates.RiskBase = patch, src.RiskBase()
	rev, err := annotateDiff(ctx, m, content, paths, base, rf.Baseline, gates)
	if err != nil {
		return err
	}
	if rf.Unread {
		viewed, verr := changeset.NewStore(m.CacheDir()).LoadViewed()
		rev = review.FilterUnread(rev, viewed, verr)
		if rev.Unread.ReadState == difftypes.ReadStateUnknown {
			warnUnreadUnknown(ctx, *rev.Unread, src.Label, verr)
		}
	}
	// The threads are named on the report so a person can find the id --thread takes. A patch
	// somebody handed over belongs to no review this branch has open. Only a running server's
	// session names them: a plain report never reaches the network.
	if src.Addressable() && opts.Format != outputName {
		if comments, _, served := review.ServerReviewComments(ctx, reviewServerGet); served {
			review.AttachThreads(&rev, changeset.ParseHunks(patch), comments)
		} else {
			slog.InfoContext(ctx, "review threads are listed only while the server runs",
				attr.Notice(""), attr.Component("magus"), attr.Next(fmt.Sprint(hint.ServerStart)))
		}
	}
	if rf.Ack {
		reason := strings.TrimSpace(rf.Reason)
		scoped, err := review.ScopeAck(rev, scopePaths)
		if err != nil {
			return err
		}
		n, err := review.AckChangeset(content, m.CacheDir(), scoped, reason, time.Now())
		if err != nil {
			return err
		}
		msg := fmt.Sprintf("recorded %d read receipt(s) at the current content; editing a file voids its receipt", n)
		if reason != "" {
			msg = fmt.Sprintf("recorded %d read receipt(s), noted %q; editing a file voids its receipt", n, reason)
		}
		fmt.Println(msg)
		return nil
	}

	var pre *reviewimpact.Report
	if impact {
		p, err := reviewimpact.Collect(ctx, m, impactSources(m, rootOverride), rev, patch, src.Kind == review.SourceWorkingTree)
		if err != nil {
			return fmt.Errorf("magus diff: %w", err)
		}
		pre = &p
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		if pre != nil {
			return emitFormatted(opts, diffReport{Diff: rev, Impact: pre})
		}
		return emitFormatted(opts, rev)
	case outputName:
		// Left alone under --impact: -o name is the shape a shell loop reads, and one
		// non-path line in it would break every one of them.
		shown := make([]difftypes.File, 0, len(rev.Files))
		for _, f := range rev.Files {
			if f.Generated() && !rf.Generated {
				continue
			}
			shown = append(shown, f)
		}
		// Narrowed to hunks, a name is a hunk: one path:start-end each, so a loop reaches
		// every unread hunk rather than every file that holds one.
		if rev.Unread != nil {
			return emitNames(os.Stdout, review.HunkNames(difftypes.Diff{Files: shown}))
		}
		paths := make([]string, 0, len(shown))
		for _, f := range shown {
			paths = append(paths, f.Path)
		}
		return emitNames(os.Stdout, paths)
	}
	if rev.Unread != nil {
		if rev.Unread.ReadState == difftypes.ReadStateUnknown {
			return nil
		}
		fmt.Println(review.UnreadLine(*rev.Unread, src.Label))
		if rev.Unread.Unread == 0 {
			return nil
		}
		fmt.Println()
	}
	review.HintSinceLastReview(ctx, rev, src)
	var impactLines []string
	if pre != nil {
		impactLines = reviewimpact.Lines(*pre)
	}
	return review.PrintText(os.Stdout, rev, review.TextOptions{
		ShowGenerated: rf.Generated, Link: review.PathLinker(m.Root()), Impact: impactLines,
		Console: func() (string, string) {
			u := consoleDiffURL()
			if u == "" {
				return "", ""
			}
			return u, console.AuthHint(u)
		},
	})
}

// warnUnreadUnknown tells the person the read marks could not be loaded, with the load error as
// the record's error.
func warnUnreadUnknown(ctx context.Context, u difftypes.Unread, source string, loadErr error) {
	slog.WarnContext(ctx, review.UnreadLine(u, source),
		attr.Notice(""), attr.Component("magus"), attr.Why("the read marks could not be read"), attr.Error(loadErr))
}

// threadPrint is what printThread reads one thread from. The patch, the changeset annotation
// and the notes join are the CLI's own; assembling a record from them is review.NewThreadInput's.
type threadPrint struct {
	opts output.Options
	// id names the thread: its first comment, or any reply in it.
	id       string
	patch    string
	annotate func(ctx context.Context, paths []string) (difftypes.Diff, error)
	anchors  func(ctx context.Context, rev difftypes.Diff) ([]review.AnchorHit, error)
}

// printThread prints one review thread and returns: the conversation, the hunk, and what the
// change there reaches.
//
// An empty patch is not a refusal: a review outlives the tree it was written on, and the record
// then carries the thread and the host's own copy of the hunk.
func printThread(ctx context.Context, m *magus.Magus, t threadPrint) error {
	comments, reason := review.Remarks(ctx, reviewServerGet, m.ReviewOrigin(ctx), m.CacheDir())
	in, err := review.NewThreadInput(ctx, review.ThreadParts{
		Patch:    t.patch,
		Comments: comments,
		Annotate: t.annotate,
		Anchors:  t.anchors,
	})
	if err != nil {
		return fmt.Errorf("magus diff: %w", err)
	}
	rec, err := review.ReadThread(in, t.id)
	if err != nil {
		return fmt.Errorf("magus diff: %w", review.ThreadLookupError(err, reason, len(comments)))
	}
	if reason != "" {
		slog.WarnContext(ctx, "part of the review could not be read", attr.Notice(""), attr.Component("magus"), attr.Why(reason))
	}
	switch t.opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(t.opts, rec)
	case outputName:
		if rec.Hunk.Source == "patch" && rec.InChangeset {
			for _, f := range in.Changeset.Files {
				for _, h := range f.Hunks {
					if f.Path == rec.Path && h.Index == rec.Hunk.Index {
						return emitNames(os.Stdout, []string{changeset.NewRange(f.Path, h.NewStart, h.NewCount)})
					}
				}
			}
		}
		return emitNames(os.Stdout, []string{rec.Path})
	}
	for _, line := range review.ThreadLines(rec) {
		fmt.Println(line)
	}
	return nil
}

// annotateDiff computes the annotated changeset for a set of changed paths.
//
// One definition, because the TUI and the one-shot renderer must show the same facts: two
// callers folding on their own overlays is how "the console said 12 files reference this and
// the CLI said nothing" happens.
//
// baselinePath, when set, is a `graph export --symbols -o json` the changed symbols are
// compared against for the API delta. opts carries the changeset's patch and the conformance
// gates.
func annotateDiff(ctx context.Context, m *magus.Magus, content review.Content, paths []string, base, baselinePath string, opts difftypes.Options) (difftypes.Diff, error) {
	if baselinePath != "" {
		var baseline types.KnowledgeGraphOutput
		raw, rerr := os.ReadFile(baselinePath)
		if rerr != nil {
			return difftypes.Diff{}, fmt.Errorf("magus diff: read --baseline: %w", rerr)
		}
		if uerr := json.Unmarshal(raw, &baseline); uerr != nil {
			return difftypes.Diff{}, fmt.Errorf("magus diff: decode --baseline %s (expected `%s` output): %w",
				baselinePath, hint.GraphExport.With("--symbols", "-o", "json"), uerr)
		}
		opts.Baseline, opts.BaselineLabel = &baseline, baselinePath
	}
	rev, err := m.DiffWith(ctx, paths, opts)
	if err != nil {
		return difftypes.Diff{}, err
	}
	rev.Base = base
	attachLayout(ctx, m, &rev, opts)
	// The churn lenses, from a fresh scan. The server serves these from a warm cache; a
	// one-shot CLI has none, so it pays the bounded git-log walk here. Best-effort: a
	// workspace with no history simply reports no churn rather than failing the diff.
	// Files: true is required; without it the lens ranks PROJECTS and the per-file list is
	// empty, so every file would silently report no churn.
	if hot, herr := m.Hotspots(ctx, types.HistoryMetricsOptions{Commits: diffHistoryCommits, Files: true}); herr == nil {
		var projects []types.TrendEntry
		if tr, terr := m.Trend(ctx, types.HistoryMetricsOptions{Commits: diffHistoryCommits}); terr == nil {
			projects = tr.Projects
		}
		rev.AttachChurn(hot.Files, projects)
	}
	// The agent record: which sessions wrote each file and what they had read first, from the
	// guard hook's trail and from loaded transcripts. Empty when neither has anything, which
	// is the common case rather than a fault.
	trail.AttachTouches(&rev, m.Root(), m.CacheDir())
	// Which of these files somebody has recorded reading. Best-effort like every other
	// overlay: an unreadable store leaves every file DiffReadUnknown, which renders as
	// unmeasured rather than as unread.
	if states, serr := review.ReadStates(m.CacheDir(), paths, content.Digest); serr == nil {
		rev.AttachReadState(states)
	}
	// Where the reader left off. Attached HERE rather than computed at the point it is rendered,
	// so the terminal, the console and `-o json` are reading one answer instead of three lookups
	// that can disagree: the same rule that moved the parser and the thread placement inward.
	if store, serr := review.Load(m.CacheDir()); serr == nil {
		rev.AttachReviewed(store.ReviewedAt(paths))
	}
	return rev, nil
}

// attachLayout runs the package checks over the directories the change creates and sets Layout
// on the first changed file of each. It loads the graph a second time, so it first asks the disk
// whether an added file opened a directory at all: most changes open none.
func attachLayout(ctx context.Context, m *magus.Magus, rev *difftypes.Diff, opts difftypes.Options) {
	// An index that could not be brought current would count a stale tree's files.
	if rev.ConformanceError != nil {
		return
	}
	added := map[string]bool{}
	for _, f := range changeset.Parse(opts.Patch) {
		if f.Status == changeset.StatusAdded {
			added[f.Path] = true
		}
	}
	if !review.OpensDir(m.Root(), added) {
		return
	}
	fail := func(err error) {
		rev.Notes = append(rev.Notes, "layout: the package checks could not run: "+err.Error())
	}
	graph, err := viewInternals(m).KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
	if err != nil {
		fail(err)
		return
	}
	var files []string
	for _, n := range graph.Nodes() {
		if n.Kind == types.KindFile || n.Kind == types.KindDoc {
			files = append(files, n.Source)
		}
	}
	entries, err := m.ClassifyFiles(ctx, files)
	if err != nil {
		fail(err)
		return
	}
	generated := map[string]bool{}
	for _, e := range entries {
		if e.Role == difftypes.RoleOutput {
			generated[e.Path] = true
		}
	}
	conformance, err := m.ConformanceOptions()
	if err != nil {
		fail(err)
		return
	}
	// compat: see diffCommand.
	gates := conformance.Gates.Override(opts.MinCohort, opts.MinShare)
	found := graph.Layout(knowledge.LayoutChange{Added: added, Generated: generated, Gates: gates})
	for dir, checks := range found {
		i := slices.IndexFunc(rev.Files, func(f difftypes.File) bool { return path.Dir(f.Path) == dir })
		if i >= 0 {
			rev.Files[i].Layout = checks
		}
	}
}

// watchDiff re-renders whenever the working tree changes, until interrupted.
//
// The point is the loop a person actually runs: edit, glance, edit. Re-running the command by
// hand costs the same keystrokes as reading the diff does, so the annotations get skipped.
//
// Declared target OUTPUTS are ignored, which is not an optimization but a correctness
// requirement: a target rewriting a generated file would fire a re-render, which is the same
// rebuild-loop guard `magus watch` needs, for the same reason.
func watchDiff(ctx context.Context, m *magus.Magus, render func() error) error {
	var outputGlobs []types.Glob
	var projectIgnores []types.IgnorePattern
	for _, p := range m.All() {
		for _, g := range p.AllOutputs() {
			outputGlobs = append(outputGlobs, g.Root(p.Path))
		}
		projectIgnores = append(projectIgnores, p.WatchIgnores...)
	}

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	w, err := watch.New(ctx,
		watch.WithRoot(m.Root()),
		watch.WithIgnore(watch.Compose(
			watch.BuiltinIgnore,
			watch.OutputsIgnore(m.Root(), outputGlobs),
			watch.IgnorePatterns(m.Root(), projectIgnores),
		)),
	)
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()

	if err := render(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			// A clean exit on Ctrl-C, not an error: the reader ending a watch got what they
			// asked for.
			return nil
		case _, ok := <-w.Events():
			if !ok {
				return nil
			}
			// A rule rather than a screen clear: clearing destroys the scrollback a reader may
			// still be reading, and this report is meant to be scrolled.
			fmt.Println()
			fmt.Println(strings.Repeat("-", 60))
			fmt.Println()
			if err := render(); err != nil {
				return err
			}
		}
	}
}

// diffTUIRequest is what one viewer session opens on.
type diffTUIRequest struct {
	patch, base string
	// riskBase is what the files are tiered against; see diffInput.riskBase.
	riskBase string
	paths    []string
	// label names the changeset in a sentence, as diffInput.label does.
	label         string
	showGenerated bool
	// unread narrows the viewer to the hunks no read mark covers.
	unread bool
	// thread, when set, is a review thread id the viewer opens positioned on.
	thread string
}

// runDiffTUI opens the interactive reader over the working tree's changeset.
//
// This is what makes "three clients, one session" true for a terminal. `magus diff` already
// shared the COMPUTATION with the console and the MCP tools; what it did not share was the
// coordination: where the reader is, what they have read, what an agent has asked them to
// look at. Reading a diff is not a report you print once, it is a place you are IN.
func runDiffTUI(ctx context.Context, m *magus.Magus, content review.Content, req diffTUIRequest) error {
	patch := req.patch
	rev, sess, sync, err := attachDiffReview(ctx, m, content, patch, req.base, req.riskBase, req.paths)
	if err != nil {
		return err
	}
	parsed := changeset.ParseHunks(patch)
	if req.unread {
		viewed, verr := changeset.NewStore(m.CacheDir()).LoadViewed()
		rev = review.FilterUnread(rev, viewed, verr)
		if rev.Unread.ReadState == difftypes.ReadStateUnknown {
			warnUnreadUnknown(ctx, *rev.Unread, req.label, verr)
			sync.Close()
			return nil
		}
		if rev.Unread.Unread == 0 {
			fmt.Println(review.UnreadLine(*rev.Unread, req.label))
			sync.Close()
			return nil
		}
		parsed = review.KeepFilteredHunks(rev, parsed)
	}
	files := difftui.OrderedFiles(rev, parsed)
	// Wrapped so finishing a file in the viewer leaves a receipt behind it. The marks were
	// always explicit; this is what makes them outlive the session.
	earned := difftui.NewEarnedSync(sync, content, m.CacheDir(), files, sess.Viewed)
	sync = earned
	// Closed here rather than inside the viewer: how a Sync gets its writes out (inline, or over a
	// goroutine that has to be drained) is this file's business, and the viewer stays ignorant
	// of it. Deferred before Run, so it also runs on the interrupts Run RETURNS from: `q`,
	// Ctrl-C read as a key, a cancelled context. A raw SIGINT unwinds nothing, and the reader
	// loses the queue along with the restored terminal.
	defer sync.Close()
	// Re-placed against THIS patch. A thread arrives placed against the working tree or not
	// placed at all, and neither is what the viewer is showing when the patch came from a file;
	// and a remark drawn against hunk 3 of the wrong patch is worse than one drawn against its
	// file, because the viewer presents it with no hedge.
	comments, reason := review.Remarks(ctx, reviewServerGet, m.ReviewOrigin(ctx), m.CacheDir())
	comments = changeset.PlaceThreads(changeset.ParseHunks(patch), comments)
	start := difftypes.Cursor{Hunk: -1}
	if req.thread != "" {
		thread, terr := changeset.FindThread(comments, req.thread)
		if terr != nil {
			return fmt.Errorf("magus diff: %w", review.ThreadLookupError(terr, reason, len(comments)))
		}
		if !slices.ContainsFunc(files, func(f difftui.File) bool { return f.Path == thread.Head.Path }) {
			return fmt.Errorf("magus diff: thread %s sits on %s, which %s does not change: `%s` prints it",
				thread.ID(), thread.Head.Path, req.label, hint.Diff.With("--thread", thread.ID(), "--no-tui"))
		}
		start = difftypes.Cursor{Path: thread.Head.Path, Hunk: thread.Head.Hunk}
	}
	return difftui.Run(ctx, difftui.Options{
		In:    os.Stdin,
		Out:   os.Stdout,
		Probe: termkit.SystemProbe,
		Input: difftui.Input{
			Files:       files,
			Unranked:    !rev.Ranked(),
			Viewed:      sess.Viewed,
			Comments:    sess.Comments,
			Suggestions: sess.Suggestions,
			// What colleagues said, so a terminal reader is not sent to a browser to find out.
			// The incompleteness reason is dropped because the viewer takes the terminal over
			// immediately; `magus notes capture` keeps it, where a reader can still see it.
			ReviewComments: comments,
			Unfolded:       req.showGenerated,
			Link:           review.PathLinker(m.Root()),
			Start:          start,
		},
		Sync: sync,
		// Called at quit rather than computed here, so the line reports the fold the reader
		// LEFT in: `.` changes what is on the page, and a summary fixed at the opening state
		// would describe a session nobody had.
		// The counts the reader left in, plus what their reading EARNED. Receipts were minted
		// silently, so `v` read as a session bookmark and the receipt vocabulary in `--impact`
		// arrived later with no referent: the reader had done the work and never been told
		// it counted. Said at quit, once, naming where to see it.
		Summary: func(unfolded bool) string {
			line := review.CountsLine(rev, unfolded)
			if n := earned.Pending(); n > 0 {
				line += fmt.Sprintf("; %d file(s) now carry a read receipt (%s)", n, hint.Diff.With("--impact"))
			}
			return line
		},
	})
}

// attachDiffReview joins the shared review, server first.
//
// With a server running its session is the ONE session: the console tab and the agent are
// already on it, so the terminal joining anywhere else would be a fourth opinion wearing the
// same name. Without one there is nobody to pair with, so the changeset is computed here and
// progress goes straight into the file the server's own store would have written.
func attachDiffReview(ctx context.Context, m *magus.Magus, content review.Content, patch, base, riskBase string, paths []string) (difftypes.Diff, *difftypes.Review, difftui.ClosingSync, error) { //nolint:ireturn // the VCS driver is picked by detection at runtime
	asOf := changeset.PatchDigest(patch)
	if b := difftui.DialBridge(ctx, cliTokens{}, mcpAddrString(), paths, asOf); b != nil {
		sess := b.Session()
		return sess.Diff, sess, b, nil
	}
	rev, err := annotateDiff(ctx, m, content, paths, base, "", difftypes.Options{Patch: patch, RiskBase: riskBase})
	if err != nil {
		return difftypes.Diff{}, nil, nil, err
	}
	// Written straight into the store rather than through the server, which is sound only
	// because there is no server: with one running it owns this file, and two writers would
	// each persist their own idea of the whole set. Attach is what loads the marks a previous
	// session left AND what makes MarkViewed below have a session to write to.
	store := changeset.NewStore(m.CacheDir())
	sess := store.Attach(m.Root(), base, rev, asOf)
	return rev, sess, difftui.NewStoreSync(store, m.Root()), nil
}

func diffUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus diff [--generated] [--impact] [--unread] [--rev <base>...<head>] [--patch <file>|-] [<path>...] [flags]")
	fmt.Fprintln(w, "       magus diff --ack [<changed-path>...]")
	fmt.Fprintln(w, "       magus diff --thread <id> [--rev <base>...<head>]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Read the working tree's uncommitted changes, ordered by what they can break.")
	termkit.Prose(w, termkit.SystemProbe,
		"The uncommitted tree is the default subject;",
		"--rev reviews a committed range and --patch reviews a patch somebody handed you.")
	fmt.Fprintln(w, "")
	termkit.Prose(w, termkit.SystemProbe,
		"Generated files - declared target outputs - are folded away:",
		"reading one is reading a machine's restatement of a change made elsewhere, so the source edit is what to read.")
	fmt.Fprintln(w, "")
	// Name the ranking key exactly, and name what is NOT one. Only reach ranks; listing exported
	// API and coverage as "the evidence behind its rank" has a reader who sees a hot file
	// sitting eighth conclude the ranking weighed churn and dismissed it. Printing a number
	// beside a rank it did not earn teaches the wrong model.
	termkit.Prose(w, termkit.SystemProbe,
		"The order is: declared outputs last, then the widest reach first - how many files reference the most-referenced symbol the file changed.",
		"Reach needs a symbol index; without one there is no ranking key at all, and diff says so at the top and falls back to path order rather than implying a ranking.",
		fmt.Sprintf("Build the index with `%s`.", hint.GraphBuild.String()))
	fmt.Fprintln(w, "")
	termkit.Prose(w, termkit.SystemProbe,
		"Below the files the report prints the reading order of the hunks: a definition before its uses, an interface before its implementations, code before its tests,",
		"each with the sentence that placed it and a count proving every hunk is placed once.",
		"With no current symbol index there is no order, and a note names the rebuild.")
	fmt.Fprintln(w, "")
	termkit.Prose(w, termkit.SystemProbe,
		"Exported API, coverage, churn, and the agent trail are CONTEXT printed beside each file.",
		"None of them is a sort key.")
	fmt.Fprintln(w, "")
	termkit.Prose(w, termkit.SystemProbe,
		"--ack records that you have read files, at the content they hold now, so --impact can tell you what changed after you read it.",
		"Name the paths you read to record just those - read them in whatever editor or pager you already use - or pass none to cover the whole changeset.",
		"Stepping a file through in the viewer records it too.",
		"Editing a file afterwards voids its receipt.")
	fmt.Fprintln(w, "")
	// State the cutoff rather than leaving a missing rank ambiguous.
	termkit.Prose(w, termkit.SystemProbe,
		fmt.Sprintf("A hotspot rank is shown only inside the workspace's top %d.", difftypes.NotableRankCutoff),
		"A file that reports a commit count and no rank was measured and sits outside that cutoff;",
		"a file with no history line at all is one magus has never seen change.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	termkit.ProseItem(w, termkit.SystemProbe, "  --generated   ", "include the folded declared outputs")
	termkit.ProseItem(w, termkit.SystemProbe, "  --[no-]tui    ",
		"open the viewer (default: on); --no-tui prints the report instead.",
		"At a terminal the viewer opens joined to the session the console and an agent share:",
		"] and [ walk hunks, v marks one read, q leaves.",
		"Anywhere it cannot draw it stands aside on its own, so a script needs no flag.")
	termkit.ProseItem(w, termkit.SystemProbe, "  --impact      ",
		"append the blast radius of landing this: which projects rebuild, who owns them,",
		"an estimate from recorded run times, what the advisors say, which notes anchor what you changed,",
		"and what the authors asked magus while writing it.",
		"It gates nothing and changes no exit code.")
	termkit.ProseItem(w, termkit.SystemProbe, "  --ack         ",
		"record that you have read the named changed files, or all of them when you name none.",
		"Needs a terminal.")
	termkit.ProseItem(w, termkit.SystemProbe, "  --reason      ", "an optional note kept with an --ack")
	termkit.ProseItem(w, termkit.SystemProbe, "  --watch       ", "re-read and re-render whenever the working tree changes")
	termkit.ProseItem(w, termkit.SystemProbe, "  --unread      ",
		"narrow the report to the hunks no read mark covers, under every -o; -o name prints one path:start-end per hunk.",
		"It always exits 0. Where the marks cannot be read it says the read state is unknown and calls no hunk unread.")
	termkit.ProseItem(w, termkit.SystemProbe, "  --rev         ",
		"review a committed range instead of the working tree, as base...head:",
		"a colleague's branch, or an agent's finished work")
	termkit.ProseItem(w, termkit.SystemProbe, "  --patch       ", "review a patch file instead of the working tree; - reads stdin")
	termkit.ProseItem(w, termkit.SystemProbe, "  --thread      ",
		"narrow the review to one pull request thread, named by its thread id or any of its comments' ids;",
		"the report lists each thread's id beside its hunk, and -o json under each file's threads.",
		"The viewer opens on its hunk. Printed, it is the conversation oldest first, the hunk,",
		"and what the change there reaches: callers, who it is public to, coverage, conformance and notes.")
	termkit.ProseItem(w, termkit.SystemProbe, "  --baseline    ",
		fmt.Sprintf("a `%s` of the base, which adds what each changed symbol did to the API", hint.GraphExport.With("--symbols", "-o", "json")),
		"and the smallest semver bump that proves. Signatures are compared as the indexer rendered them,",
		"so the base must come from the same indexers this tree is indexed with.")
	termkit.ProseItem(w, termkit.SystemProbe, "  --conformance-min-cohort ",
		"how many declarations a conformance norm needs before a changed symbol is compared against it;",
		"defaults to knowledge.conformance.gates.min_cohort")
	termkit.ProseItem(w, termkit.SystemProbe, "  --conformance-min-share  ",
		"the share of a norm's declarations, above 0 and at most 1, that must agree;",
		"defaults to knowledge.conformance.gates.min_share.",
		"Both apply to the printed report, not the viewer.")
}

// diffHistoryCommits bounds the git-log walk the churn lenses do. 500 matches what the
// server's insight scan uses, so the CLI and the console rank the same files the same way:
// two different windows would report two different "hottest file" answers for one tree.
const diffHistoryCommits = 500

// diffReport is the changeset plus its impact, the document --impact serves on the
// structured path.
//
// It exists ONLY under the flag. Without it the emitted value is types.Diff exactly as
// before, so nothing reading `magus diff -o json` today meets a key it has never seen.
type diffReport struct {
	difftypes.Diff `yaml:",inline"`
	Impact         *reviewimpact.Report `json:"impact,omitempty" yaml:"impact,omitempty"`
}

// impactSources hands the impact report the lenses whose readers live in this package.
//
// rootOverride, not m.Root(): the workspace loaders are once-per-process and keyed on the
// override they were first handed, so each graph or workspace load must spell the root
// exactly as diffCommand's own load did.
func impactSources(m *magus.Magus, rootOverride string) reviewimpact.Sources {
	return reviewimpact.Sources{
		OwnershipCommits: diffHistoryCommits,
		HistoryPath:      globalCfg.HistoryPath,
		Advisors: func(ctx context.Context, base string, rev difftypes.Diff) ([]review.AdviceSection, []string, error) {
			return runLocalAdvisors(ctx, m, base, rev)
		},
		Anchors: func(ctx context.Context, rev difftypes.Diff) ([]review.AnchorHit, error) {
			return impactAnchors(ctx, rootOverride, rev)
		},
		LoadGraph: func(ctx context.Context) (*knowledge.Graph, error) {
			return loadKnowledgeGraph(ctx, rootOverride, false, false, true)
		},
		UnusedSymbols: func(ctx context.Context, g *knowledge.Graph, driver types.RevisionReader, head string, changed []string) ([]string, bool) {
			delta, ok := evidence.ReadSymbolDelta(ctx, m.Root(), g, driver, head, changed)
			return delta.Left, ok
		},
		RequiredIn: func(ctx context.Context) func(string) bool {
			ws, err := inspectWorkspace(ctx, rootOverride)
			if err != nil {
				return nil
			}
			return review.RequiredMatcher(ws)
		},
		BulkReasons: review.BulkReasons,
	}
}

// impactAnchors joins the declared notes stores against the changeset. root is the override
// diffCommand's own load was handed, because the graph loader is once-per-process and keyed on it.
func impactAnchors(ctx context.Context, root string, rev difftypes.Diff) ([]review.AnchorHit, error) {
	declared := globalCfg.Knowledge.Notes
	dirs := review.NoteDirs{Shared: declared.Shared, Private: declared.Private}
	return review.ChangesetAnchors(ctx, root, dirs, func(ctx context.Context) (*knowledge.Graph, error) {
		return loadKnowledgeGraph(ctx, root, false, false, true)
	}, rev)
}

// runLocalAdvisors runs the read-only PR advisors against the local tree, with the workspace on
// the context the way `magus buzz` has it. The workspace is attached, not loaded again:
// loadMagus panics on a second root.
func runLocalAdvisors(ctx context.Context, m *magus.Magus, base string, rev difftypes.Diff) ([]review.AdviceSection, []string, error) {
	return review.LocalAdvice(types.WithWorkspace(ctx, viewInternals(m)), m.Root(), base, rev)
}
