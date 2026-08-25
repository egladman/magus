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
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/diff"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/interactive/difftui"
	"github.com/egladman/magus/internal/interactive/tty"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/trail"
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
	if rf.Watch && src.kind != inputWorkingTree {
		// stdin is consumed once and a patch file is a snapshot someone handed us; re-reading
		// either on every tree change would re-render identical output forever.
		return usagef("magus diff: --watch reads the working tree, so it cannot be combined with %s", src.label)
	}
	if rf.Ack && src.kind != inputWorkingTree {
		// A receipt fingerprints the file on disk, and a patch describes files that may
		// not be there. Accepting it would record receipts against whatever the working
		// tree happens to hold - an acknowledgement of something nobody read.
		return usagef("magus diff: --ack fingerprints the working tree, so it cannot be combined with %s", src.label)
	}
	if rf.Ack && (rf.Tui || rf.Watch) {
		return usagef("magus diff: --ack records once and returns, so it cannot be combined with a live view")
	}
	// --reason is optional, deliberately. It was briefly required, on the reasoning that a
	// bulk stamp should cost a sentence the way spells.allow_shadow does. The shapes look
	// alike and behave oppositely: an allow_shadow entry is written a handful of times in a
	// repository's life and stays meaningful, while a changeset ack is written daily and
	// becomes a form field - at which point the requirement has only taught the reader to
	// type something they do not mean.
	//
	// The two controls that actually hold are the ones nobody can satisfy by typing faster:
	// the agent guard, and the terminal check below.
	if rf.Ack && !isInteractiveTTY() {
		// The agent guard denies --ack outright, but it fails OPEN where it is not wired,
		// and a receipt minted by a script is precisely the laundering this refuses. Not a
		// usage error: the flags are fine and the caller is not a person.
		// 2, matching --tui at a non-interactive terminal: the flags are fine and the
		// request cannot be served as asked, which the documented taxonomy separates from
		// a 1 (the changeset could not be read).
		fmt.Fprintln(os.Stderr, "magus: diff --ack records that a person read this, so it needs an interactive terminal")
		return errSilent{exitCode: 2}
	}
	if rf.Cost && rf.Tui {
		// Refused rather than ignored. The viewer has nowhere to put a report, so accepting the
		// flag would answer the question with a page that never mentions it - and a flag that
		// silently does nothing is the failure this command's refusal matrix exists to prevent.
		return usagef("magus diff: --cost is a report and --tui is a viewport, so they cannot be combined; run `magus diff --cost` for the report")
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

	render := func() error { return renderDiff(ctx, m, src, opts, rf, root, rf.Cost, ackPaths) }
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
//
// preflight is ADDITIVE: with it off, every byte emitted here is what this command emitted
// before the flag existed, which is what lets a script parsing `magus diff -o json` keep
// working and what keeps the flag honest about being context rather than a gate.
func renderDiff(ctx context.Context, m *magus.Magus, src diffInput, opts OutputOptions, rf *gen.DiffFlags, rootOverride string, preflight bool, ackPaths []string) error {
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
	if rf.Ack {
		reason := strings.TrimSpace(rf.Reason)
		scoped, err := scopeAck(rev, ackPaths)
		if err != nil {
			return err
		}
		n, err := ackChangeset(m.Root(), m.CacheDir(), scoped, reason, time.Now())
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

	var pre *diffPreflight
	if preflight {
		p := collectPreflight(ctx, m, rootOverride, rev)
		pre = &p
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		if pre != nil {
			return emitFormatted(opts, diffReport{Diff: rev, Preflight: pre})
		}
		return emitFormatted(opts, rev)
	case outputName:
		// Left alone under --preflight: -o name is the shape a shell loop reads, and one
		// non-path line in it would break every one of them.
		for _, f := range rev.Files {
			if f.Generated() && !rf.Generated {
				continue
			}
			fmt.Println(f.Path)
		}
		return nil
	}
	return printDiffText(rev, rf.Generated, pathLinker(m.Root()), pre)
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
	// Which of these files somebody has recorded reading. Best-effort like every other
	// overlay: an unreadable store leaves every file DiffReadUnknown, which renders as
	// unmeasured rather than as unread.
	if states, serr := review.ReadStates(m.Root(), m.CacheDir(), paths); serr == nil {
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
func runDiffTUI(ctx context.Context, m *magus.Magus, patch, base string, paths []string, showGenerated bool) error {
	rev, sess, sync, err := attachDiffSession(ctx, m, patch, base, paths)
	if err != nil {
		return err
	}
	files := diffTUIFiles(rev, diff.ParseHunks(patch))
	// Wrapped so finishing a file in the viewer leaves a receipt behind it. The marks were
	// always explicit; this is what makes them outlive the session.
	sync = newEarnedSync(sync, m.Root(), m.CacheDir(), files, sess.Viewed)
	// Closed here rather than inside difftui: how a Sync gets its writes out - inline, or over a
	// goroutine that has to be drained - is this file's business, and the viewer stays ignorant
	// of it. Deferred before Run, so it also runs on the interrupts Run RETURNS from: `q`,
	// Ctrl-C read as a key, a cancelled context. A raw SIGINT unwinds nothing, and the reader
	// loses the queue along with the restored terminal.
	defer sync.close()
	return difftui.Run(ctx, difftui.Options{
		In:    os.Stdin,
		Out:   os.Stdout,
		Probe: tty.SystemProbe,
		Input: difftui.Input{
			Files:       files,
			Unranked:    !rev.Ranked(),
			Viewed:      sess.Viewed,
			Comments:    sess.Comments,
			Suggestions: sess.Suggestions,
			Unfolded:    showGenerated,
			Link:        pathLinker(m.Root()),
		},
		Sync: sync,
		// Called at quit rather than computed here, so the line reports the fold the reader
		// LEFT in: `.` changes what is on the page, and a summary fixed at the opening state
		// would describe a session nobody had.
		Summary: func(unfolded bool) string { return diffCountsLine(rev, unfolded) },
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
// full. It never blocks: the key loop is what calls it, and difftui.Sync promises best-effort
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
	fmt.Fprintln(w, "Usage: magus diff [--generated] [--cost] [flags]")
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
	fmt.Fprintln(w, "--cost can tell you what changed after you read it. Name the paths you read")
	fmt.Fprintln(w, "to record just those - read them in whatever editor or pager you already")
	fmt.Fprintln(w, "use - or pass none to cover the whole changeset. Stepping a file through in")
	fmt.Fprintln(w, "--tui records it too. Editing a file afterwards voids its receipt.")
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
	fmt.Fprintln(w, "  --cost        append what landing this costs: which projects rebuild, who")
	fmt.Fprintln(w, "                owns them, an estimate from recorded run times, what the")
	fmt.Fprintln(w, "                advisors say, and which notes anchor what you changed.")
	fmt.Fprintln(w, "                It gates nothing and changes no exit code.")
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
// pre is nil unless --preflight was passed, and nil prints nothing at all: the report above
// it must not change shape because a second one was appended.
func printDiffText(rev types.Diff, showGenerated bool, link func(string) string, pre *diffPreflight) error {
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
		for _, line := range preflightLines(*pre) {
			fmt.Println(line)
		}
	}

	// The same changeset in the console's Diff surface. The link carries no token (see
	// printJobWatchHint), so the terminal check is no longer a secrecy measure - it is an
	// invitation to go look at something, and a pipe is not a person. Keeping it means
	// `magus diff > file` stays data.
	if tty.IsTerminalWriter(os.Stdout, tty.SystemProbe) {
		if u := consoleDiffURL(); u != "" {
			fmt.Printf("\nopen in console: %s\n%s\n", u, authHint)
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
// diffReport is the changeset plus its preflight, the document --preflight serves on the
// structured path.
//
// It exists ONLY under the flag. Without it the emitted value is types.Diff exactly as
// before, so nothing reading `magus diff -o json` today meets a key it has never seen.
type diffReport struct {
	types.Diff `yaml:",inline"`
	Preflight  *diffPreflight `json:"preflight,omitempty" yaml:"preflight,omitempty"`
}

// diffPreflight is what a disposer should know before landing: the consequences a file list
// structurally cannot show.
//
// Every field distinguishes "nothing" from "nobody measured", the same refusal types.DiffFile
// makes with its pointers. An absent cost is a workspace with no run history, and rendering it
// as zero would tell a reader the build is free.
type diffPreflight struct {
	Reach     *preflightReach  `json:"reach,omitempty"     yaml:"reach,omitempty"`
	Ownership []preflightOwner `json:"ownership,omitempty" yaml:"ownership,omitempty"`
	Cost      *preflightCost   `json:"cost,omitempty"      yaml:"cost,omitempty"`
	Advisors  []adviceSection  `json:"advisors,omitempty"  yaml:"advisors,omitempty"`
	// AdvisorNotes names each advisor that could not run. Surfaced rather than swallowed, so
	// an empty advisor list reads as "they all passed" only when it is one.
	AdvisorNotes []string `json:"advisor_notes,omitempty" yaml:"advisor_notes,omitempty"`
	// AdvisorBase qualifies everything the advisors said. nil when the backend cannot date a
	// revision, which is a different fact from a base that is merely old.
	AdvisorBase *preflightAdvisorBase `json:"advisor_base,omitempty" yaml:"advisor_base,omitempty"`
	Anchors     []anchorHit           `json:"anchors,omitempty"      yaml:"anchors,omitempty"`
	Rationale   []rationaleHit        `json:"rationale,omitempty"    yaml:"rationale,omitempty"`
	Review      *preflightReview      `json:"review,omitempty"       yaml:"review,omitempty"`
}

// preflightAdvisorBase is the revision the advisors compared against, and how current this
// clone's copy of it is.
//
// It is stated ONCE for the whole set rather than per section: a local run never fetches, so
// the caveat is identical under every advisor, and repeating it ten times is how a reader
// learns to skip it.
type preflightAdvisorBase struct {
	// Ref is the revision as the advisors spell it, so a reader can run the same comparison
	// by hand.
	Ref string `json:"ref" yaml:"ref"`
	// Tip is when Ref's commit was authored, RFC3339, and empty when Ref names nothing in
	// this clone. The AGE a reader sees is derived from this at render rather than stored:
	// a report teed to a file and read tomorrow must not still claim today's number.
	Tip string `json:"tip,omitempty" yaml:"tip,omitempty"`
}

// preflightReach is the blast radius: what was edited, and what rebuilds because of it.
type preflightReach struct {
	Seeds    int                `json:"seeds"              yaml:"seeds"`
	Rebuilds int                `json:"rebuilds"           yaml:"rebuilds"`
	Projects []preflightProject `json:"projects,omitempty" yaml:"projects,omitempty"`
}

type preflightProject struct {
	Path  string `json:"path"            yaml:"path"`
	Seed  bool   `json:"seed"            yaml:"seed"`
	Files int    `json:"files,omitempty" yaml:"files,omitempty"`
}

// preflightOwner is one project in reach and who has been changing it.
type preflightOwner struct {
	Project      string `json:"project"                yaml:"project"`
	Primary      string `json:"primary"                yaml:"primary"`
	PrimaryShare int    `json:"primary_share"          yaml:"primary_share"` // percent
	Authors      int    `json:"authors"                yaml:"authors"`
	BusFactor1   bool   `json:"bus_factor_1,omitempty" yaml:"bus_factor_1,omitempty"`
}

// preflightCost is the estimated cost of rebuilding the reach, from recorded run durations.
type preflightCost struct {
	TotalMs  int64                  `json:"total_ms"           yaml:"total_ms"`
	Projects []preflightCostProject `json:"projects,omitempty" yaml:"projects,omitempty"`
}

type preflightCostProject struct {
	Project string `json:"project" yaml:"project"`
	Target  string `json:"target"  yaml:"target"`
	Ms      int64  `json:"ms"      yaml:"ms"`
	// Samples is how many runs the estimate rests on, carried so a reader can weigh it. An
	// estimate from three runs and one from three hundred print the same duration otherwise.
	Samples int     `json:"samples"           yaml:"samples"`
	HitRate float64 `json:"hit_rate,omitempty" yaml:"hit_rate,omitempty"`
}

// preflightCostTargets is the target a project's rebuild is estimated by, most representative
// first. ci is the whole gate; test is the bulk of it where no ci target is declared.
var preflightCostTargets = []string{"ci", "test"}

// preflightMinSamples mirrors the tier-3 gate inside forecast.resolvePrediction: below it the
// prediction is the workspace fallback rather than a measurement of this project, and printing
// that as an estimate would be inventing a number.
const preflightMinSamples = 3

// preflightListCap bounds each section's list. A reach of sixty projects is a real answer and
// an unreadable one; the remainder is reported rather than truncated in silence.
const preflightListCap = 10

// collectPreflight joins the lenses the report needs onto an already-annotated changeset.
//
// Every lens is best-effort and every failure degrades to that lens's empty form. A preflight
// that refuses to print because the symbol index is cold or no daemon is running is a
// preflight nobody runs, and this surface reports context rather than passing judgement.
func collectPreflight(ctx context.Context, m *magus.Magus, rootOverride string, rev types.Diff) diffPreflight {
	p := diffPreflight{Reach: preflightReachOf(rev)}

	// The same bounded git-log walk annotateDiff pays for the churn lenses, so the two
	// sections of one report cannot describe two different windows of history.
	if own, err := m.Ownership(ctx, types.InsightOptions{Commits: diffHistoryCommits}); err == nil {
		p.Ownership = preflightOwnersOf(own, rev.AffectedProjects)
	}

	if path := globalCfg.HistoryPath; path != "" {
		var h forecast.History
		if err := h.Load(ctx, path); err == nil {
			p.Cost = preflightCostOf(&h, rev.AffectedProjects)
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
	p.AdvisorBase = preflightAdvisorBaseOf(ctx, m, base)

	// rootOverride, not m.Root(): the workspace loaders are once-per-process and keyed on
	// the override they were first handed, so the anchors' graph load must spell the root
	// exactly as diffCmd's own load did.
	p.Anchors = preflightAnchors(ctx, rootOverride, diffPaths(rev), diffSymbolIDs(rev))
	p.Rationale = collectRationale(m.Root(), rev)
	var requiredIn func(string) bool
	if ws, werr := inspectWorkspace(ctx, rootOverride); werr == nil {
		requiredIn = reviewRequiredMatcher(ws)
	}
	p.Review = collectReview(rev, requiredIn, bulkReasons(m.CacheDir(), rev))
	return p
}

// preflightReachOf renders what types.Diff has carried since the impact join landed and
// nothing has ever printed: which projects were edited, and which merely rebuild.
//
// nil when the closure is empty, which is a real state - a change entirely outside every
// project directory seeds nothing.
func preflightReachOf(rev types.Diff) *preflightReach {
	if len(rev.AffectedProjects) == 0 {
		return nil
	}
	r := &preflightReach{Seeds: len(rev.SeedProjects), Rebuilds: len(rev.AffectedProjects)}
	for _, p := range rev.AffectedProjects {
		r.Projects = append(r.Projects, preflightProject{Path: p.Path, Seed: p.Seed, Files: len(p.Files)})
	}
	return r
}

// preflightOwnersOf joins the ownership lens onto the reach, in reach order.
//
// Projects the lens has nothing to say about are DROPPED rather than listed with an empty
// author: a project with no commits in the window has no owner to name, and a blank name
// beside a path reads as a lookup that failed.
func preflightOwnersOf(own types.OwnershipOutput, affected []types.ImpactProject) []preflightOwner {
	byPath := make(map[string]types.OwnershipEntry, len(own.Projects))
	for _, e := range own.Projects {
		byPath[e.Path] = e
	}
	var out []preflightOwner
	for _, p := range affected {
		e, ok := byPath[p.Path]
		if !ok || e.Primary == "" {
			continue
		}
		out = append(out, preflightOwner{
			Project:      p.Path,
			Primary:      e.Primary,
			PrimaryShare: e.PrimaryShare,
			Authors:      e.Authors,
			BusFactor1:   e.BusFactor1,
		})
	}
	return out
}

// preflightCostOf sums the recorded history's per-project prediction over the reach.
//
// nil when the history has nothing to say about ANY project in reach. That case matters more
// than the happy one: forecast falls back to a workspace-wide default and then to a compiled-in
// constant, so a total is always computable and a total computed from those is a fabrication
// wearing a duration. Only projects with enough samples for forecast's own project tier are
// counted, and a reach whose projects all fall short reports no history at all.
func preflightCostOf(h *forecast.History, affected []types.ImpactProject) *preflightCost {
	c := &preflightCost{}
	for _, p := range affected {
		target, stats, ok := preflightCostTarget(h, p)
		if !ok {
			continue
		}
		// Tags are derived from the changed files inside the project, which is what selects
		// forecast's per-subdirectory bucket over the project-wide percentile. A transitively
		// affected project has none, and Tags answers "transitive" for it.
		d := h.PredictDuration(p.Path, target, forecast.Tags(p.Path, p.Files))
		c.TotalMs += d.Milliseconds()
		c.Projects = append(c.Projects, preflightCostProject{
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

// preflightCostTarget picks the target a project's rebuild is estimated by, and reports false
// when the history has no measurement of this project worth quoting.
func preflightCostTarget(h *forecast.History, p types.ImpactProject) (string, forecast.Stats, bool) {
	targets, ok := h.Projects[p.Path]
	if !ok {
		return "", forecast.Stats{}, false
	}
	for _, name := range preflightCostTargets {
		s, timed := targets[name]
		if timed && s.Samples >= preflightMinSamples && s.P75Ms > 0 {
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

// preflightLines renders the report, one claim per line, in the same count-then-list shape
// the file list above it uses.
//
// Lines rather than prints, so every empty form is testable without a terminal - and the empty
// forms are the half that matters. Each one says what was not measured and what would measure
// it, because a silent section reads as a clean bill of health.
func preflightLines(p diffPreflight) []string {
	out := []string{"PREFLIGHT - what landing this costs, and who else it touches", ""}
	sections := [][]string{
		preflightReachLines(p.Reach),
		preflightOwnershipLines(p.Ownership),
		preflightCostLines(p.Cost),
		preflightAdvisorLines(p.Advisors, p.AdvisorNotes, p.AdvisorBase),
		preflightAnchorLines(p.Anchors),
		preflightRationaleLines(p.Rationale),
		preflightReviewLines(p.Review),
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

func preflightReachLines(r *preflightReach) []string {
	if r == nil {
		return []string{"REACH: no project contains a changed file, so nothing rebuilds"}
	}
	out := []string{fmt.Sprintf("REACH: %d project%s edited, %d project%s rebuild",
		r.Seeds, pluralSuffix(r.Seeds, "", "s"), r.Rebuilds, pluralSuffix(r.Rebuilds, "", "s"))}
	for _, p := range preflightCap(r.Projects) {
		if p.Seed {
			out = append(out, fmt.Sprintf("      %s - edited, %d file%s", p.Path, p.Files, pluralSuffix(p.Files, "", "s")))
			continue
		}
		out = append(out, fmt.Sprintf("      %s - rebuilds because it depends on one that was", p.Path))
	}
	return append(out, preflightMoreLine(len(r.Projects))...)
}

func preflightOwnershipLines(owners []preflightOwner) []string {
	if len(owners) == 0 {
		return []string{"OWNERSHIP: no commit history in the window, so no owner is named"}
	}
	out := []string{"OWNERSHIP: who has been changing the projects in reach"}
	for _, o := range preflightCap(owners) {
		line := fmt.Sprintf("      %s mostly %s (%d%%), %d author%s",
			o.Project, o.Primary, o.PrimaryShare, o.Authors, pluralSuffix(o.Authors, "", "s"))
		if o.BusFactor1 {
			// The bus factor is the whole point of the lens, so it is stated rather than left
			// to be inferred from "1 author".
			line += " - BUS FACTOR 1"
		}
		out = append(out, line)
	}
	return append(out, preflightMoreLine(len(owners))...)
}

func preflightCostLines(c *preflightCost) []string {
	if c == nil {
		return []string{
			"COST: no run history yet, so there is nothing to estimate from",
			"      Run `magus affected ci` once and the next preflight can price this.",
		}
	}
	out := []string{fmt.Sprintf(
		"COST: ~%s to rebuild the reach (history-based estimate: the p75 of past runs, discounted by the cache hit rate they recorded)",
		preflightDuration(c.TotalMs))}
	for _, p := range preflightCap(c.Projects) {
		line := fmt.Sprintf("      %s %s ~%s (%d run%s)",
			p.Project, p.Target, preflightDuration(p.Ms), p.Samples, pluralSuffix(p.Samples, "", "s"))
		if p.HitRate > 0 {
			line += fmt.Sprintf(", %d%% cache hits", int(p.HitRate*100+0.5))
		}
		out = append(out, line)
	}
	return append(out, preflightMoreLine(len(c.Projects))...)
}

// preflightAdvisorBaseOf dates this clone's copy of the ref the advisors compared against.
//
// nil whenever the answer would be a guess: no VCS, or a backend that cannot date a
// revision. A ref the clone does not HAVE is not that case - it resolves to a
// preflightAdvisorBase with no Tip, because "you have never fetched this" is the single most
// useful thing the report can say about why nine advisors went quiet.
func preflightAdvisorBaseOf(ctx context.Context, m *magus.Magus, base string) *preflightAdvisorBase {
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
		return &preflightAdvisorBase{Ref: ref}
	}
	return &preflightAdvisorBase{Ref: ref, Tip: tip.Format(time.RFC3339)}
}

// preflightAdvisorBaseLine states what the advisors measured against, and how old it is.
//
// The age is computed here rather than carried, so it describes when the report is READ.
// An unparsable Tip degrades to naming the ref: the ref alone is still true, and a
// malformed date is not worth losing the rest of the line over.
func preflightAdvisorBaseLine(b *preflightAdvisorBase) string {
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
		b.Ref, preflightAge(time.Since(tip)), refresh)
}

// preflightAge renders a duration at one unit of precision. A reader deciding whether to
// fetch needs the order of magnitude ("3 days"), and "3 days 4 hours 11 minutes" buries it.
func preflightAge(d time.Duration) string {
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

func preflightAdvisorLines(sections []adviceSection, failed []string, base *preflightAdvisorBase) []string {
	// Prepended to whichever headline follows, including "nothing to report": a clean set
	// measured against a ref from last week is the case where the caveat matters MOST, and
	// hanging it off a finding count would drop it exactly there.
	var out []string
	if line := preflightAdvisorBaseLine(base); line != "" {
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

func preflightAnchorLines(hits []anchorHit) []string {
	if len(hits) == 0 {
		return []string{"ANCHORS: no note anchors a changed file or symbol"}
	}
	out := []string{fmt.Sprintf("ANCHORS: %d note%s anchored to what you changed",
		len(hits), pluralSuffix(len(hits), "", "s"))}
	for _, h := range preflightCap(hits) {
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
	return append(out, preflightMoreLine(len(hits))...)
}

// preflightCap bounds one section's list; preflightMoreLine reports what it left off. They
// are separate because the remainder is a line in the section's own indentation, not an entry
// in the list it describes.
func preflightCap[T any](xs []T) []T {
	if len(xs) <= preflightListCap {
		return xs
	}
	return xs[:preflightListCap]
}

func preflightMoreLine(n int) []string {
	if n <= preflightListCap {
		return nil
	}
	return []string{fmt.Sprintf("      and %d more", n-preflightListCap)}
}

// preflightDuration renders an estimate at the precision it deserves. Seconds is the floor:
// this is a p75 of past runs, and a millisecond figure would claim an accuracy it has not got.
func preflightDuration(ms int64) string {
	d := (time.Duration(ms) * time.Millisecond).Round(time.Second)
	if d < time.Second {
		return "<1s"
	}
	return d.String()
}

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
