package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
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
	src, err := diffInputFromArgs(rest)
	if err != nil {
		return err
	}
	if rf.Watch && src.kind != inputWorkingTree {
		// stdin is consumed once and a patch file is a snapshot someone handed us; re-reading
		// either on every tree change would re-render identical output forever.
		return usagef("magus diff: --watch reads the working tree, so it cannot be combined with %s", src.label)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	render := func() error { return renderDiff(ctx, m, src, opts, rf) }
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
)

// diffInput names a patch source and how to describe it to the reader.
type diffInput struct {
	kind  diffInputKind
	path  string // for inputFile
	label string
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
	default:
		p, err := m.WorkingDiff(ctx, nil)
		return p, "working", err
	}
}

// renderDiff reads one patch and emits it in the requested format.
func renderDiff(ctx context.Context, m *magus.Magus, src diffInput, opts OutputOptions, rf *gen.DiffFlags) error {
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
	rev, err := m.Diff(ctx, paths)
	if err != nil {
		return err
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

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, rev)
	case outputName:
		for _, f := range rev.Files {
			if f.Generated() && !rf.Generated {
				continue
			}
			fmt.Println(f.Path)
		}
		return nil
	}
	return printDiffText(rev, rf.Generated, pathLinker(m.Root()))
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

func diffUsage(w *os.File) {
	fmt.Fprintln(w, "Usage: magus diff [--generated] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Read the working tree's uncommitted changes, ordered by what they can break.")
	fmt.Fprintln(w, "It takes no ref: the subject is always the uncommitted tree.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Generated files - declared target outputs - are folded away: reading one is")
	fmt.Fprintln(w, "reading a machine's restatement of a change made elsewhere, so the source edit")
	fmt.Fprintln(w, "is what to read.")
	fmt.Fprintln(w, "")
	// Name the ranking key exactly, and name what is NOT one. The previous wording listed
	// reach, public surface, and coverage as "the evidence behind its rank"; only reach ranks,
	// so a reader who saw a hot file sitting eighth concluded the ranking had weighed churn and
	// dismissed it. Printing a number beside a rank it did not earn teaches the wrong model.
	fmt.Fprintln(w, "The order is: declared outputs last, then the widest reach first - how many")
	fmt.Fprintln(w, "files reference the most-referenced symbol the file changed. Reach needs a")
	fmt.Fprintln(w, "symbol index; without one there is no ranking key at all, and diff says so")
	fmt.Fprintln(w, "at the top and falls back to path order rather than implying a ranking.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Public surface, coverage, churn, and the agent trail are CONTEXT printed")
	fmt.Fprintln(w, "beside each file. None of them is a sort key.")
	fmt.Fprintln(w, "")
	// State the cutoff rather than leaving a missing rank ambiguous.
	fmt.Fprintf(w, "A hotspot rank is shown only inside the workspace's top %d. A file that reports\n", types.NotableRankCutoff)
	fmt.Fprintln(w, "a commit count and no rank was measured and sits outside that cutoff; a file with")
	fmt.Fprintln(w, "no history line at all is one magus has never seen change.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --generated   include the folded declared outputs")
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

// printDiffText renders the diff in the house style: counts before lists, the evidence
// beside the claim, plain ASCII.
func printDiffText(rev types.Diff, showGenerated bool, link func(string) string) error {
	var primary, generated []types.DiffFile
	for _, f := range rev.Files {
		if f.Generated() {
			generated = append(generated, f)
		} else {
			primary = append(primary, f)
		}
	}

	fmt.Printf("%d files to read", len(primary))
	if len(generated) > 0 {
		fmt.Printf(", %d generated folded", len(generated))
	}
	if n := len(rev.SeedProjects); n > 0 {
		// "rebuild" carried no noun and readers could not tell what the count was OF.
		fmt.Printf("; %d projects edited, %d projects rebuild", n, len(rev.AffectedProjects))
	}
	fmt.Println()
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
	return nil
}

func printDiffFile(f types.DiffFile, link func(string) string) {
	fmt.Printf("  %s\n", link(f.Path))

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
	for _, fact := range facts {
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
func changedPathsFromPatch(patch string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(patch, "\n") {
		if !strings.HasPrefix(line, "diff --git ") {
			continue
		}
		rest := strings.TrimPrefix(line, "diff --git ")
		cut := strings.LastIndex(rest, " b/")
		if cut < 0 {
			continue
		}
		p := strings.TrimPrefix(rest[cut+1:], "b/")
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
