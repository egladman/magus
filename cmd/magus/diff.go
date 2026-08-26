package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/ci/forecast"
	session "github.com/egladman/magus/internal/diff"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/diff"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/interp/bindings"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/internal/prompt"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/gopherbuzz"
	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// diffCmd implements `magus diff`: the working tree's changes, annotated and ordered by
// what they can break.
//
// It is the TERMINAL client of the same annotation join the console's Diff surface reads
// and an agent joins over MCP. One computation, three transports - so a person reviewing in a
// terminal and an agent pairing with them are looking at the same ranking rather than two
// tools' opinions of one changeset.
func diffCmd(ctx context.Context, root string, args []string) error {
	var rf *gen.DiffFlags
	rest, err := cmdParse("diff", args, func(fs *flag.FlagSet) {
		rf = gen.BindDiff(fs)
		fs.Usage = func() { diffUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}
	// With --ack the positionals are PATHS, not a patch. Reading happens in whatever the
	// reader already uses - vim, magit, a pager, an IDE - and magus has no business
	// requiring its own viewer to record that it happened. Without this the only door open
	// to somebody who reviews in their editor is the blanket ack, which is the one path
	// this feature least wants to be the default.
	//
	// Safe to overload because --ack already refuses a patch source: a receipt fingerprints
	// the working tree, and a patch describes files that may not be there.
	var ackPaths []string
	if rf.Ack {
		ackPaths, rest = rest, nil
	}
	src, err := diffInputFromArgs(rest)
	if err != nil {
		return err
	}
	if rf.Rev != "" {
		if src.kind != inputWorkingTree {
			return usagef("magus diff: --rev names the changeset, so it cannot be combined with %s", src.label)
		}
		if src, err = revRangeFromFlag(rf.Rev); err != nil {
			return err
		}
	}
	if rf.Watch && src.kind != inputWorkingTree {
		// stdin is consumed once and a patch file is a snapshot someone handed us; re-reading
		// either on every tree change would re-render identical output forever.
		return usagef("magus diff: --watch reads the working tree, so it cannot be combined with %s", src.label)
	}
	if rf.Ack && !src.addressable() {
		// A receipt fingerprints content magus can name, and a patch describes files that may
		// not be here at all. Accepting one would record receipts against whatever the working
		// tree happens to hold - an acknowledgement of something nobody read.
		//
		// A revision range IS nameable, so it is admitted: its receipts fingerprint the file at
		// that revision, never the reader's own checkout.
		return usagef("magus diff: --ack fingerprints content magus can address, so it cannot be combined with %s", src.label)
	}
	if rf.Ack && rf.Watch {
		return usagef("magus diff: --ack records once and returns, so it cannot be combined with a live view")
	}
	// --reason is optional, deliberately. Requiring a sentence the way spells.allow_shadow does
	// would not hold here: an allow_shadow entry is written a handful of times in a repository's
	// life, while a changeset ack is written daily and becomes a form field - teaching the
	// reader to type something they do not mean.
	//
	// The two controls that hold are the ones nobody can satisfy by typing faster: the agent
	// guard, and the terminal check below.
	if rf.Ack && !isInteractiveTTY() {
		// The agent guard denies --ack outright, but it fails OPEN where it is not wired,
		// and a receipt minted by a script is precisely the laundering this refuses. Not a
		// usage error: the flags are fine and the caller is not a person.
		// 2 rather than 1: the flags are fine and the caller is not who this is for, which
		// the documented taxonomy separates from a 1 (the changeset could not be read).
		fmt.Fprintln(os.Stderr, "magus: diff --ack records that a person read this, so it needs an interactive terminal")
		return errSilent{exitCode: 2}
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	term := diffTUITerm{
		Reads:  isInteractiveTTY(),
		Paints: tty.IsTerminalWriter(os.Stdout, tty.SystemProbe),
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	tui := wantsTUI(rf, src, opts.Format, term, m.DiffTUIEnabled())

	render := func() error { return renderDiff(ctx, m, src, opts, rf, root, tui, rf.Impact, ackPaths) }
	if rf.Watch {
		return watchDiff(ctx, m, render)
	}
	return render()
}

// diffInputKind is where the patch comes from.
type diffInputKind int

const (
	inputWorkingTree diffInputKind = iota
	inputStdin
	inputFile
	inputRevRange
)

// diffInput names a patch source and how to describe it to the reader.
type diffInput struct {
	kind  diffInputKind
	path  string // for inputFile
	base  string // for inputRevRange
	head  string // for inputRevRange
	label string
}

// addressable reports whether magus can name what this source contains well enough to record a
// reading of it.
//
// The working tree and a revision range are both tree states magus can re-derive and re-digest; a
// patch on stdin and a patch in a file are bytes somebody handed over, describing files that may
// not exist here at all. That is the line --ack has always drawn - it was just spelled "is this the
// working tree" back when the working tree was the only addressable source.
func (in diffInput) addressable() bool {
	return in.kind == inputWorkingTree || in.kind == inputRevRange
}

// revRangeFromFlag resolves --rev, which is written base...head the way git and the branch-overlap
// reader already spell a symmetric difference.
//
// Two dots are REFUSED rather than quietly accepted as three. They are a different question - what
// head has that base has, including everything base gained meanwhile - and a reviewer who typed the
// git spelling out of habit would get a diff padded with commits the branch author never wrote.
func revRangeFromFlag(rev string) (diffInput, error) {
	base, head, ok := strings.Cut(rev, "...")
	if !ok {
		if b, _, two := strings.Cut(rev, ".."); two {
			return diffInput{}, usagef("magus diff: --rev takes base...head with three dots, got %q. "+
				"Three dots is what head added since it diverged; two dots would also count everything "+
				"%s gained meanwhile, which the branch author did not write", rev, b)
		}
		return diffInput{}, usagef("magus diff: --rev takes a range written base...head, got %q", rev)
	}
	if base == "" || head == "" {
		return diffInput{}, usagef("magus diff: --rev needs both ends, got %q. "+
			"magus does not fill in a default: which branch you are comparing against is the "+
			"question, not a detail", rev)
	}
	return diffInput{kind: inputRevRange, base: base, head: head, label: "the range " + rev}, nil
}

// diffInputFromArgs resolves the one optional positional.
//
// A positional is accepted ONLY as `-` or a readable patch file. Anything else - and a git
// ref is the case that matters, because everyone arriving from `git diff <ref>` or `gh pr
// diff` types one first - is refused loudly. Swallowing it printed a plausible list of the
// reader's OWN uncommitted edits under exit 0, which is the worst possible failure: the
// output looks exactly like an answer to the question they asked.
//
// Reading a patch rather than the working tree is what lets this annotate a changeset it did
// not produce - a colleague's patch, a stash, a mail attachment - and it composes, which the
// working-tree-only version never did.
func diffInputFromArgs(rest []string) (diffInput, error) {
	if len(rest) == 0 {
		return diffInput{kind: inputWorkingTree, label: "the working tree"}, nil
	}
	if len(rest) > 1 {
		if err := gitExternalDiffRefusal(rest); err != nil {
			return diffInput{}, err
		}
		return diffInput{}, usagef("magus diff: takes at most one patch argument, got %d", len(rest))
	}
	arg := rest[0]
	if arg == "-" {
		return diffInput{kind: inputStdin, label: "a patch on stdin"}, nil
	}
	if st, serr := os.Stat(arg); serr == nil && !st.IsDir() {
		return diffInput{kind: inputFile, path: arg, label: "the patch in " + arg}, nil
	}
	return diffInput{}, usagef("magus diff: %q is neither a readable patch file nor `-`. "+
		"diff reads the working tree and takes no ref; for a committed range use `git diff %s`, "+
		"and pipe a patch in with `git diff %s | magus diff -`", arg, arg, arg)
}

// gitExternalDiffRefusal names the mistake when magus has been wired as GIT_EXTERNAL_DIFF or
// diff.external, and points at the git-native setting that does work.
//
// git calls an external diff once PER FILE with seven arguments, because the contract is for a
// program that renders one file's diff. Almost everything magus has to say is a property of the
// whole changeset - which projects rebuild, who owns them, what it costs, what to read first -
// so honoring that contract would mean printing the report once per file or not at all.
//
// git's pager is the same integration without the mismatch: it hands over the entire diff on
// stdin, exactly once, which is an input `magus diff -` already takes. So this refuses, and
// says which line to put in the config instead. Landing here means someone tried, which makes
// it the one moment they will read the answer.
//
// GIT_DIFF_PATH_TOTAL is the signal rather than the argument count alone: git sets it on every
// external-diff call, and seven positionals are otherwise just a typo.
func gitExternalDiffRefusal(rest []string) error {
	if len(rest) != 7 || os.Getenv("GIT_DIFF_PATH_TOTAL") == "" {
		return nil
	}
	return usagef("magus diff: git invoked this as an external diff, one file at a time (%q). "+
		"magus reports on a whole changeset, so it cannot answer per file. "+
		"Use git's pager instead, which hands over the entire diff at once: "+
		"`git config pager.diff 'magus diff -'` - then plain `git diff` renders through magus, "+
		"and `git --no-pager diff` still gets you the raw patch", rest[0])
}

// diffTUITerm is the terminal the viewer was handed, split by descriptor, because the viewer
// uses two of them and one probe does not answer for both.
type diffTUITerm struct {
	// Reads is the shared interactive gate every stepping surface asks for: stdin and stderr
	// are both terminals.
	Reads bool
	// Paints is stdout, which the viewer draws the changeset on. It is a separate condition
	// because `magus diff > file` leaves stdin a keyboard, so Reads alone would let the viewer
	// try to draw into a file.
	Paints bool
}

// ok reports whether the viewer can have this terminal.
func (t diffTUITerm) ok() bool { return t.Reads && t.Paints }

// wantsTUI decides whether this invocation opens the viewer.
//
// The viewer is the DEFAULT at a terminal, so every condition below is a quiet FALLBACK rather
// than a refusal. Nobody asked for the viewer: a script running `magus diff -o json`, a patch
// file, a watch loop are all ordinary invocations, and erroring at them would punish a caller
// for a default they never chose. The report prints and nothing is said.
//
// --no-tui is the one explicit answer, and it wins over the config the way a flag always does.
//
// It takes the terminal as an ARGUMENT rather than probing it, which is what makes this
// testable without a pty.
func wantsTUI(rf *gen.DiffFlags, src diffInput, format Format, term diffTUITerm, enabled bool) bool {
	switch {
	case rf.NoTui, !enabled:
		return false
	// All three END in output the viewer has nowhere to put - a receipt count, a report, and a
	// prompt to copy. They are requests for an answer rather than for somewhere to read.
	case rf.Ack, rf.Impact, rf.Prompt:
		return false
	// A patch somebody handed us is bytes about files that may not be here, and a watch loop
	// drives the terminal itself. A revision range is neither: it is a tree state magus can
	// address, which is the whole reason the viewer opens over one.
	case !src.addressable(), rf.Watch:
		return false
	case format != outputText:
		return false
	default:
		return term.ok()
	}
}

// readPatch returns the unified patch for this input.
func (in diffInput) readPatch(ctx context.Context, m *magus.Magus) (string, string, error) {
	switch in.kind {
	case inputStdin:
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", "", fmt.Errorf("magus diff: read stdin: %w", err)
		}
		return string(b), "stdin", nil
	case inputFile:
		b, err := os.ReadFile(in.path)
		if err != nil {
			return "", "", fmt.Errorf("magus diff: %w", err)
		}
		return string(b), in.path, nil
	case inputRevRange:
		// The BASE side, because every reader of types.Diff.Base labels it "compared against".
		// Returning the head made the prompt say a branch was compared against itself, which is
		// not a wrong-looking value a reader would question - it is a sentence that parses.
		p, err := m.RangeDiff(ctx, in.base, in.head)
		if err != nil {
			return "", "", fmt.Errorf("magus diff: %w", err)
		}
		return p, in.base, nil
	default:
		p, err := m.WorkingDiff(ctx, nil)
		return p, "working", err
	}
}

// renderDiff reads one patch and emits it in the requested format.
//
// impact is ADDITIVE: with it off, every byte emitted here is what this command emitted
// before the flag existed, which is what lets a script parsing `magus diff -o json` keep
// working and what keeps the flag honest about being context rather than a gate.
func renderDiff(ctx context.Context, m *magus.Magus, src diffInput, opts OutputOptions, rf *gen.DiffFlags, rootOverride string, tui, impact bool, ackPaths []string) error {
	patch, base, err := src.readPatch(ctx, m)
	if err != nil {
		return err
	}
	if strings.TrimSpace(patch) == "" {
		// An empty input is a STATE, and saying so beats printing an empty table that reads as
		// a failure to find anything. The sentence differs by source: a clean tree is good
		// news, an empty patch file is probably a mistake.
		if opts.Format == outputText {
			if src.kind == inputWorkingTree {
				fmt.Println("clean: every change is committed")
			} else {
				fmt.Printf("empty: %s carries no changes\n", src.label)
			}
			return nil
		}
		return emitFormatted(opts, types.Diff{Base: base})
	}

	paths := changedPathsFromPatch(patch)
	// Bytes went in and no file came out, so this is not an empty changeset - it is a patch
	// magus could not read, and the two must never print the same thing. Reporting "0 files to
	// read" at exit 0 is the worst available answer: a reader checking whether they had
	// anything left to review is told no, and believes it.
	//
	// Scoped to a patch the caller handed us. A working tree's patch and a revision range's both
	// come from whichever VCS adapter is active, and refusing there would turn "a backend spells
	// its headers a third way" into a hard failure of the whole command - a worse bug than the
	// one being fixed.
	if len(paths) == 0 && !src.addressable() {
		if strings.Contains(patch, "\x1b[") {
			return fmt.Errorf("magus diff: %s is colorized, so its headers carry escape sequences "+
				"and no longer begin a line. This is what a VCS emits when it thinks it is writing "+
				"to a terminal, which is exactly the case when magus is its pager. Turn color off "+
				"for the diff it hands over: `hg --config color.mode=off`, `jj --config ui.color=never`, "+
				"or `--color=never` on any of them", src.label)
		}
		return fmt.Errorf("magus diff: %s has content but no file headers magus can read; "+
			"it expects a unified diff (`diff --git a/x b/x`, or a `--- a/x` / `+++ b/x` pair)", src.label)
	}
	// Resolved once: every receipt this command mints or reports on must agree about which
	// tree it is talking about, and a second resolve is a second chance to disagree.
	content := contentOf(ctx, m, src)
	if tui {
		return runDiffTUI(ctx, m, content, patch, base, paths, rf.Generated)
	}
	rev, err := annotateDiff(ctx, m, content, paths, base)
	if err != nil {
		return err
	}
	if rf.Ack {
		reason := strings.TrimSpace(rf.Reason)
		scoped, err := scopeAck(rev, ackPaths)
		if err != nil {
			return err
		}
		n, err := ackChangeset(content, m.CacheDir(), scoped, reason, time.Now())
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

	// Before the format switch, because a prompt is TEXT for a person to paste whatever -o says.
	// The other formats project a record; this one is prose, and there is nothing to project.
	if rf.Prompt {
		// --impact already means "give me the fuller answer", so it selects the long form here
		// rather than a second flag that would ask the same question again.
		variant := prompt.Short
		if impact {
			variant = prompt.Long
		}
		// Best-effort: a backend that cannot report branches is an ordinary state, and the
		// prompt omits that section rather than refusing to render.
		overlap, _ := m.BranchChanges(ctx, branchOverlapLimit)
		out := review.Prompt(review.PromptInput{
			Changeset: rev,
			Origin:    m.ReviewOrigin(ctx),
			Overlap:   overlap,
			Variant:   variant,
		})
		_, err := io.WriteString(os.Stdout, out)
		return err
	}

	var pre *diffImpact
	if impact {
		p := collectImpact(ctx, m, rootOverride, rev)
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
		for _, f := range rev.Files {
			if f.Generated() && !rf.Generated {
				continue
			}
			fmt.Println(f.Path)
		}
		return nil
	}
	hintReviewPrompt(os.Stderr, rev, rf)
	return printDiffText(rev, rf.Generated, pathLinker(m.Root()), pre)
}

// promptHintFiles is the changeset size above which reading alone stops being the whole job. Set
// where a reader plausibly wants a second pass rather than at a number that fires on every commit:
// a hint printed every time is one nobody sees by the third time, which is exactly when it starts
// to matter.
const promptHintFiles = 10

// hintReviewPrompt mentions `--prompt` on a changeset big enough to want a second reader.
//
// It exists because a flag nobody knows about is a feature nobody has. `agent install` settled the
// same question the same way - it prints the managed AGENTS.md block only when the reader's file
// is missing it or carrying a stale one - and this is that discipline applied to the other place
// magus hands a person text to carry somewhere itself refuses to go.
//
// stderr, so a piped or redirected report is unchanged, and only where hints are enabled at all.
func hintReviewPrompt(w io.Writer, rev types.Diff, rf *gen.DiffFlags) {
	if rf.Prompt || !interactive.HintsEnabled() || len(rev.Files) < promptHintFiles {
		return
	}
	interactive.Emit(w, fmt.Sprintf(
		"%d changed files: `magus diff --prompt` prints a review prompt to paste into your own model - the reading order, what rebuilds, and what could not be measured. It calls no model and sends nothing",
		len(rev.Files)))
}

// branchOverlapLimit caps how many branches the prompt's overlap lookup examines, and so how many
// forks it costs. It matches the console route's bound for the same reason: unbounded, it would be
// the most expensive thing in the command on a repository with a hundred stale branches.
const branchOverlapLimit = 20

// annotateDiff computes the annotated changeset for a set of changed paths.
//
// One definition, because the TUI and the one-shot renderer must show the same facts - two
// callers folding on their own overlays is how "the console said 12 files reference this and
// the CLI said nothing" happens.
func annotateDiff(ctx context.Context, m *magus.Magus, content reviewedContent, paths []string, base string) (types.Diff, error) {
	rev, err := m.Diff(ctx, paths)
	if err != nil {
		return types.Diff{}, err
	}
	rev.Base = base
	// The churn lenses, from a fresh scan. The daemon serves these from a warm cache; a
	// one-shot CLI has none, so it pays the bounded git-log walk here. Best-effort: a
	// workspace with no history simply reports no churn rather than failing the diff.
	// Files: true is required - without it the lens ranks PROJECTS and the per-file list is
	// empty, so every file would silently report no churn.
	if hot, herr := m.Hotspots(ctx, types.InsightOptions{Commits: diffHistoryCommits, Files: true}); herr == nil {
		var projects []types.TrendEntry
		if tr, terr := m.Trend(ctx, types.InsightOptions{Commits: diffHistoryCommits}); terr == nil {
			projects = tr.Projects
		}
		rev.AttachChurn(hot.Files, projects)
	}
	// The agent trail: which sessions wrote each file and what they had read first. Empty when
	// no guard hook is wired, which is the common case rather than a fault.
	rev.AttachReplay(diffTouches(m.Root(), m.CacheDir(), paths))
	// Which of these files somebody has recorded reading. Best-effort like every other
	// overlay: an unreadable store leaves every file DiffReadUnknown, which renders as
	// unmeasured rather than as unread.
	if states, serr := review.ReadStates(m.CacheDir(), paths, content.digest); serr == nil {
		rev.AttachReadState(states)
	}
	return rev, nil
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
	var outputGlobs []string
	var projectIgnores []types.IgnorePattern
	for _, p := range m.All() {
		outputGlobs = append(outputGlobs, p.AllOutputs()...)
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
			// still be reading, and this surface is meant to be scrolled.
			fmt.Println()
			fmt.Println(strings.Repeat("-", 60))
			fmt.Println()
			if err := render(); err != nil {
				return err
			}
		}
	}
}

// pathLinker returns a function that makes a workspace-relative path clickable, or the
// identity when the terminal cannot use one.
//
// A path printed as bare text is a path the reader has to retype or hand to an editor
// themselves, and this surface prints nothing BUT paths. delta has had OSC 8 links for years
// and they are the cheapest legibility win available here.
//
// The gate is tty.WantsHyperlinks, which already refuses a pipe, TERM=screen, and - the
// subtle one - an SSH session, where a file:// URL names a path on the remote machine that
// the local terminal would resolve against the wrong filesystem. Keeping that decision in one
// place is what preserves the property that piped output carries no escape sequences at all.
func pathLinker(root string) func(string) string {
	if !tty.WantsHyperlinks(os.Stdout, tty.SystemProbe) {
		return func(p string) string { return p }
	}
	return func(p string) string {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, p)
		}
		return tty.Hyperlink(p, (&url.URL{Scheme: "file", Path: abs}).String())
	}
}

// runDiffTUI opens the interactive reader over the working tree's changeset.
//
// This is what makes "three clients, one session" true for a terminal. `magus diff` already
// shared the COMPUTATION with the console and the MCP surface; what it did not share was the
// coordination - where the reader is, what they have read, what an agent has asked them to
// look at. Reading a diff is not a report you print once, it is a place you are IN.
func runDiffTUI(ctx context.Context, m *magus.Magus, content reviewedContent, patch, base string, paths []string, showGenerated bool) error {
	rev, sess, sync, err := attachDiffSession(ctx, m, content, patch, base, paths)
	if err != nil {
		return err
	}
	files := diffTUIFiles(rev, session.ParseHunks(patch))
	// Wrapped so finishing a file in the viewer leaves a receipt behind it. The marks were
	// always explicit; this is what makes them outlive the session.
	earned := newEarnedSync(sync, content, m.CacheDir(), files, sess.Viewed)
	sync = earned
	// Closed here rather than inside the viewer: how a Sync gets its writes out - inline, or over a
	// goroutine that has to be drained - is this file's business, and the viewer stays ignorant
	// of it. Deferred before Run, so it also runs on the interrupts Run RETURNS from: `q`,
	// Ctrl-C read as a key, a cancelled context. A raw SIGINT unwinds nothing, and the reader
	// loses the queue along with the restored terminal.
	defer sync.close()
	// Re-placed against THIS patch. The daemon resolved each thread's hunk against the working
	// tree, which is not what the viewer is showing when the patch came from a file or stdin -
	// and a remark drawn against hunk 3 of the wrong patch is worse than one drawn against its
	// file, because the viewer presents it with no hedge.
	threads, _ := daemonReviewThreads(ctx)
	threads = session.PlaceThreads(session.ParseHunks(patch), threads)
	return diff.Run(ctx, diff.Options{
		In:    os.Stdin,
		Out:   os.Stdout,
		Probe: tty.SystemProbe,
		Input: diff.Input{
			Files:       files,
			Unranked:    !rev.Ranked(),
			Viewed:      sess.Viewed,
			Comments:    sess.Comments,
			Suggestions: sess.Suggestions,
			// What colleagues said, so a terminal reader is not sent to a browser to find out.
			// The incompleteness reason is dropped because the viewer takes the terminal over
			// immediately; `magus notes capture` keeps it, where a reader can still see it.
			Threads:  threads,
			Unfolded: showGenerated,
			Link:     pathLinker(m.Root()),
		},
		Sync: sync,
		// Called at quit rather than computed here, so the line reports the fold the reader
		// LEFT in: `.` changes what is on the page, and a summary fixed at the opening state
		// would describe a session nobody had.
		// The counts the reader left in, plus what their reading EARNED. Receipts were minted
		// silently, so `v` read as a session bookmark and the receipt vocabulary in `--impact`
		// arrived later with no referent - the reader had done the work and never been told
		// it counted. Said at quit, once, naming where to see it.
		Summary: func(unfolded bool) string {
			line := diffCountsLine(rev, unfolded)
			if n := earned.pending(); n > 0 {
				line += fmt.Sprintf("; %d file(s) now carry a read receipt (magus diff --impact)", n)
			}
			return line
		},
	})
}

// attachDiffSession joins the shared review, daemon first.
//
// With a daemon running its session is the ONE session - the console tab and the agent are
// already on it, so the terminal joining anywhere else would be a fourth opinion wearing the
// same name. Without one there is nobody to pair with, so the changeset is computed here and
// progress goes straight into the file the daemon's own store would have written.
func attachDiffSession(ctx context.Context, m *magus.Magus, content reviewedContent, patch, base string, paths []string) (types.Diff, *types.DiffSession, diffSync, error) {
	asOf := session.PatchDigest(patch)
	if b := dialDiffBridge(ctx, paths, asOf); b != nil {
		return b.session.Diff, b.session, b, nil
	}
	rev, err := annotateDiff(ctx, m, content, paths, base)
	if err != nil {
		return types.Diff{}, nil, nil, err
	}
	// Written straight into the store rather than through the daemon, which is sound only
	// because there is no daemon: with one running it owns this file, and two writers would
	// each persist their own idea of the whole set. Attach is what loads the marks a previous
	// session left AND what makes MarkViewed below have a session to write to.
	store := session.NewStore(m.CacheDir())
	sess := store.Attach(m.Root(), base, rev, asOf)
	return rev, sess, diffStoreSync{store: store, root: m.Root()}, nil
}

// diffTUIFiles joins the annotations to the patch: one is ordered by consequence, the other
// by whatever the VCS emitted, and the reader wants the first order with the second's text.
//
// The annotation order is authoritative and is never recomputed here - types.Diff.
// SortForReading is the single definition of review order.
func diffTUIFiles(rev types.Diff, parsed []session.FileHunks) []diff.File {
	byPath := make(map[string][]session.Hunk, len(parsed))
	for _, f := range parsed {
		byPath[f.Path] = f.Hunks
	}
	out := make([]diff.File, 0, len(rev.Files))
	for _, f := range rev.Files {
		file := diff.File{Path: f.Path, Generated: f.Generated(), Facts: diffFileFacts(f)}
		for _, h := range byPath[f.Path] {
			file.Hunks = append(file.Hunks, diff.Hunk{
				Index: h.Index, Header: h.Header, Lines: h.Lines, Digest: h.Digest,
				Emph: session.RawLineEmphasis(h),
			})
		}
		out = append(out, file)
	}
	return out
}

// diffSync is a diff.Sync with a shutdown. The two implementations get their writes out
// differently - one to a local file, one over a goroutine that has to be drained - and the
// viewer must not have to know which it was handed.
type diffSync interface {
	diff.Sync
	close()
}

// diffStoreSync persists the reader's progress with no daemon in the picture. There is no
// cursor to publish: nobody is listening.
type diffStoreSync struct {
	store *session.Store
	root  string
}

func (diffStoreSync) SetCursor(types.DiffCursor) {}

// SetViewed stays SYNCHRONOUS, unlike the bridge's: this is memory and a file under the cache
// dir, so it costs a keypress nothing that a queue would win back.
func (s diffStoreSync) SetViewed(digest string, on bool) {
	s.store.MarkViewed(s.root, digest, on)
}

func (diffStoreSync) close() {}

// diffBridge is the running daemon's session, reached over the same loopback routes the
// console uses. The session it attached to travels with it, because a transport that could
// hand back a session it had not attached would be a client of nothing.
//
// Writes leave on sends rather than on the caller's stack: every one of them is provoked by a
// KEYPRESS - a cursor move, a read mark - and an inline post would put a network round trip
// between the key and the screen moving, up to the full diffBridgeWrite deadline against a
// daemon that has stopped answering.
//
// The two kinds of write get two queues, because they fail in opposite directions. A cursor is
// idempotent by REPLACEMENT - the newest one says where the reader is and everything behind it
// says where they no longer are - so that queue evicts to stay current. A read mark is
// replaceable by nothing: the bridge is the only writer of it on this path, so a dropped
// `viewed` is a hunk the reader read that no client will ever be told about. Its queue is deep,
// waited on, and drained in full at close.
type diffBridge struct {
	addr    string
	token   string
	session *types.DiffSession

	cursors chan diffSessionOp
	marks   chan diffSessionOp
	// stop is closed in place of the op channels, so a send racing close cannot panic.
	stop chan struct{}
	// done closes when deliver has returned, which is the only proof the sender is gone.
	done chan struct{}
	// cancel aborts the post on the wire once close has spent its budget.
	cancel context.CancelFunc
}

// diffBridgeAttach bounds the GET that annotates and attaches. It is the expensive route -
// symbol shards and a reverse closure - so this is generous; the alternative on a slow answer
// is not a faster one, it is computing the same thing locally.
const diffBridgeAttach = 10 * time.Second

// diffBridgeWrite bounds a coordination write. Short, because a wedged daemon must not hold
// the sender long enough for the queue behind it to overflow, and because it is also how long
// quitting waits for the last mark to leave.
const diffBridgeWrite = time.Second

// diffBridgeQueue bounds the CURSOR writes waiting to leave. Small on purpose: a backlog means
// the daemon has stopped keeping up, and at that point the newest cursor is the only one worth
// having - the ones behind it describe somewhere the reader no longer is.
const diffBridgeQueue = 8

// diffBridgeMarks bounds the read marks waiting to leave. Deep rather than small, because
// nothing here may be evicted and the volume is bounded by a human pressing `v`: reaching the
// end of it takes a daemon that has stopped answering AND a minute of uninterrupted marking.
const diffBridgeMarks = 64

// diffBridgeClose caps how long quitting waits for the queue to empty. The budget is one write
// deadline per queued op, and this is the ceiling on it: a wedged daemon costs the shell a few
// seconds rather than the whole queue's worth of deadlines.
const diffBridgeClose = 3 * time.Second

// dialDiffBridge attaches to the daemon's session, or returns nil when there is nothing to
// join - no token, no listener, a daemon with no workspace. Every one of those is an ordinary
// state rather than an error: the terminal reads the diff on its own and says nothing about
// a daemon the reader never asked for.
//
// asOf is the digest of the patch about to be rendered, and the session is DECLINED unless
// the daemon computed its changeset from the same bytes. Without that check a daemon serving
// a different workspace - the main checkout while this is a worktree - answers confidently
// about a tree the reader is not looking at, and the coordinate every comment and every
// viewed mark is keyed by would silently mean something else.
func dialDiffBridge(ctx context.Context, paths []string, asOf string) *diffBridge {
	token, err := auth.Load()
	if err != nil {
		return nil
	}
	q := url.Values{}
	for _, p := range paths {
		q.Add("path", p)
	}
	b := &diffBridge{addr: mcpAddrString(), token: token}
	ctx, cancel := context.WithTimeout(ctx, diffBridgeAttach)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+b.addr+"/api/v1/diff?"+q.Encode(), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var sess types.DiffSession
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		return nil
	}
	if sess.AsOf != asOf {
		return nil
	}
	b.session = &sess
	b.start()
	return b
}

// start wires the sender. Split from dialDiffBridge so the queue can be driven against a stub
// server without a daemon, a token, or the attach round trip.
func (b *diffBridge) start() {
	b.cursors = make(chan diffSessionOp, diffBridgeQueue)
	b.marks = make(chan diffSessionOp, diffBridgeMarks)
	b.stop = make(chan struct{})
	b.done = make(chan struct{})
	// Not the caller's context and not the session's: a cancelled read must not turn the last
	// read mark into a lost one. close owns this cancel and nothing else does.
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	go b.deliver(ctx)
}

// diffSessionOp is one mutation of the shared session, the wire shape the daemon's
// /api/v1/diff/session route takes.
type diffSessionOp struct {
	Op     string `json:"op"`
	Path   string `json:"path,omitempty"`
	Hunk   int    `json:"hunk,omitempty"`
	Digest string `json:"digest,omitempty"`
	On     bool   `json:"on,omitempty"`
}

// SetCursor publishes where the reader is looking. The reply is discarded on purpose: it
// carries the session's own cursor, and applying that would let another client move this
// reader's viewport - which is the one thing the paired-review design forbids.
func (b *diffBridge) SetCursor(c types.DiffCursor) {
	b.queueCursor(diffSessionOp{Op: "cursor", Path: c.Path, Hunk: c.Hunk})
}

// SetViewed publishes a read mark, which the daemon persists for every client at once.
func (b *diffBridge) SetViewed(digest string, on bool) {
	b.queueMark(diffSessionOp{Op: "viewed", Digest: digest, On: on})
}

// queueCursor hands the newest cursor to the sender, evicting the OLDEST when the queue is
// full. It never blocks: the key loop is what calls it, and diff.Sync promises best-effort
// delivery precisely so a slow daemon costs the reader nothing.
//
// A plain non-blocking send drops the ARRIVING op instead, which keeps exactly the positions
// the reader has already walked past and throws away the only one still true.
//
// Evicting and sending are two steps, and deliver can take the head between them - which is why
// this retries rather than assuming the receive made room. Each iteration ends with a slot free
// whichever way that race went, and stop short-circuits both selects once close has run.
func (b *diffBridge) queueCursor(op diffSessionOp) {
	for {
		select {
		case b.cursors <- op:
			return
		case <-b.stop:
			return
		default:
		}
		select {
		case <-b.cursors:
		case <-b.stop:
			return
		default:
		}
	}
}

// queueMark hands a read mark to the sender. It never evicts, because there is nothing to
// replace a mark with - see diffBridge.
//
// A full queue is waited on rather than dropped, and the wait is bounded at diffBridgeWrite
// because an unbounded one would DEADLOCK the quit path: close runs after the key loop returns,
// so a key loop parked in here is one that can never reach the drain that would empty the queue.
//
// Past that wait the mark is lost and there is no honest floor below it on this path: the daemon
// owns the session file, so writing the local store instead would be a second writer persisting
// its own idea of the whole set, and stderr belongs to the viewport until the reader quits.
func (b *diffBridge) queueMark(op diffSessionOp) {
	select {
	case b.marks <- op:
		return
	default:
	}
	t := time.NewTimer(diffBridgeWrite)
	defer t.Stop()
	select {
	case b.marks <- op:
	case <-b.stop:
	case <-t.C:
	}
}

// deliver posts queued mutations one at a time, in order, until close stops it - then drains
// what is left behind. It is the only receiver on either queue and it returns on every path: the
// loop leaves on stop, and the drain is bounded by the depth it measured on entry.
func (b *diffBridge) deliver(ctx context.Context) {
	defer close(b.done)
	for {
		select {
		case op := <-b.marks:
			b.post(ctx, op)
		case op := <-b.cursors:
			b.post(ctx, op)
		case <-b.stop:
			b.drain(ctx)
			return
		}
	}
}

// drain posts what was waiting when close ran, marks first because those are the writes that
// cannot be reconstructed, and never more than were queued at that moment - a send racing close
// is not worth extending the shell's exit for.
func (b *diffBridge) drain(ctx context.Context) {
	for n := len(b.marks) + len(b.cursors); n > 0; n-- {
		select {
		case op := <-b.marks:
			b.post(ctx, op)
			continue
		default:
		}
		select {
		case op := <-b.cursors:
			b.post(ctx, op)
		default:
			return
		}
	}
}

// close stops the sender and gives what is already queued a bounded chance to leave: a mark
// made on the last keypress should not be lost to the process exiting, and a daemon that has
// stopped answering should not hold the shell either.
//
// Spending the budget CANCELS the sender rather than just abandoning it, which is what aborts
// the post already on the wire. Left uncancelled, the goroutine went on talking to the daemon
// about a session the reader had walked away from, with nothing left to receive the answer.
func (b *diffBridge) close() {
	defer b.cancel() // the drain finished inside its budget; nothing is on the wire to abort
	close(b.stop)
	select {
	case <-b.done:
		return
	case <-time.After(b.drainBudget()):
	}
	b.cancel()
	select {
	case <-b.done:
	case <-time.After(diffBridgeWrite):
	}
}

// drainBudget is how long close waits for the queue: one write deadline per op, since they leave
// one at a time, plus one for whatever is already on the wire, capped at diffBridgeClose.
func (b *diffBridge) drainBudget() time.Duration {
	d := time.Duration(len(b.marks)+len(b.cursors)+1) * diffBridgeWrite
	if d > diffBridgeClose {
		return diffBridgeClose
	}
	return d
}

// post sends one mutation, best-effort. A coordination write that fails is a pairing that
// went quiet, not a review that has to stop - and there is nothing useful to say about it to
// somebody in the middle of reading a diff.
//
// ctx is the SENDER's, never the session's: a cancelled read must not turn the last write into a
// lost mark. close cancels it once the drain budget is spent, so a post outlives the process's
// interest in it by nothing.
func (b *diffBridge) post(ctx context.Context, op diffSessionOp) {
	body, err := json.Marshal(op)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, diffBridgeWrite)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+b.addr+"/api/v1/diff/session", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	// Drained so the connection can be reused for the next keypress rather than torn down.
	_, _ = io.Copy(io.Discard, resp.Body)
}

func diffUsage(w *os.File) {
	fmt.Fprintln(w, "Usage: magus diff [--generated] [--impact] [flags]")
	fmt.Fprintln(w, "       magus diff --ack [<changed-path>...]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Read the working tree's uncommitted changes, ordered by what they can break.")
	fmt.Fprintln(w, "It takes no ref: the subject is always the uncommitted tree.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Generated files - declared target outputs - are folded away: reading one is")
	fmt.Fprintln(w, "reading a machine's restatement of a change made elsewhere, so the source edit")
	fmt.Fprintln(w, "is what to read.")
	fmt.Fprintln(w, "")
	// Name the ranking key exactly, and name what is NOT one. Only reach ranks; listing public
	// surface and coverage as "the evidence behind its rank" has a reader who sees a hot file
	// sitting eighth conclude the ranking weighed churn and dismissed it. Printing a number
	// beside a rank it did not earn teaches the wrong model.
	fmt.Fprintln(w, "The order is: declared outputs last, then the widest reach first - how many")
	fmt.Fprintln(w, "files reference the most-referenced symbol the file changed. Reach needs a")
	fmt.Fprintln(w, "symbol index; without one there is no ranking key at all, and diff says so")
	fmt.Fprintln(w, "at the top and falls back to path order rather than implying a ranking.")
	fmt.Fprintln(w, "Build the index with `magus graph build`.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Public surface, coverage, churn, and the agent trail are CONTEXT printed")
	fmt.Fprintln(w, "beside each file. None of them is a sort key.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "--ack records that you have read files, at the content they hold now, so")
	fmt.Fprintln(w, "--impact can tell you what changed after you read it. Name the paths you read")
	fmt.Fprintln(w, "to record just those - read them in whatever editor or pager you already")
	fmt.Fprintln(w, "use - or pass none to cover the whole changeset. Stepping a file through in")
	fmt.Fprintln(w, "the viewer records it too. Editing a file afterwards voids its receipt.")
	fmt.Fprintln(w, "")
	// State the cutoff rather than leaving a missing rank ambiguous.
	fmt.Fprintf(w, "A hotspot rank is shown only inside the workspace's top %d. A file that reports\n", types.NotableRankCutoff)
	fmt.Fprintln(w, "a commit count and no rank was measured and sits outside that cutoff; a file with")
	fmt.Fprintln(w, "no history line at all is one magus has never seen change.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --generated   include the folded declared outputs")
	fmt.Fprintln(w, "  --no-tui      print the report instead of opening the viewer. At a")
	fmt.Fprintln(w, "                terminal the viewer opens by default, joined to the session")
	fmt.Fprintln(w, "                the console and an agent share: ] and [ walk hunks, v marks")
	fmt.Fprintln(w, "                one read, q leaves. Anywhere it cannot draw it stands aside")
	fmt.Fprintln(w, "                on its own, so a script needs no flag.")
	fmt.Fprintln(w, "  --impact      append the blast radius of landing this: which projects")
	fmt.Fprintln(w, "                rebuild, who owns them, an estimate from recorded run times,")
	fmt.Fprintln(w, "                what the advisors say, and which notes anchor what you")
	fmt.Fprintln(w, "                changed. It gates nothing and changes no exit code.")
	fmt.Fprintln(w, "  --ack         record that you have read the named changed files, or all of")
	fmt.Fprintln(w, "                them when you name none. Needs a terminal.")
	fmt.Fprintln(w, "  --reason      an optional note kept with an --ack")
	fmt.Fprintln(w, "  --watch       re-read and re-render whenever the working tree changes")
}

// diffHistoryCommits bounds the git-log walk the churn lenses do. 500 matches what the
// daemon's insight scan uses, so the CLI and the console rank the same files the same way -
// two different windows would report two different "hottest file" answers for one tree.
const diffHistoryCommits = 500

// diffReplayEvents bounds the trail walk. Each event costs a small blob read, and a reader
// asking "what was this agent looking at" is asking about recent work by construction.
const diffReplayEvents = 2000

// diffTouches adapts the trail's Touch to the diff's - a rename across a boundary types
// must not cross, since types imports nothing internal and the trail is internal.
func diffTouches(root, cacheDir string, paths []string) map[string][]types.DiffTouch {
	raw := trail.Replay(root, cacheDir, paths, diffReplayEvents)
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string][]types.DiffTouch, len(raw))
	for path, touches := range raw {
		conv := make([]types.DiffTouch, 0, len(touches))
		for _, t := range touches {
			conv = append(conv, types.DiffTouch{
				Host: t.Host, Session: t.Session, Transcript: t.Transcript, Read: t.Read, Ran: t.Ran,
			})
		}
		out[path] = conv
	}
	return out
}

// diffCountsLine is the headline every rendering of a changeset opens with: how much there
// is to read before any of it is shown.
//
// The interactive reader leaves this same line behind when it quits, which is why it is a
// string rather than a print: a session that erased its viewport and printed nothing would
// leave the scrollback with no record that a review happened at all.
// The fold state is threaded rather than assumed: under --generated every generated file is
// printed right below this line, so calling them folded would contradict the page it introduces.
func diffCountsLine(rev types.Diff, showGenerated bool) string {
	gen := rev.GeneratedCount()
	line := fmt.Sprintf("%d files to read", len(rev.Files)-gen)
	if gen > 0 {
		state := "folded"
		if showGenerated {
			state = "shown"
		}
		line += fmt.Sprintf(", %d generated %s", gen, state)
	}
	if n := len(rev.SeedProjects); n > 0 {
		// Both halves name the noun: a bare "N rebuild" leaves the reader guessing what is
		// being counted.
		line += fmt.Sprintf("; %d projects edited, %d projects rebuild", n, len(rev.AffectedProjects))
	}
	return line
}

// printDiffText renders the diff in the house style: counts before lists, the evidence
// beside the claim, plain ASCII.
//
// pre is nil unless --impact was passed, and nil prints nothing at all: the report above
// it must not change shape because a second one was appended.
func printDiffText(rev types.Diff, showGenerated bool, link func(string) string, pre *diffImpact) error {
	var primary, generated []types.DiffFile
	for _, f := range rev.Files {
		if f.Generated() {
			generated = append(generated, f)
		} else {
			primary = append(primary, f)
		}
	}

	fmt.Println(diffCountsLine(rev, showGenerated))
	fmt.Println()

	// The ordering caveat prints BEFORE the list, and only this placement works. As a trailing
	// note it arrived after the reader had already read the first entry as the most dangerous
	// one, and it named the missing overlays rather than the missing order - so three separate
	// readers concluded the ranking had considered churn and rejected it. Say the one thing
	// that changes how the next twelve lines should be read, first.
	if !rev.Ranked() && len(primary) > 1 {
		fmt.Println("UNRANKED: no symbol index, so there is no consequence to rank by.")
		fmt.Println("What follows is path order, not a ranking. Build the index with")
		fmt.Println("`magus graph build` to order these by what they can break.")
		fmt.Println()
	}

	for _, f := range primary {
		printDiffFile(f, link)
	}

	if len(generated) > 0 {
		fmt.Println()
		// Both branches name `magus describe file`, because "why is this folded" is the one
		// question the fold provokes and the answer was a hop out of reach: the folded list
		// said THAT a target rewrites these and never WHICH, so a reader who suspected a
		// mis-declared glob had nowhere to go.
		if showGenerated {
			fmt.Printf("generated (%d) - a target rewrites these; the source edit is what to read\n", len(generated))
			for _, f := range generated {
				printDiffFile(f, link)
			}
			fmt.Println("      why is one of these folded? `magus describe file <path>` names the project that declares it")
		} else {
			fmt.Printf("%d generated files folded. They are declared target outputs: reading one is\n", len(generated))
			fmt.Println("reading a machine's restatement of a change made elsewhere. Show them with --generated,")
			fmt.Println("or ask why one is folded with `magus describe file <path>`.")
		}
	}

	// Notes name what could NOT be measured. Surfaced rather than swallowed, so an empty
	// column reads as "nothing was measured" rather than as "nothing depends on this".
	for _, n := range rev.Notes {
		fmt.Printf("\nnote: %s\n", n)
	}

	// Above the console link rather than below it: the link is a trailing pointer somewhere
	// else, and burying it under forty lines of report would cost it every reader it has.
	if pre != nil {
		fmt.Println()
		for _, line := range impactLines(*pre) {
			fmt.Println(line)
		}
	}

	// What to do next, and where to see the same changeset. Both are for a person: the console
	// link carries no token (see printJobWatchHint), so the terminal check is an invitation
	// rather than a secrecy measure, and it keeps `magus diff > file` as data.
	if tty.IsTerminalWriter(os.Stdout, tty.SystemProbe) {
		for _, line := range diffNextStepLines(len(primary)) {
			fmt.Println(line)
		}
		if u := consoleDiffURL(); u != "" {
			fmt.Printf("open in console: %s\n%s\n", u, authHint)
		}
	}
	return nil
}

// diffNextStepLines names what a reader can do with the changeset they were just shown.
//
// This output is where most people meet magus's review workflow, so `--tui` and `--impact` are
// named here rather than only in `-h` prose and the man page.
//
// Nothing when nothing was listed: a clean tree or an all-generated changeset has no reading
// to offer, and a suggestion there is a suggestion to do nothing.
func diffNextStepLines(readable int) []string {
	if readable == 0 {
		return nil
	}
	// At a terminal the viewer has already opened, so what is left to teach is the two ways out
	// of it: this report, and the impact question the viewer has nowhere to put.
	return []string{
		"",
		"the blast radius:  magus diff --impact",
		"just the report:   magus diff --no-tui",
	}
}

func printDiffFile(f types.DiffFile, link func(string) string) {
	fmt.Printf("  %s\n", link(f.Path))
	for _, fact := range diffFileFacts(f) {
		fmt.Printf("      %s\n", fact)
	}
	// The story, last: it is the deepest context and the least urgent. A reader scanning for
	// risk should hit reach and coverage first and find the narrative when they stop to read.
	for _, t := range f.Touches {
		who := t.Host
		if who == "" {
			who = "an agent"
		}
		fmt.Printf("      written by %s", who)
		if len(t.Read) > 0 {
			fmt.Printf(", after reading %s", strings.Join(capSlice(t.Read, 4), ", "))
		}
		fmt.Println()
		if t.Transcript != "" {
			fmt.Printf("        transcript: %s\n", t.Transcript)
		}
	}
}

// diffFileFacts is what magus knows about one changed file, one claim per line.
//
// One definition for the printer and the interactive reader, so both show the SAME sentences:
// two renderings of "12 files reference its widest changed symbol" would drift, and the drift
// would be invisible until somebody compared two surfaces side by side.
func diffFileFacts(f types.DiffFile) []string {
	var facts []string
	if f.Surface == types.DiffSurfacePublic {
		var api []string
		var across []string
		seen := map[string]bool{}
		for _, s := range f.Symbols {
			if s.ModuleAPI && s.Label != "" {
				api = append(api, s.Label)
			}
			for _, p := range s.ExternalProjects {
				if !seen[p] {
					seen[p] = true
					across = append(across, p)
				}
			}
		}
		switch {
		case len(api) > 0:
			facts = append(facts, "PUBLIC SURFACE: exports "+strings.Join(capSlice(api, 6), ", "))
		case len(across) > 0:
			facts = append(facts, "PUBLIC SURFACE: used by "+strings.Join(across, ", "))
		default:
			facts = append(facts, "PUBLIC SURFACE")
		}
	}
	if n := f.ReachOr(0); f.Reach != nil && n > 0 {
		noun := "files"
		if n == 1 {
			noun = "file"
		}
		facts = append(facts, fmt.Sprintf("%d %s reference its widest changed symbol", n, noun))
	}
	if c := f.Coverage; c != nil && c.Total > 0 {
		facts = append(facts, fmt.Sprintf("%d%% covered", int(c.Ratio*100+0.5)))
	}
	// Said plainly, because an empty annotation row otherwise reads as "quiet file" when it
	// means "magus has never seen this file before".
	if f.NoHistory {
		facts = append(facts, "NO HISTORY - nothing has exercised this yet")
	}
	if ch := f.Churn; ch != nil && ch.Commits > 0 {
		noun := "commits"
		if ch.Commits == 1 {
			noun = "commit"
		}
		churn := fmt.Sprintf("changed in %d %s", ch.Commits, noun)
		if ch.Authors > 1 {
			churn += fmt.Sprintf(" by %d people", ch.Authors)
		}
		if ch.NotableRank() {
			churn += fmt.Sprintf(", hotspot #%d", ch.Rank)
		}
		if ch.Rising() {
			churn += " AND RISING - worth asking why it keeps changing"
		}
		facts = append(facts, churn)
	}
	if f.Project != "" {
		facts = append(facts, "in "+f.Project)
	}
	return facts
}

// capSlice bounds a list for display, reporting the remainder rather than truncating in
// silence - a list that stops without saying so reads as the whole answer.
func capSlice(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(xs[:n:n], fmt.Sprintf("and %d more", len(xs)-n))
}

// changedPathsFromPatch reads the `diff --git` headers out of a unified patch.
//
// The paths come from the PATCH rather than from a second VCS call on purpose: a re-derived
// set would race an edit made since the patch was read, and annotate a file the reader is not
// looking at. The console makes the same choice for the same reason.
// diffReport is the changeset plus its impact, the document --impact serves on the
// structured path.
//
// It exists ONLY under the flag. Without it the emitted value is types.Diff exactly as
// before, so nothing reading `magus diff -o json` today meets a key it has never seen.
type diffReport struct {
	types.Diff `yaml:",inline"`
	Impact     *diffImpact `json:"impact,omitempty" yaml:"impact,omitempty"`
}

// diffImpact is what a disposer should know before landing: the consequences a file list
// structurally cannot show.
//
// Every field distinguishes "nothing" from "nobody measured", the same refusal types.DiffFile
// makes with its pointers. An absent cost is a workspace with no run history, and rendering it
// as zero would tell a reader the build is free.
type diffImpact struct {
	Reach     *impactReach    `json:"reach,omitempty"     yaml:"reach,omitempty"`
	Ownership []impactOwner   `json:"ownership,omitempty" yaml:"ownership,omitempty"`
	Cost      *impactCost     `json:"cost,omitempty"      yaml:"cost,omitempty"`
	Advisors  []adviceSection `json:"advisors,omitempty"  yaml:"advisors,omitempty"`
	// AdvisorNotes names each advisor that could not run. Surfaced rather than swallowed, so
	// an empty advisor list reads as "they all passed" only when it is one.
	AdvisorNotes []string `json:"advisor_notes,omitempty" yaml:"advisor_notes,omitempty"`
	// AdvisorBase qualifies everything the advisors said. nil when the backend cannot date a
	// revision, which is a different fact from a base that is merely old.
	AdvisorBase *impactAdvisorBase `json:"advisor_base,omitempty" yaml:"advisor_base,omitempty"`
	Anchors     []anchorHit        `json:"anchors,omitempty"      yaml:"anchors,omitempty"`
	Rationale   []rationaleHit     `json:"rationale,omitempty"    yaml:"rationale,omitempty"`
	Review      *impactReview      `json:"review,omitempty"       yaml:"review,omitempty"`
}

// impactAdvisorBase is the revision the advisors compared against, and how current this
// clone's copy of it is.
//
// It is stated ONCE for the whole set rather than per section: a local run never fetches, so
// the caveat is identical under every advisor, and repeating it ten times is how a reader
// learns to skip it.
type impactAdvisorBase struct {
	// Ref is the revision as the advisors spell it, so a reader can run the same comparison
	// by hand.
	Ref string `json:"ref" yaml:"ref"`
	// Tip is when Ref's commit was authored, RFC3339, and empty when Ref names nothing in
	// this clone. The AGE a reader sees is derived from this at render rather than stored:
	// a report teed to a file and read tomorrow must not still claim today's number.
	Tip string `json:"tip,omitempty" yaml:"tip,omitempty"`
}

// impactReach is the blast radius: what was edited, and what rebuilds because of it.
type impactReach struct {
	Seeds    int             `json:"seeds"              yaml:"seeds"`
	Rebuilds int             `json:"rebuilds"           yaml:"rebuilds"`
	Projects []impactProject `json:"projects,omitempty" yaml:"projects,omitempty"`
}

type impactProject struct {
	Path  string `json:"path"            yaml:"path"`
	Seed  bool   `json:"seed"            yaml:"seed"`
	Files int    `json:"files,omitempty" yaml:"files,omitempty"`
}

// impactOwner is one project in reach and who has been changing it.
type impactOwner struct {
	Project      string `json:"project"                yaml:"project"`
	Primary      string `json:"primary"                yaml:"primary"`
	PrimaryShare int    `json:"primary_share"          yaml:"primary_share"` // percent
	Authors      int    `json:"authors"                yaml:"authors"`
	BusFactor1   bool   `json:"bus_factor_1,omitempty" yaml:"bus_factor_1,omitempty"`
}

// impactCost is the estimated cost of rebuilding the reach, from recorded run durations.
type impactCost struct {
	TotalMs  int64               `json:"total_ms"           yaml:"total_ms"`
	Projects []impactCostProject `json:"projects,omitempty" yaml:"projects,omitempty"`
}

type impactCostProject struct {
	Project string `json:"project" yaml:"project"`
	Target  string `json:"target"  yaml:"target"`
	Ms      int64  `json:"ms"      yaml:"ms"`
	// Samples is how many runs the estimate rests on, carried so a reader can weigh it. An
	// estimate from three runs and one from three hundred print the same duration otherwise.
	Samples int     `json:"samples"           yaml:"samples"`
	HitRate float64 `json:"hit_rate,omitempty" yaml:"hit_rate,omitempty"`
}

// impactCostTargets is the target a project's rebuild is estimated by, most representative
// first. ci is the whole gate; test is the bulk of it where no ci target is declared.
var impactCostTargets = []string{"ci", "test"}

// impactMinSamples mirrors the tier-3 gate inside forecast.resolvePrediction: below it the
// prediction is the workspace fallback rather than a measurement of this project, and printing
// that as an estimate would be inventing a number.
const impactMinSamples = 3

// impactListCap bounds each section's list. A reach of sixty projects is a real answer and
// an unreadable one; the remainder is reported rather than truncated in silence.
const impactListCap = 10

// collectImpact joins the lenses the report needs onto an already-annotated changeset.
//
// Every lens is best-effort and every failure degrades to that lens's empty form. A impact
// that refuses to print because the symbol index is cold or no daemon is running is a
// impact nobody runs, and this surface reports context rather than passing judgement.
func collectImpact(ctx context.Context, m *magus.Magus, rootOverride string, rev types.Diff) diffImpact {
	p := diffImpact{Reach: impactReachOf(rev)}

	// The same bounded git-log walk annotateDiff pays for the churn lenses, so the two
	// sections of one report cannot describe two different windows of history.
	if own, err := m.Ownership(ctx, types.InsightOptions{Commits: diffHistoryCommits}); err == nil {
		p.Ownership = impactOwnersOf(own, rev.AffectedProjects)
	}

	if path := globalCfg.HistoryPath; path != "" {
		var h forecast.History
		if err := h.Load(ctx, path); err == nil {
			p.Cost = impactCostOf(&h, rev.AffectedProjects)
		}
	}

	// The configured VCS base ref, never rev.Base: the working diff's base is a STATE
	// label ("working"), and the advisors compare git revisions (`origin/<base>`), so the
	// label would send them fetching a branch that does not exist. Their section
	// therefore describes committed state against the base branch - the most a rev-based
	// advisor can measure, per the documented limit on runLocalAdvisors.
	base := m.VCSOptions().BaseRef
	if base == "" {
		base = "main"
	}
	sections, failed, err := runLocalAdvisors(ctx, m, base)
	p.Advisors, p.AdvisorNotes = sections, failed
	if err != nil {
		p.AdvisorNotes = append(p.AdvisorNotes, fmt.Sprintf("advisors did not run: %v", err))
	}
	p.AdvisorBase = impactAdvisorBaseOf(ctx, m, base)

	// rootOverride, not m.Root(): the workspace loaders are once-per-process and keyed on
	// the override they were first handed, so the anchors' graph load must spell the root
	// exactly as diffCmd's own load did.
	p.Anchors = impactAnchors(ctx, rootOverride, diffPaths(rev), diffSymbolIDs(rev))
	p.Rationale = collectRationale(m.Root(), rev)
	var requiredIn func(string) bool
	if ws, werr := inspectWorkspace(ctx, rootOverride); werr == nil {
		requiredIn = reviewRequiredMatcher(ws)
	}
	p.Review = collectReview(rev, requiredIn, bulkReasons(m.CacheDir(), rev))
	return p
}

// impactReachOf renders what types.Diff has carried since the impact join landed and
// nothing has ever printed: which projects were edited, and which merely rebuild.
//
// nil when the closure is empty, which is a real state - a change entirely outside every
// project directory seeds nothing.
func impactReachOf(rev types.Diff) *impactReach {
	if len(rev.AffectedProjects) == 0 {
		return nil
	}
	r := &impactReach{Seeds: len(rev.SeedProjects), Rebuilds: len(rev.AffectedProjects)}
	for _, p := range rev.AffectedProjects {
		r.Projects = append(r.Projects, impactProject{Path: p.Path, Seed: p.Seed, Files: len(p.Files)})
	}
	return r
}

// impactOwnersOf joins the ownership lens onto the reach, in reach order.
//
// Projects the lens has nothing to say about are DROPPED rather than listed with an empty
// author: a project with no commits in the window has no owner to name, and a blank name
// beside a path reads as a lookup that failed.
func impactOwnersOf(own types.OwnershipOutput, affected []types.ImpactProject) []impactOwner {
	byPath := make(map[string]types.OwnershipEntry, len(own.Projects))
	for _, e := range own.Projects {
		byPath[e.Path] = e
	}
	var out []impactOwner
	for _, p := range affected {
		e, ok := byPath[p.Path]
		if !ok || e.Primary == "" {
			continue
		}
		out = append(out, impactOwner{
			Project:      p.Path,
			Primary:      e.Primary,
			PrimaryShare: e.PrimaryShare,
			Authors:      e.Authors,
			BusFactor1:   e.BusFactor1,
		})
	}
	return out
}

// impactCostOf sums the recorded history's per-project prediction over the reach.
//
// nil when the history has nothing to say about ANY project in reach. That case matters more
// than the happy one: forecast falls back to a workspace-wide default and then to a compiled-in
// constant, so a total is always computable and a total computed from those is a fabrication
// wearing a duration. Only projects with enough samples for forecast's own project tier are
// counted, and a reach whose projects all fall short reports no history at all.
func impactCostOf(h *forecast.History, affected []types.ImpactProject) *impactCost {
	c := &impactCost{}
	for _, p := range affected {
		target, stats, ok := impactCostTarget(h, p)
		if !ok {
			continue
		}
		// Tags are derived from the changed files inside the project, which is what selects
		// forecast's per-subdirectory bucket over the project-wide percentile. A transitively
		// affected project has none, and Tags answers "transitive" for it.
		d := h.PredictDuration(p.Path, target, forecast.Tags(p.Path, p.Files))
		c.TotalMs += d.Milliseconds()
		c.Projects = append(c.Projects, impactCostProject{
			Project: p.Path,
			Target:  target,
			Ms:      d.Milliseconds(),
			Samples: stats.Samples,
			HitRate: stats.HitRate,
		})
	}
	if len(c.Projects) == 0 {
		return nil
	}
	return c
}

// impactCostTarget picks the target a project's rebuild is estimated by, and reports false
// when the history has no measurement of this project worth quoting.
func impactCostTarget(h *forecast.History, p types.ImpactProject) (string, forecast.Stats, bool) {
	targets, ok := h.Projects[p.Path]
	if !ok {
		return "", forecast.Stats{}, false
	}
	for _, name := range impactCostTargets {
		s, timed := targets[name]
		if timed && s.Samples >= impactMinSamples && s.P75Ms > 0 {
			return name, s, true
		}
	}
	return "", forecast.Stats{}, false
}

// diffNotes reads every declared notes store, best-effort.
//
// A workspace that declares none is the DEFAULT rather than a fault, and an unreadable store
// is already reported by `magus notes verify`; either way the anchors section says it found
// nothing rather than failing the report around it.
// diffPaths is the changed file set, generated files included: a note may anchor one.
func diffPaths(rev types.Diff) []string {
	out := make([]string, 0, len(rev.Files))
	for _, f := range rev.Files {
		out = append(out, f.Path)
	}
	return out
}

// diffSymbolIDs is every changed symbol's index id, which is what a symbol anchor names.
func diffSymbolIDs(rev types.Diff) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range rev.Files {
		for _, s := range f.Symbols {
			if s.ID != "" && !seen[s.ID] {
				seen[s.ID] = true
				out = append(out, s.ID)
			}
		}
	}
	return out
}

// impactLines renders the report, one claim per line, in the same count-then-list shape
// the file list above it uses.
//
// Lines rather than prints, so every empty form is testable without a terminal - and the empty
// forms are the half that matters. Each one says what was not measured and what would measure
// it, because a silent section reads as a clean bill of health.
func impactLines(p diffImpact) []string {
	out := []string{"IMPACT - the blast radius of landing this", ""}
	sections := [][]string{
		impactReachLines(p.Reach),
		impactOwnershipLines(p.Ownership),
		impactCostLines(p.Cost),
		impactAdvisorLines(p.Advisors, p.AdvisorNotes, p.AdvisorBase),
		impactAnchorLines(p.Anchors),
		impactRationaleLines(p.Rationale),
		impactReviewLines(p.Review),
	}
	for _, s := range sections {
		// A section may render nothing - REVIEW says nothing about a small change nobody
		// has disturbed. Skipping it here rather than emitting its blank separator is what
		// keeps that silence from reading as a section that broke.
		if len(s) == 0 {
			continue
		}
		if len(out) > 2 {
			out = append(out, "")
		}
		out = append(out, s...)
	}
	return out
}

func impactReachLines(r *impactReach) []string {
	if r == nil {
		return []string{"REACH: no project contains a changed file, so nothing rebuilds"}
	}
	out := []string{fmt.Sprintf("REACH: %d project%s edited, %d project%s rebuild",
		r.Seeds, pluralSuffix(r.Seeds, "", "s"), r.Rebuilds, pluralSuffix(r.Rebuilds, "", "s"))}
	for _, p := range impactCap(r.Projects) {
		if p.Seed {
			out = append(out, fmt.Sprintf("      %s - edited, %d file%s", p.Path, p.Files, pluralSuffix(p.Files, "", "s")))
			continue
		}
		out = append(out, fmt.Sprintf("      %s - rebuilds because it depends on one that was", p.Path))
	}
	return append(out, impactMoreLine(len(r.Projects))...)
}

func impactOwnershipLines(owners []impactOwner) []string {
	if len(owners) == 0 {
		return []string{"OWNERSHIP: no commit history in the window, so no owner is named"}
	}
	out := []string{"OWNERSHIP: who has been changing the projects in reach"}
	for _, o := range impactCap(owners) {
		line := fmt.Sprintf("      %s mostly %s (%d%%), %d author%s",
			o.Project, o.Primary, o.PrimaryShare, o.Authors, pluralSuffix(o.Authors, "", "s"))
		if o.BusFactor1 {
			// The bus factor is the whole point of the lens, so it is stated rather than left
			// to be inferred from "1 author".
			line += " - BUS FACTOR 1"
		}
		out = append(out, line)
	}
	return append(out, impactMoreLine(len(owners))...)
}

func impactCostLines(c *impactCost) []string {
	if c == nil {
		return []string{
			"COST: no run history yet, so there is nothing to estimate from",
			"      Run `magus affected ci` once and the next impact can price this.",
		}
	}
	out := []string{fmt.Sprintf(
		"COST: ~%s to rebuild the reach (history-based estimate: the p75 of past runs, discounted by the cache hit rate they recorded)",
		impactDuration(c.TotalMs))}
	for _, p := range impactCap(c.Projects) {
		line := fmt.Sprintf("      %s %s ~%s (%d run%s)",
			p.Project, p.Target, impactDuration(p.Ms), p.Samples, pluralSuffix(p.Samples, "", "s"))
		if p.HitRate > 0 {
			line += fmt.Sprintf(", %d%% cache hits", int(p.HitRate*100+0.5))
		}
		out = append(out, line)
	}
	return append(out, impactMoreLine(len(c.Projects))...)
}

// impactAdvisorBaseOf dates this clone's copy of the ref the advisors compared against.
//
// nil whenever the answer would be a guess: no VCS, or a backend that cannot date a
// revision. A ref the clone does not HAVE is not that case - it resolves to a
// impactAdvisorBase with no Tip, because "you have never fetched this" is the single most
// useful thing the report can say about why nine advisors went quiet.
func impactAdvisorBaseOf(ctx context.Context, m *magus.Magus, base string) *impactAdvisorBase {
	res, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions())
	if err != nil || res.VCS == nil {
		return nil
	}
	timer, ok := res.VCS.(types.RevTimeReporter)
	if !ok {
		return nil
	}
	// "origin/" + base is the spelling the advisors themselves use, not a normalization of
	// it: the line exists so a reader can run the same comparison by hand, and a ref that
	// reads differently here than in advice.buzz would send them at the wrong one.
	ref := "origin/" + base
	tip, found, err := timer.RevTime(ctx, m.Root(), ref)
	if err != nil {
		return nil
	}
	if !found {
		return &impactAdvisorBase{Ref: ref}
	}
	return &impactAdvisorBase{Ref: ref, Tip: tip.Format(time.RFC3339)}
}

// impactAdvisorBaseLine states what the advisors measured against, and how old it is.
//
// The age is computed here rather than carried, so it describes when the report is READ.
// An unparsable Tip degrades to naming the ref: the ref alone is still true, and a
// malformed date is not worth losing the rest of the line over.
func impactAdvisorBaseLine(b *impactAdvisorBase) string {
	if b == nil {
		return ""
	}
	refresh := "`git fetch origin " + strings.TrimPrefix(b.Ref, "origin/") + "` brings it forward"
	if b.Tip == "" {
		return fmt.Sprintf("BASE: %s is not in this clone, so the advisors below had nothing to "+
			"compare against; %s", b.Ref, refresh)
	}
	tip, err := time.Parse(time.RFC3339, b.Tip)
	if err != nil {
		return fmt.Sprintf("BASE: %s, as this clone has it - a local run stays off the network; %s", b.Ref, refresh)
	}
	return fmt.Sprintf("BASE: %s, tip %s old - a local run stays off the network, so anything "+
		"merged since is outside what the advisors saw; %s",
		b.Ref, impactAge(time.Since(tip)), refresh)
}

// impactAge renders a duration at one unit of precision. A reader deciding whether to
// fetch needs the order of magnitude ("3 days"), and "3 days 4 hours 11 minutes" buries it.
func impactAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "seconds"
	case d < time.Hour:
		return fmt.Sprintf("%d minute%s", int(d.Minutes()), pluralSuffix(int(d.Minutes()), "", "s"))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hour%s", int(d.Hours()), pluralSuffix(int(d.Hours()), "", "s"))
	default:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%d day%s", days, pluralSuffix(days, "", "s"))
	}
}

func impactAdvisorLines(sections []adviceSection, failed []string, base *impactAdvisorBase) []string {
	// Prepended to whichever headline follows, including "nothing to report": a clean set
	// measured against a ref from last week is the case where the caveat matters MOST, and
	// hanging it off a finding count would drop it exactly there.
	var out []string
	if line := impactAdvisorBaseLine(base); line != "" {
		out = append(out, line)
	}
	if len(sections) == 0 && len(failed) == 0 {
		return append(out, "ADVISORS: nothing to report")
	}
	// An empty Body is a RETRACTION - that advisor ran and found nothing - so it is not a
	// finding, and counting sections rather than findings reported ten of them where there
	// was one, each rendered as a title with nothing underneath. A reader who learns the
	// headline overstates by an order of magnitude stops reading the section.
	var found []adviceSection
	var clear []string
	for _, s := range sections {
		if strings.TrimRight(s.Body, "\n") == "" {
			clear = append(clear, s.Name)
			continue
		}
		found = append(found, s)
	}
	out = append(out, fmt.Sprintf("ADVISORS: %d finding%s", len(found), pluralSuffix(len(found), "", "s")))
	for _, s := range found {
		out = append(out, "      "+s.Title)
		for _, line := range strings.Split(strings.TrimRight(s.Body, "\n"), "\n") {
			out = append(out, "        "+line)
		}
	}
	// Named, not just counted, and kept apart from the notes below: "ran and found nothing"
	// and "could not run" are the two ways a section goes quiet, and only one of them is
	// news about the change.
	if len(clear) > 0 {
		out = append(out, fmt.Sprintf("      %d ran and found nothing: %s",
			len(clear), strings.Join(clear, ", ")))
	}
	// Named rather than counted: which advisor went quiet is what a reader needs to decide
	// whether the silence above it means anything. Printed verbatim - each note already
	// says whether it is a warning or a "could not run" failure, and only collectAdvice
	// can tell the two apart.
	for _, f := range failed {
		out = append(out, "      "+f)
	}
	return out
}

func impactAnchorLines(hits []anchorHit) []string {
	if len(hits) == 0 {
		return []string{"ANCHORS: no note anchors a changed file or symbol"}
	}
	out := []string{fmt.Sprintf("ANCHORS: %d note%s anchored to what you changed",
		len(hits), pluralSuffix(len(hits), "", "s"))}
	for _, h := range impactCap(hits) {
		line := fmt.Sprintf("      note %s anchors %s:%s", h.Note, h.Kind, h.Target)
		if h.Drift != "" {
			// An unmeasured anchor is marked too, and deliberately not with its wire code:
			// rendered as "ungraded-anchor" it scans as a fourth kind of drift verdict, when
			// what it says is that no verdict was reached. Bare would be worse - that reads
			// as clean, which is the one thing nobody checked it for.
			marker := h.Drift
			if h.Drift == string(notes.StatusUngraded) {
				marker = "ungraded"
			}
			line += " [" + marker + "]"
		}
		out = append(out, line)
	}
	return append(out, impactMoreLine(len(hits))...)
}

// impactCap bounds one section's list; impactMoreLine reports what it left off. They
// are separate because the remainder is a line in the section's own indentation, not an entry
// in the list it describes.
func impactCap[T any](xs []T) []T {
	if len(xs) <= impactListCap {
		return xs
	}
	return xs[:impactListCap]
}

func impactMoreLine(n int) []string {
	if n <= impactListCap {
		return nil
	}
	return []string{fmt.Sprintf("      and %d more", n-impactListCap)}
}

// impactDuration renders an estimate at the precision it deserves. Seconds is the floor:
// this is a p75 of past runs, and a millisecond figure would claim an accuracy it has not got.
func impactDuration(ms int64) string {
	d := (time.Duration(ms) * time.Millisecond).Round(time.Second)
	if d < time.Second {
		return "<1s"
	}
	return d.String()
}

// changedPathsFromPatch lists the files a patch touches, in patch order.
//
// It defers to the session parser rather than reading headers itself. This file used to carry
// its own copy, and the copy is how a GNU `diff -u` patch - no `diff --git` line anywhere -
// reported zero changed files and exited 0. Two readers of the same bytes will drift, and when
// they do the annotations describe different files than the hunks a reader is marking.
func changedPathsFromPatch(patch string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range session.ParseHunks(patch) {
		if f.Path != "" && !seen[f.Path] {
			seen[f.Path] = true
			out = append(out, f.Path)
		}
	}
	return out
}

// anchorHit is one note anchor that names something in the changeset, shaped for the
// impact report rather than for the store.
type anchorHit struct {
	Note   string           `json:"note"            yaml:"note"`
	Title  string           `json:"title,omitempty" yaml:"title,omitempty"`
	Kind   notes.AnchorKind `json:"kind"            yaml:"kind"`
	Target string           `json:"target"          yaml:"target"`
	// Drift is the notes.IssueCode this anchor resolved to. Empty is graded CLEAN;
	// notes.StatusUngraded is unmeasured, which grading needs the knowledge graph for and
	// cannot report when the graph will not load. The two are distinct so a renderer cannot
	// show an anchor nobody checked as fresh.
	Drift string `json:"drift,omitempty" yaml:"drift,omitempty"`
}

// impactAnchors joins every note anchor against the changeset. A knowledge graph that will
// not load costs the drift column and nothing else: notes.ResolveAnchors takes a nil resolver
// and grades every anchor ungraded, so the section still answers WHAT is anchored.
func impactAnchors(ctx context.Context, root string, files, symbols []string) []anchorHit {
	stores, err := notesStores(root, "")
	if err != nil {
		return nil
	}
	res, resErr := notesResolver(ctx, root)

	var resolved []notes.ResolvedAnchor
	for _, st := range stores {
		var scoped notes.Resolver
		if resErr == nil {
			scoped = res.ForScope(string(st.scope))
		}
		ra, raErr := notes.ResolveAnchors(ctx, st.dir, scoped)
		if raErr != nil {
			continue
		}
		resolved = append(resolved, ra...)
	}

	hits := notes.AnchorHits(resolved, files, symbols)
	out := make([]anchorHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, anchorHit{
			Note: h.Note, Title: h.Title, Kind: h.Kind, Target: h.Target, Drift: string(h.Status),
		})
	}
	return out
}

// earnedSync watches a viewer session and turns finished files into read receipts.
//
// This is the receipt worth trusting. `--ack` is a claim made about files in bulk and pays
// for that with a reason on the record; this one is minted from what the reader actually
// did - every hunk of the file marked read, one keypress at a time, in a viewer only a
// person can drive. Nothing is inferred: the marks were already explicit, and all this adds
// is that they now outlive the session.
//
// It wraps rather than replaces the underlying sync, because publishing the reader's
// progress to the daemon and recording a durable receipt are different jobs with different
// lifetimes, and the viewer must keep knowing about neither.
type earnedSync struct {
	diffSync
	// content is where a receipt's bytes come from, and what its Source records.
	content        reviewedContent
	root, cacheDir string
	// fileOf maps a hunk digest to the file it belongs to, and hunksOf counts how many a
	// file has. A receipt is per FILE, so a file is earned only once every hunk it
	// contributes is marked - reading four hunks of six is not reading the file.
	fileOf map[string]string
	// hunksOf counts DISTINCT hunk digests per file, for the reason session.Store.TrackHunks
	// gives: counting occurrences sets a total the marked set can never reach.
	hunksOf map[string]int
	// digestAt is each file's content fingerprint as it was when the reader started, taken
	// once here rather than at mint time.
	//
	// A receipt must attest to the bytes somebody SAW; fingerprinting at close would stamp
	// whatever the file holds by then, and the next report would not call it stale. See
	// session.Store.ContentAt.
	digestAt map[string]string
	viewed   map[string]bool
	// live are the hunks marked in THIS session, as opposed to seeded from the store.
	//
	// A file earns a receipt only if at least one of its hunks was marked here. The stored
	// viewed set is a plain unauthenticated JSON file whose hunk digests are computable
	// from `magus diff` output, so anything with write access can forge a complete reading;
	// without this, opening the viewer once would launder that forgery into durable
	// receipts. Requiring a live mark keeps the seed doing its real job - resuming a
	// reading across sittings - while making it worth nothing on its own.
	live map[string]bool
	now  func() time.Time
}

// newEarnedSync wraps sync with receipt minting, seeded with the marks the session already
// carried so a reader who finishes a file across two sittings still earns it.
func newEarnedSync(inner diffSync, content reviewedContent, cacheDir string, files []diff.File, seen []string) *earnedSync {
	e := &earnedSync{
		diffSync: inner,
		content:  content,
		root:     content.root,
		cacheDir: cacheDir,
		fileOf:   map[string]string{},
		hunksOf:  map[string]int{},
		digestAt: map[string]string{},
		viewed:   map[string]bool{},
		live:     map[string]bool{},
		now:      time.Now,
	}
	for _, f := range files {
		// Generated files are excluded for the reason they are folded away by default:
		// reading a machine's restatement of an edit made elsewhere is not the review.
		if f.Generated {
			continue
		}
		for _, h := range f.Hunks {
			if _, seen := e.fileOf[h.Digest]; seen {
				continue // an identical hunk repeated in one file is one mark, not two
			}
			e.fileOf[h.Digest] = f.Path
			e.hunksOf[f.Path]++
		}
		if _, ok := e.digestAt[f.Path]; !ok {
			e.digestAt[f.Path] = content.digest(f.Path)
		}
	}
	for _, d := range seen {
		e.viewed[d] = true
	}
	return e
}

// SetViewed records the mark and forwards it, so the daemon and the console still see the
// reader's progress exactly as before.
func (e *earnedSync) SetViewed(digest string, on bool) {
	e.viewed[digest] = on
	// Comma-ok rather than a bare lookup: an untracked digest would otherwise record a live
	// mark against the empty path, which no file can ever match but which leaves a map
	// entry that reads as a bug to whoever finds it next.
	if path, ok := e.fileOf[digest]; ok && on {
		e.live[path] = true
	}
	e.diffSync.SetViewed(digest, on)
}

// close mints a receipt for every file whose hunks were all marked read, then shuts the
// wrapped sync down.
//
// At close rather than per mark, because a file is not read until its last hunk is, and a
// reader who marks a hunk and then unmarks it has not read anything. Failures are silent:
// this is a side effect of reading, and a reader who reached the end of a changeset should
// not meet an error about bookkeeping.
func (e *earnedSync) close() {
	defer e.diffSync.close()
	if add := e.finished(); len(add) > 0 {
		_ = review.Record(e.cacheDir, add)
	}
}

// pending is how many files the reader has finished but not yet had recorded, for the line
// the viewer prints on the way out. Reading it before close is the point: a reader is told
// what their session earned at the moment they finish it, not the next time they happen to
// run a report.
func (e *earnedSync) pending() int { return len(e.finished()) }

// finished is every file whose hunks were all marked read in a session that touched it.
func (e *earnedSync) finished() []review.Receipt {
	var add []review.Receipt
	for path, total := range e.hunksOf {
		if !e.live[path] {
			continue
		}
		marked := 0
		for digest, file := range e.fileOf {
			if file == path && e.viewed[digest] {
				marked++
			}
		}
		if marked < total {
			continue
		}
		// The content as it was when the reading STARTED, not as it is now. See digestAt.
		content := e.digestAt[path]
		if content == "" {
			continue
		}
		add = append(add, review.Receipt{Path: path, Digest: content, At: e.now(), Source: e.content.at})
	}
	return add
}

// compatMarker is the convention's opening, INCLUDING the space the convention always
// writes after the colon. Everything from there to the closing paren is the RETIREMENT
// CONDITION, which is the half a reader needs: what would have to become true before the
// code below may go.
//
// inAComment is what keeps this constant out of its own report, by requiring the marker to sit
// in a comment rather than in a string literal. The trailing space cannot do it: the constant
// contains the space too, so it matches itself.
const compatMarker = "compat(until: "

// rationaleHit is one deliberate decision recorded beside code this change touches.
type rationaleHit struct {
	Path string `json:"path"      yaml:"path"`
	Line int    `json:"line"      yaml:"line"`
	// Until is the retirement condition the marker declares. It is carried rather than the
	// whole comment because the condition is the part that tells a reader whether their
	// edit is the thing that retires it.
	Until string `json:"until" yaml:"until"`
}

// rationaleShown bounds the list for the same reason every other section is bounded: a
// report nobody scrolls to the end of has told the reader less than a count would.
const rationaleShown = 8

// collectRationale finds the compat(until:) markers in the files this change touches.
//
// Notes cover the decisions somebody wrote a note about, which is the small minority. This
// covers the ones recorded where they are actually kept: in a comment beside the code, under
// the marker this repository's conventions require. Without this, a reader proposing to undo a
// decision never meets the explanation sitting two lines above the thing they are changing.
//
// FILE-level, not hunk-level, and the wording says so. Deciding whether a marker sits inside
// a changed region needs the hunk ranges, and a marker fifty lines from your edit still
// governs the code you are in - claiming otherwise would be a precision this does not have.
//
// Generated files are skipped: a marker there was written by whatever produced the file.
func collectRationale(root string, rev types.Diff) []rationaleHit {
	var out []rationaleHit
	for _, f := range rev.Files {
		if f.Generated() {
			continue
		}
		out = append(out, compatMarkersIn(root, f.Path)...)
	}
	return out
}

// compatMarkersIn scans one file. An unreadable file yields nothing: a deleted path is the
// common case and is not worth a line of report.
func compatMarkersIn(root, rel string) []rationaleHit {
	fh, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	defer fh.Close()

	var out []rationaleHit
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		idx := strings.Index(sc.Text(), compatMarker)
		if idx < 0 || !inAComment(sc.Text()[:idx]) {
			continue
		}
		until := compatUntil(sc.Text()[idx:])
		if until == "" {
			continue
		}
		out = append(out, rationaleHit{Path: rel, Line: line, Until: until})
	}
	return out
}

// inAComment reports whether the text preceding a marker opens a comment.
//
// The convention writes this marker in a comment beside the code it explains, so a match
// anywhere else is a mention of the convention rather than a use of it - this file's own
// constant, and the fixtures in its test, both of which reported themselves as decisions
// governing the reader's change.
//
// Not a parser, and it does not need to be: it cannot tell a `//` inside a string from one
// that starts a comment. The cases that matters for are files whose subject IS this marker,
// where the remaining noise is a handful of lines in one test.
func inAComment(before string) bool {
	if strings.Contains(before, "//") || strings.Contains(before, "#") || strings.Contains(before, "/*") {
		return true
	}
	// A block comment's continuation lines carry only a leading star.
	return strings.HasPrefix(strings.TrimLeft(before, " \t"), "*")
}

// compatUntil extracts the retirement condition from a marker, empty when there is none.
//
// A condition running past the end of the line is truncated rather than dropped: these are
// prose and routinely wrap, and half a condition still tells a reader what kind of thing
// would retire the code.
func compatUntil(s string) string {
	rest := strings.TrimPrefix(s, compatMarker)
	if end := strings.Index(rest, ")"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// impactRationaleLines renders the section, including its empty form.
func impactRationaleLines(hits []rationaleHit) []string {
	if len(hits) == 0 {
		return []string{"RATIONALE: no compat(until:) marker in the files you changed"}
	}
	out := []string{fmt.Sprintf("RATIONALE: %d compat(until:) marker%s in files you changed - each names why the code stays",
		len(hits), pluralSuffix(len(hits), "", "s"))}
	shown := hits
	if len(shown) > rationaleShown {
		shown = shown[:rationaleShown]
	}
	for _, h := range shown {
		out = append(out, fmt.Sprintf("      %s:%d until %s", h.Path, h.Line, h.Until))
	}
	if len(hits) > len(shown) {
		out = append(out, fmt.Sprintf("      and %d more", len(hits)-len(shown)))
	}
	return out
}

// adviceSection is one advisor's finding: the section it owns in the pull-request
// comment, rendered for a local reader instead. An EMPTY Body is a retraction - the
// advisor ran and found nothing - and is a section like any other, not an absence.
type adviceSection struct {
	Name  string `json:"name"  yaml:"name"`
	Title string `json:"title" yaml:"title"`
	Body  string `json:"body"  yaml:"body"`
}

// adviceDirRel is where this repository keeps the advisors. They are checked in as a
// composite action because CI is where they run first, not because the pull request is
// the only place their answers are useful.
var adviceDirRel = filepath.Join(".github", "actions", "advice")

// localAdvisors is every advisor a local run may execute, in the order action.yml runs
// them. action.yml is the source of truth for that set; this list restates it.
//
// Restating it rather than reading the directory is deliberate, and the deciding reason
// is safety. Three of the scripts in that directory PUSH to a branch, and nothing about a
// filename separates them from the read-only ones: `fix-generated-drift.buzz` and
// `fix-merge-conflict.buzz` share a prefix that `settle-fix-labels.buzz` does not. A
// sweep of *.buzz would therefore enroll a writer into a local command the moment someone
// added one, and the failure mode of getting that wrong is a `magus diff` that pushes.
//
// Two lesser reasons: the directory also holds `advice.buzz`, which is the shared library
// and not an advisor at all. And filename order is not the order action.yml chose.
//
// first-contribution.buzz is the one read-only advisor deliberately left out: it asks the
// forge who opened the pull request, through its own `gh` call rather than through
// advice.buzz, so local mode cannot intercept it. It also has no local meaning.
//
// Restating is not the same as drifting, and TestLocalAdvisorsMatchActionYML is what keeps
// the two apart: it reads the steps back out of action.yml and fails naming any advisor
// that is in one list and not the other. Adding a read-only advisor to CI without adding
// it here is the failure that gate exists for.
var localAdvisors = []string{
	"merge-conflict.buzz",
	"hand-edited-generated.buzz",
	"target-outputs.buzz",
	"doctor.buzz",
	"version-floor.buzz",
	"unclaimed.buzz",
	"blast-radius.buzz",
	"skip-cache.buzz",
	"conformance.buzz",
	"missing-target.buzz",
	"api-surface.buzz",
}

// runLocalAdvisors runs the read-only PR advisors against the local tree and returns
// their sections in localAdvisors order, plus a note per advisor that failed.
//
// base is a BRANCH name, not a rev: the advisors compare against `origin/<base>`, the
// same way they use PR_BASE in CI.
//
// An advisor that raises produces a note and never an error - one broken advisor must not
// take the other nine down, because the caller is showing a reader what magus knows and
// nine tenths of that is still worth showing. The error return is for a failure that
// makes the whole set meaningless.
//
// Not safe for concurrent use: the advisors read their inputs with os\env, so the two
// local-mode variables are set process-wide for the duration of the call.
func runLocalAdvisors(ctx context.Context, m *magus.Magus, base string) ([]adviceSection, []string, error) {
	dir := filepath.Join(m.Root(), adviceDirRel)
	if _, err := os.Stat(dir); err != nil {
		return nil, []string{fmt.Sprintf("no advisors in this workspace: %s is not readable (%v)", dir, err)}, nil
	}

	// The advisors ask magus about the workspace (magus\describeFile, magus\diff,
	// magus\affectedImpact), which reads it off the context the way `magus buzz` does.
	// The caller's already-loaded workspace is attached rather than loaded again:
	// loadMagus is once-per-process and panics on a second call with a different root.
	if m != nil {
		ctx = types.WithWorkspace(ctx, m)
	}
	return collectAdvice(ctx, dir, localAdvisors, base)
}

// collectAdvice is runLocalAdvisors without the workspace and directory resolution, so a
// test can drive it against stub advisors.
func collectAdvice(ctx context.Context, dir string, files []string, base string) ([]adviceSection, []string, error) {
	restore, err := setAdviceEnv(base)
	if err != nil {
		return nil, nil, err
	}
	defer restore()

	var sections []adviceSection
	var notes []string
	for _, file := range files {
		out, warnings, err := runAdvisor(ctx, dir, file)
		// Sections printed before the failure are kept: an advisor that publishes and
		// then dies has already said something true, and dropping it would report the
		// finding as absent rather than as partial.
		sections = append(sections, parseAdviceSections(out)...)
		if err != nil {
			// Warnings first, then the failure: that is the order they happened in, and a
			// BZZ3001 above a crash is usually the explanation for it.
			//
			// ONLY when it crashed. These are lint diagnostics about the advisor's own
			// source - magus's shipped scripts, not the reader's change - so printing them
			// unconditionally buries a one-line docs fix under tens of lines about magus's
			// own files, which reads as "you broke something". That lint belongs in magus's
			// own lint run.
			for _, w := range warnings {
				notes = append(notes, fmt.Sprintf("%s: %s", file, w))
			}
			// Stamped here, not at render time: warnings and failures share one ordered
			// stream, and only this frame knows which is which.
			notes = append(notes, fmt.Sprintf("could not run: %s: %v", file, err))
		}
	}
	return sections, notes, nil
}

// The contract between this driver and advice.buzz. The names are read there with os\env;
// there is no per-session environment to hand a Buzz script, so they are set on the
// process.
//
// MAGUS_INTERNAL_ says what these are: the handshake between two halves of one feature,
// not a knob. Setting either by hand puts the advisors into local mode with no driver
// reading their stdout, and both names may change with this file in one commit. advice.buzz
// pins the same three strings in a test block of its own, because nothing at runtime
// couples its copy to this one - rename one side alone and every advisor fails with
// "nowhere to publish" instead of saying anything.
const (
	adviceModeEnv       = "MAGUS_INTERNAL_ADVICE_MODE"
	adviceModeLocal     = "local"
	adviceBaseBranchEnv = "MAGUS_INTERNAL_ADVICE_BASE_BRANCH"
)

// setAdviceEnv puts the local-mode variables on the process and returns the restore.
// It restores an absent variable to absent rather than to empty, because advice.buzz
// distinguishes the two.
func setAdviceEnv(base string) (func(), error) {
	saved := map[string]*string{}
	for name, want := range map[string]string{adviceModeEnv: adviceModeLocal, adviceBaseBranchEnv: base} {
		if had, ok := os.LookupEnv(name); ok {
			saved[name] = &had
		} else {
			saved[name] = nil
		}
		if err := os.Setenv(name, want); err != nil {
			return func() {}, fmt.Errorf("magus diff: set %s: %w", name, err)
		}
	}
	return func() {
		for name, was := range saved {
			if was == nil {
				_ = os.Unsetenv(name)
				continue
			}
			_ = os.Setenv(name, *was)
		}
	}, nil
}

// runAdvisor evaluates one advisor in-process and returns what it printed, the warnings
// its compilation raised, and the error that ended it. Nothing here writes or exits: all
// three are the caller's to report.
//
// The session is built as `magus buzz <file>` builds one - same module surface, same
// strict parse mode, warnings drained at the same point, `fun main() > int` read rather
// than discarded - so an advisor cannot behave one way in CI and another way here. Two
// differences remain, both deliberate:
//
//   - std.print goes to a buffer, not stdout. That IS the transport: a section is a line
//     the advisor printed, and parseAdviceSections reads them back.
//   - the include path is set on the session rather than through BUZZ_INCLUDE_PATH, for
//     the reason at the call site below.
//
// Where those two observations LAND differs as well, and has to. `magus buzz` prints
// warnings to stderr and exits with main's value; ten advisors run here, and the ninth
// failing is not a verdict on `magus diff`. Both become notes instead.
func runAdvisor(ctx context.Context, dir, file string) (string, []string, error) {
	src, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return "", nil, err
	}
	sess := buzz.NewSession(ctx)
	defer func() { _ = sess.Close() }()
	// `import "advice"` resolves against this list. The composite action gets the same
	// effect from BUZZ_INCLUDE_PATH; setting it directly keeps the advisor's view of the
	// filesystem out of the process environment.
	sess.SetIncludeDirs([]string{dir})

	var out bytes.Buffer
	bindings.RegisterModuleSurface(ctx, sess, bindings.WithScriptOutput(&out))
	bindings.RegisterMagusNamespace(ctx, sess)
	bindings.RegisterSpellSourceModules(sess)

	if err := sess.Exec(ctx, string(src)); err != nil {
		return out.String(), nil, err
	}
	// Drained where `magus buzz` drains them: after Exec, before main. These are parse and
	// check diagnostics (BZZ3001 unused import, and the rest), so Exec is where all of them
	// are produced, and it never fails on one - which is exactly why they need collecting
	// rather than trusting a green run. An advisor whose imports have rotted is the kind of
	// thing a reader wants told, not left to read as an advisor with nothing to say.
	var warnings []string
	for _, w := range sess.Warnings() {
		w.File = file
		warnings = append(warnings, w.String())
	}

	mainFn := sess.GetGlobal("main")
	if !mainFn.IsFun() {
		return out.String(), warnings, fmt.Errorf("no main() to run")
	}
	ret, err := sess.CallValue(ctx, mainFn, []vm.Value{vm.ListValue(nil)})
	if err != nil {
		return out.String(), warnings, err
	}
	// `fun main() > int` is upstream's exit-status convention, and the checker permits it,
	// so an advisor may report failure by returning rather than by throwing. Discarding the
	// value read that advisor as having succeeded.
	if ret.IsInt() && ret.AsInt() != 0 {
		return out.String(), warnings, fmt.Errorf("main() returned %d", ret.AsInt())
	}
	return out.String(), warnings, nil
}

// parseAdviceSections picks the section objects out of one advisor's output. An advisor
// also prints a progress line for a human ("unclaimed: advised on ..."), so the two are
// told apart by decoding rather than by position: a line that is not a section object is
// the advisor talking, and is dropped.
func parseAdviceSections(out string) []adviceSection {
	var got []adviceSection
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var s adviceSection
		if err := json.Unmarshal([]byte(line), &s); err != nil || s.Name == "" {
			continue
		}
		got = append(got, s)
	}
	return got
}

// impactReview is where a reader left off in this change.
//
// It is a BOOKMARK, not a score, and the difference decides the whole design. An earlier
// version led with "N of M files carry a read receipt", which is a completion metric: it
// has a target, and the cheapest way to reach the target is to stamp everything without
// reading it. A count that can be satisfied by typing is worse than no count, because it
// trains the reader to type something they do not mean.
//
// So no ratio is reported. What is reported is the two things a reader cannot produce
// without reading: which files moved after they read them, and which they have not opened.
// Both are answers to "where was I", and the test any addition here has to pass is whether
// somebody would still want it if nobody else ever saw the result.
type impactReview struct {
	Files int `json:"files" yaml:"files"`
	Read  int `json:"read"  yaml:"read"`
	// Stale are files read and then edited. They lead the section: the signal is derived from
	// CONTENT rather than from a claim, so inattention cannot fake it. See types.DiffReadStale
	// for why it outranks unread.
	Stale  []string `json:"stale,omitempty"  yaml:"stale,omitempty"`
	Unread []string `json:"unread,omitempty" yaml:"unread,omitempty"`
	// Required are unread files inside a project's declared review_required globs. Listed
	// separately and in FULL rather than capped: the workspace said an unread change costs
	// something here, so this is the half of the section that is not just a count.
	Required []string `json:"required,omitempty" yaml:"required,omitempty"`
	// Reasons are the distinct bulk-ack justifications covering files in this changeset,
	// so "somebody read it" and "somebody assumed it was fine, here is why" do not
	// collapse into one number.
	Reasons []string `json:"reasons,omitempty" yaml:"reasons,omitempty"`
}

// unreadShown bounds the never-opened list, which is context rather than the finding.
const unreadShown = 10

// reviewMinFiles is the changeset size below which the section says nothing at all.
//
// A four-file change needs no reading plan, and printing one there is how a reader learns
// to skip this section before ever meeting a change big enough to need it. Nothing stale is
// part of the condition: a small change where a file moved after you read it is exactly
// when the section earns its line.
const reviewMinFiles = 5

// reviewRequiredMatcher reports whether a workspace-relative path sits inside any project's
// declared review_required globs.
//
// Globs are matched against the path relative to the DECLARING project, so a project names
// its own files the same way its sources and outputs do rather than having to spell the
// workspace prefix that its magusfile already sits under.
//
// nil when no project declares any, which the caller treats as "single nothing out" rather
// than as "everything matters".
func reviewRequiredMatcher(ws types.WorkspaceReader) func(string) bool {
	type scope struct {
		dir   string
		globs []string
	}
	var scopes []scope
	for _, p := range ws.All() {
		if len(p.ReviewRequired) > 0 {
			scopes = append(scopes, scope{dir: p.Path, globs: p.ReviewRequired})
		}
	}
	if len(scopes) == 0 {
		return nil
	}
	return func(path string) bool {
		for _, s := range scopes {
			rel := path
			if s.dir != "" && s.dir != "." {
				if !strings.HasPrefix(path, s.dir+"/") {
					continue
				}
				rel = strings.TrimPrefix(path, s.dir+"/")
			}
			for _, g := range s.globs {
				if ok, err := doublestar.Match(g, rel); err == nil && ok {
					return true
				}
			}
		}
		return false
	}
}

// bulkReasons is every distinct reason a bulk ack recorded against a file in this
// changeset, in first-seen order.
func bulkReasons(cacheDir string, rev types.Diff) []string {
	store, err := review.Load(cacheDir)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, f := range rev.Files {
		r, ok := store[f.Path]
		if !ok || r.Reason == "" || seen[r.Reason] {
			continue
		}
		seen[r.Reason] = true
		out = append(out, r.Reason)
	}
	return out
}

// collectReview tallies the read state already folded onto the changeset by annotateDiff.
//
// It reads DiffFile.ReadState rather than consulting the store a second time, so the
// terminal report and the console's review surface cannot disagree about which files
// somebody has read - they are looking at one join.
//
// nil when no file carries a state at all, which the renderer states as unmeasured rather
// than as unread. Those are opposite claims and only one of them accuses.
//
// Generated files are excluded: reading a machine's restatement of an edit made elsewhere
// is not the review, the same reason the file list folds them away by default.
func collectReview(rev types.Diff, required func(string) bool, reasons []string) *impactReview {
	out := &impactReview{Reasons: reasons}
	measured := false
	for _, f := range rev.Files {
		if f.Generated() {
			continue
		}
		out.Files++
		switch f.ReadState {
		case types.DiffReadRead:
			measured = true
			out.Read++
			continue
		case types.DiffReadStale:
			measured = true
			out.Stale = append(out.Stale, f.Path)
			// Not also Required: the section already names it under "changed after you
			// read them", and listing it again as "unopened" would both contradict itself
			// and print one path as two findings.
			continue
		case types.DiffReadUnread:
			measured = true
			out.Unread = append(out.Unread, f.Path)
		default:
			continue
		}
		if required != nil && required(f.Path) {
			out.Required = append(out.Required, f.Path)
		}
	}
	if !measured && out.Files > 0 {
		return nil
	}
	return out
}

// scopeAck narrows a changeset to the paths the caller named, so a reader can record the
// three files they just read in their editor without claiming the other thirty.
//
// An unnamed path is an ERROR rather than a silent no-op. The whole value of a receipt is
// that it names something real; a typo that quietly acknowledged nothing would leave the
// reader believing they had recorded work they had not.
func scopeAck(rev types.Diff, paths []string) (types.Diff, error) {
	if len(paths) == 0 {
		return rev, nil
	}
	inChange := make(map[string]bool, len(rev.Files))
	for _, f := range rev.Files {
		inChange[f.Path] = true
	}
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "./")
		if !inChange[clean] {
			return types.Diff{}, usagef("magus diff --ack: %q is not a changed file in this changeset; `magus diff -o name` lists them", p)
		}
		want[clean] = true
	}
	out := types.Diff{Base: rev.Base}
	for _, f := range rev.Files {
		if want[f.Path] {
			out.Files = append(out.Files, f)
		}
	}
	return out, nil
}

// reviewedContent resolves the bytes a receipt attests to, for whichever changeset source is
// being read.
//
// It exists because getting this wrong is silent and severe. Every receipt was minted from the
// WORKING TREE, which is correct for a working-tree review and a lie for a range one: it would
// stamp a colleague's file as read at the content of your own checkout, and Covers would then
// agree forever. Making the source an explicit value means a new minting site has to say which
// tree it means rather than inheriting the wrong default.
type reviewedContent struct {
	root string
	// at is zero for the working tree, and otherwise the revision the content is read from.
	at   types.VCSCheckpoint
	read func(rev, path string) (string, error)
}

// digest fingerprints one path, or returns "" where there is nothing to attest to.
//
// "" is the answer for a file that is absent - deleted in the working tree, or not present at the
// revision. Recording a receipt against it would satisfy Covers for every unreadable file forever.
func (c reviewedContent) digest(path string) string {
	if c.at.Revision == "" {
		return review.DigestFile(filepath.Join(c.root, filepath.FromSlash(path)))
	}
	body, err := c.read(c.at.Revision, path)
	if err != nil {
		return ""
	}
	return review.Digest([]byte(body))
}

// contentOf says which tree a receipt minted for this source should attest to.
//
// A source magus cannot address yields the working tree, which is what every caller did before
// this existed. That is safe only because the two gates that mint receipts - --ack and the viewer
// - both refuse an unaddressable source outright, so the fallback is unreachable rather than
// merely unlikely. If either ever accepts a patch on stdin, this has to refuse instead.
func contentOf(ctx context.Context, m *magus.Magus, src diffInput) reviewedContent {
	c := reviewedContent{
		root: m.Root(),
		read: func(rev, path string) (string, error) { return m.FileAt(ctx, rev, path) },
	}
	if src.kind != inputRevRange {
		return c
	}
	// A range magus cannot resolve leaves at zero, and the caller then digests the working tree
	// for a review of somebody else's branch. Refused rather than degraded: --ack has already
	// checked the range resolves, so reaching here means the repository moved mid-command.
	at, err := m.RevisionCheckpoint(ctx, src.head)
	if err != nil {
		return c
	}
	c.at = at
	return c
}

// ackChangeset records a receipt for every non-generated changed file at the content the reader
// was shown, carrying the reason the caller gave for covering them all at once.
func ackChangeset(content reviewedContent, cacheDir string, rev types.Diff, reason string, now time.Time) (int, error) {
	var add []review.Receipt
	for _, f := range rev.Files {
		if f.Generated() {
			continue
		}
		digest := content.digest(f.Path)
		if digest == "" {
			continue
		}
		add = append(add, review.Receipt{
			Path: f.Path, Digest: digest, At: now, Source: content.at, Reason: reason,
		})
	}
	if len(add) == 0 {
		return 0, nil
	}
	return len(add), review.Record(cacheDir, add)
}

// impactReviewLines renders the section, or nothing at all.
//
// Nil is a real answer here and the common one. A section that always prints is a section
// people stop reading, and this one has nothing to say about a small change nobody has
// disturbed since reading.
//
// It belongs in the impact report rather than the viewer because the viewer counts hunks read
// and has no notion of STALE. "This changed after you read it" is the half worth having, and
// this is the only place it is said.
//
// TODO: teach the viewer stale state, so a reader stepping through sees which files moved
// under them. That is additive - it is not a reason to drop the only telling of it here.
func impactReviewLines(r *impactReview) []string {
	if r == nil {
		return []string{"REVIEW: read receipts unavailable; step a file through in `magus diff` to earn one"}
	}
	// Silence, not a reassurance. "Everything here has been read" would be a claim the
	// reader can produce by stamping rather than by reading, which is the sentence this
	// section was rebuilt to stop printing.
	if r.Files == 0 || (len(r.Stale) == 0 && (r.Files < reviewMinFiles || len(r.Unread) == 0)) {
		return nil
	}

	var out []string
	// Stale leads, always, whatever else is in the section. It is the one finding here
	// that no amount of stamping produces: the file moved after somebody read it.
	if len(r.Stale) > 0 {
		out = append(out, fmt.Sprintf("REVIEW: %d file(s) changed after you read them", len(r.Stale)))
		for _, p := range r.Stale {
			out = append(out, "      "+p)
		}
	}
	// Then what the workspace itself said was worth reading, uncapped.
	if len(r.Required) > 0 {
		head := fmt.Sprintf("%d unopened in review_required paths:", len(r.Required))
		if len(out) == 0 {
			out = append(out, "REVIEW: "+head)
		} else {
			out = append(out, "      "+head)
		}
		for _, p := range r.Required {
			out = append(out, "        "+p)
		}
	}
	// Then the rest, as context and capped, in the order the reader would take them.
	if rest := unreadRest(r); len(rest) > 0 {
		head := fmt.Sprintf("%d file(s) you have not opened, widest blast radius first", len(rest))
		if len(out) == 0 {
			out = append(out, "REVIEW: "+head)
		} else {
			out = append(out, "      "+head)
		}
		shown := rest
		if len(shown) > unreadShown {
			shown = shown[:unreadShown]
		}
		for _, p := range shown {
			out = append(out, "        "+p)
		}
		if len(rest) > len(shown) {
			out = append(out, fmt.Sprintf("        and %d more", len(rest)-len(shown)))
		}
	}
	for _, reason := range r.Reasons {
		// Echoed so a file covered by one keystroke does not read as one somebody sat
		// down with. It is a note the reader left themselves, not a toll they paid.
		out = append(out, fmt.Sprintf("      some were covered in bulk: %q", reason))
	}
	if len(out) == 0 {
		return nil
	}
	// Both doors, because the reader's editor is not magus's business: naming only the
	// viewer told anyone who reviews in vim or magit that their only option was the
	// blanket ack.
	return append(out,
		"      record what you read, wherever you read it: magus diff --ack <path>...",
		"      or step through them here: magus diff")
}

// unreadRest is the never-opened files the section has not already named under
// review_required, so no path appears twice.
func unreadRest(r *impactReview) []string {
	if len(r.Required) == 0 {
		return r.Unread
	}
	named := make(map[string]bool, len(r.Required))
	for _, p := range r.Required {
		named[p] = true
	}
	var out []string
	for _, p := range r.Unread {
		if !named[p] {
			out = append(out, p)
		}
	}
	return out
}
