package main

import (
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

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/diff"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/interactive/difftui"
	"github.com/egladman/magus/internal/interactive/tty"
	json "github.com/egladman/magus/internal/json"
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
	term := diffTUITerm{
		Reads:  isInteractiveTTY(),
		Paints: tty.IsTerminalWriter(os.Stdout, tty.SystemProbe),
	}
	if err := diffTUIRefusal(rf, src, opts.Format, term); err != nil {
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

// diffTUITerm is the terminal the viewer was handed, split by descriptor, because the viewer
// uses two of them and one probe does not answer for both.
type diffTUITerm struct {
	// Reads is the shared interactive gate every stepping surface asks for: stdin and stderr
	// are both terminals.
	Reads bool
	// Paints is stdout, which the viewer draws the changeset on. Reads never looks at it, and
	// `magus diff --tui > file` is what fell through the gap: the flags passed, then
	// tty.OpenInput refused with a bare error naming none of this.
	Paints bool
}

// ok reports whether the viewer can have this terminal.
func (t diffTUITerm) ok() bool { return t.Reads && t.Paints }

// diffTUIRefusal reports why --tui cannot run under these flags, or nil when it can.
//
// Every refusal is LOUD and names plain `magus diff` as the way to get the same answer,
// because each of these combinations has a reading that looks like it should work and a
// silent one that would be a lie: a patch file has no working tree to coordinate a session
// over, a watch loop and a keypress loop both own the same terminal, and -o json asked for
// a machine-readable answer that a viewport cannot give.
//
// It takes the terminal as an ARGUMENT rather than probing it, which is what makes the
// refusal matrix testable without a pty.
func diffTUIRefusal(rf *gen.DiffFlags, src diffInput, format Format, term diffTUITerm) error {
	if !rf.Tui {
		return nil
	}
	if src.kind != inputWorkingTree {
		return usagef("magus diff: --tui reads the working tree, so it cannot be combined with %s", src.label)
	}
	if rf.Watch {
		return usagef("magus diff: --tui and --watch both drive the terminal, so they cannot be combined")
	}
	if format != outputText {
		return usagef("magus diff: --tui draws at a terminal, so it cannot be combined with -o %s", format)
	}
	if !term.ok() {
		// Not a usage error: the flags are fine and the terminal is not, so say which one and
		// name the command that works here.
		fmt.Fprintln(os.Stderr, "magus: diff --tui requires an interactive terminal; use `magus diff` instead")
		return errSilent{exitCode: 2}
	}
	return nil
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
	if rf.Tui {
		return runDiffTUI(ctx, m, patch, base, paths, rf.Generated)
	}
	rev, err := annotateDiff(ctx, m, paths, base)
	if err != nil {
		return err
	}

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

// annotateDiff computes the annotated changeset for a set of changed paths.
//
// One definition, because the TUI and the one-shot renderer must show the same facts - two
// callers folding on their own overlays is how "the console said 12 files reference this and
// the CLI said nothing" happens.
func annotateDiff(ctx context.Context, m *magus.Magus, paths []string, base string) (types.Diff, error) {
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
func runDiffTUI(ctx context.Context, m *magus.Magus, patch, base string, paths []string, showGenerated bool) error {
	rev, sess, sync, err := attachDiffSession(ctx, m, patch, base, paths)
	if err != nil {
		return err
	}
	// Closed here rather than inside difftui: how a Sync gets its writes out - inline, or over a
	// goroutine that has to be drained - is this file's business, and the viewer stays ignorant
	// of it. Deferred before Run, so it also runs when the reader interrupts.
	defer sync.close()
	return difftui.Run(ctx, difftui.Options{
		In:    os.Stdin,
		Out:   os.Stdout,
		Probe: tty.SystemProbe,
		Input: difftui.Input{
			Files:       diffTUIFiles(rev, diff.ParseHunks(patch)),
			Unranked:    !rev.Ranked(),
			Viewed:      sess.Viewed,
			Comments:    sess.Comments,
			Suggestions: sess.Suggestions,
			Unfolded:    showGenerated,
			Link:        pathLinker(m.Root()),
		},
		Sync: sync,
		// The fold the session OPENS in. A reader who presses `.` changes what they are shown
		// and not this line, which is the closest an up-front string gets to the truth.
		Summary: diffCountsLine(rev, showGenerated),
	})
}

// attachDiffSession joins the shared review, daemon first.
//
// With a daemon running its session is the ONE session - the console tab and the agent are
// already on it, so the terminal joining anywhere else would be a fourth opinion wearing the
// same name. Without one there is nobody to pair with, so the changeset is computed here and
// progress goes straight into the file the daemon's own store would have written.
func attachDiffSession(ctx context.Context, m *magus.Magus, patch, base string, paths []string) (types.Diff, *types.DiffSession, diffSync, error) {
	asOf := diff.PatchDigest(patch)
	if b := dialDiffBridge(ctx, paths, asOf); b != nil {
		return b.session.Diff, b.session, b, nil
	}
	rev, err := annotateDiff(ctx, m, paths, base)
	if err != nil {
		return types.Diff{}, nil, nil, err
	}
	// Written straight into the store rather than through the daemon, which is sound only
	// because there is no daemon: with one running it owns this file, and two writers would
	// each persist their own idea of the whole set. Attach is what loads the marks a previous
	// session left AND what makes MarkViewed below have a session to write to.
	store := diff.NewStore(m.CacheDir())
	sess := store.Attach(m.Root(), base, rev, asOf)
	return rev, sess, diffStoreSync{store: store, root: m.Root()}, nil
}

// diffTUIFiles joins the annotations to the patch: one is ordered by consequence, the other
// by whatever the VCS emitted, and the reader wants the first order with the second's text.
//
// The annotation order is authoritative and is never recomputed here - types.Diff.
// SortForReading is the single definition of review order.
func diffTUIFiles(rev types.Diff, parsed []diff.FileHunks) []difftui.File {
	byPath := make(map[string][]diff.Hunk, len(parsed))
	for _, f := range parsed {
		byPath[f.Path] = f.Hunks
	}
	out := make([]difftui.File, 0, len(rev.Files))
	for _, f := range rev.Files {
		file := difftui.File{Path: f.Path, Generated: f.Generated(), Facts: diffFileFacts(f)}
		for _, h := range byPath[f.Path] {
			file.Hunks = append(file.Hunks, difftui.Hunk{
				Index: h.Index, Header: h.Header, Lines: h.Lines, Digest: h.Digest,
			})
		}
		out = append(out, file)
	}
	return out
}

// diffSync is a difftui.Sync with a shutdown. The two implementations get their writes out
// differently - one to a local file, one over a goroutine that has to be drained - and the
// viewer must not have to know which it was handed.
type diffSync interface {
	difftui.Sync
	close()
}

// diffStoreSync persists the reader's progress with no daemon in the picture. There is no
// cursor to publish: nobody is listening.
type diffStoreSync struct {
	store *diff.Store
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
// Writes leave on sends rather than on the caller's stack. Every one of them is provoked by a
// KEYPRESS - a cursor move, a read mark - and posting inline put a network round trip between
// the key and the screen moving, up to the full diffBridgeWrite deadline against a daemon that
// had stopped answering.
type diffBridge struct {
	addr    string
	token   string
	session *types.DiffSession
	sends   chan diffSessionOp
	done    chan struct{}
}

// diffBridgeAttach bounds the GET that annotates and attaches. It is the expensive route -
// symbol shards and a reverse closure - so this is generous; the alternative on a slow answer
// is not a faster one, it is computing the same thing locally.
const diffBridgeAttach = 10 * time.Second

// diffBridgeWrite bounds a coordination write. Short, because a wedged daemon must not hold
// the sender long enough for the queue behind it to overflow, and because it is also how long
// quitting waits for the last mark to leave.
const diffBridgeWrite = time.Second

// diffBridgeQueue bounds the writes waiting to leave. Small on purpose: a backlog means the
// daemon has stopped keeping up, and at that point the newest cursor is the only one worth
// having - the ones behind it describe somewhere the reader no longer is.
const diffBridgeQueue = 8

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
	b.sends = make(chan diffSessionOp, diffBridgeQueue)
	b.done = make(chan struct{})
	go b.deliver()
	return b
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
	b.queue(diffSessionOp{Op: "cursor", Path: c.Path, Hunk: c.Hunk})
}

// SetViewed publishes a read mark, which the daemon persists for every client at once.
func (b *diffBridge) SetViewed(digest string, on bool) {
	b.queue(diffSessionOp{Op: "viewed", Digest: digest, On: on})
}

// queue hands one mutation to the sender, and DROPS it when the sender is behind. It never
// blocks: the key loop is what calls it, and difftui.Sync promises best-effort delivery
// precisely so a slow daemon costs the reader nothing.
func (b *diffBridge) queue(op diffSessionOp) {
	select {
	case b.sends <- op:
	default:
	}
}

// deliver posts queued mutations one at a time, in order, until the queue is closed.
func (b *diffBridge) deliver() {
	defer close(b.done)
	for op := range b.sends {
		b.post(op)
	}
}

// close stops the sender and gives what is already queued a bounded chance to leave: a mark
// made on the last keypress should not be lost to the process exiting, and a daemon that has
// stopped answering should not hold the shell either.
func (b *diffBridge) close() {
	close(b.sends)
	select {
	case <-b.done:
	case <-time.After(diffBridgeWrite):
	}
}

// post sends one mutation, best-effort. A coordination write that fails is a pairing that
// went quiet, not a review that has to stop - and there is nothing useful to say about it to
// somebody in the middle of reading a diff.
func (b *diffBridge) post(op diffSessionOp) {
	body, err := json.Marshal(op)
	if err != nil {
		return
	}
	// Not the session's context: this is fire-and-forget under its own short deadline, and a
	// cancelled read should not turn the last write into a lost mark.
	ctx, cancel := context.WithTimeout(context.Background(), diffBridgeWrite)
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
	fmt.Fprintln(w, "  --tui         read it interactively, joined to the session the console")
	fmt.Fprintln(w, "                and an agent share: ] and [ walk hunks, v marks one read")
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
// The fold state is threaded rather than assumed: the line said "folded" unconditionally, so
// under --generated - where every one of them is printed right below it - the headline
// contradicted the page it introduced.
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
		// "rebuild" carried no noun and readers could not tell what the count was OF.
		line += fmt.Sprintf("; %d projects edited, %d projects rebuild", n, len(rev.AffectedProjects))
	}
	return line
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

	// The same changeset in the console's Diff surface. Interactive-only: the token rides
	// the URL fragment (see printJobWatchHint), so it must never land in a captured log.
	if tty.IsTerminalWriter(os.Stdout, tty.SystemProbe) {
		if u := consoleDiffURL(); u != "" {
			fmt.Printf("\nopen in console: %s\n", u)
		}
	}
	return nil
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
