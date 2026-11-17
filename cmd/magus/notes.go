package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive/difftui"
	store "github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/text/plural"
)

// There is deliberately no `put` subcommand, and adding one would defeat the feature.
//
// A note's only provenance is the person who wrote it: nothing in the repo can corroborate
// it, now or in a year. That is exactly why the write path is a human in an editor and not
// an API. `put` is the affordance an agent reaches for first, so its absence is the design,
// not an omission: `edit` opens $EDITOR and gets out of the way.
func notesCommand(ctx context.Context, root string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		commands.WriteNotesUsage(os.Stderr)
		return nil
	}
	switch args[0] {
	case "ls":
		return notesList(ctx, root, args[1:])
	case "get":
		return notesGet(ctx, root, args[1:])
	case "edit":
		return notesEdit(ctx, root, args[1:])
	case "verify":
		return notesVerify(ctx, root, args[1:])
	case "capture":
		return notesCapture(ctx, root, args[1:])
	case "put":
		// Named rather than left to the generic unknown-subcommand error, because the
		// reason it is absent is the whole point of the store and is worth stating at the
		// moment someone reaches for it.
		return exit.Usagef("magus notes: there is no `put`; a note is written by a person, not a program (run `%s`)", hint.NotesEdit.With("<name>"))
	default:
		return exit.Usagef("magus notes: unknown subcommand %q (want ls, get, edit, verify, or capture)", args[0])
	}
}

// notesScopeFlags binds the pair of filters every subcommand accepts.
func notesScopeFlags(fs *flag.FlagSet) (*bool, *bool) {
	return fs.Bool("shared", false, "Only notes committed to this repository (your team has these)"),
		fs.Bool("private", false, "Only your own notes")
}

// negateScopeFlags binds --no-shared and --no-private for the subcommands whose
// declared flags carry the pair.
func negateScopeFlags(fs *flag.FlagSet, shared, private *bool) {
	command.NegatedBoolVar(fs, shared, "shared")
	command.NegatedBoolVar(fs, private, "private")
}

func notesList(ctx context.Context, root string, args []string) error {
	var onlyShared, onlyPrivate, reproducible *bool
	var tags tagList
	_, err := commandParse("notes ls", args, func(fs *flag.FlagSet) {
		onlyShared, onlyPrivate = notesScopeFlags(fs)
		reproducible = fs.Bool("reproducible", false, "Omit values that are real but unstable (file mtimes, absolute paths), so the output can be committed")
		negateScopeFlags(fs, onlyShared, onlyPrivate)
		command.NegatedBoolVar(fs, reproducible, "reproducible")
		fs.Var(&tags, "tag", "Only notes carrying this tag; repeatable, a note must carry every one (gap/open lists open gaps)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus notes ls [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "List every note with its anchors. Warnings identify broken notes without hiding them.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	scope, err := store.ScopeFromFlags(*onlyShared, *onlyPrivate)
	if err != nil {
		return err
	}
	stores, err := review.ResolveNoteStores(root, noteDirs(), scope)
	if err != nil {
		return err
	}
	found, issues, listed, err := review.ListNotes(root, stores, tags, *reproducible)
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputName {
		names := make([]string, len(found))
		for i, n := range found {
			names[i] = n.Name
		}
		if err := emitNames(os.Stdout, names); err != nil {
			return err
		}
		return store.IssuesError(issues, false)
	}
	if opts.Format != outputText {
		if err := emitFormatted(opts, store.ListOutput{Stores: listed, Notes: found, Issues: issues}); err != nil {
			return err
		}
		// Never strict: `ls` is a listing, and a listing that exits non-zero because the
		// store has a repair pending is one nothing can pipe. The gate lives in `verify`.
		return store.IssuesError(issues, false)
	}
	if len(found) == 0 {
		fmt.Printf("No notes yet. Write the first one with `%s`.\n", hint.NotesEdit.With("<name>"))
	} else {
		finder := &review.NotesFinder{Root: root, Load: notesResolver}
		for _, n := range found {
			fmt.Printf("%-8s %s  %s  (%s)\n", n.Scope, n.Name, n.Title, store.AnchorSummary(n.Note))
			for _, line := range finder.Block(ctx, n.Scope, n.Note) {
				fmt.Println("  " + line)
			}
		}
	}
	return store.PrintIssues(issues, false)
}

func notesGet(ctx context.Context, root string, args []string) error {
	var getShared, getPrivate *bool
	pos, err := commandParse("notes get", args, func(fs *flag.FlagSet) {
		getShared, getPrivate = notesScopeFlags(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus notes get <name> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Show one note: its anchors, its prose, and when it was last touched.")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("magus notes get: requires exactly one note name")
	}
	scope, err := store.ScopeFromFlags(*getShared, *getPrivate)
	if err != nil {
		return err
	}
	stores, err := review.ResolveNoteStores(root, noteDirs(), scope)
	if err != nil {
		return err
	}
	n, st, err := review.FindNote(stores, pos[0])
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputName {
		return emitNames(os.Stdout, []string{n.Name})
	}
	if opts.Format != outputText {
		return emitFormatted(opts, store.ScopedNote{
			Note:     n,
			Scope:    string(st.Scope),
			Path:     store.Locate(root, st.Scope, n),
			Modified: store.NoteModified(n, false),
		})
	}
	store.PrintNote(n, st.Scope)
	finder := &review.NotesFinder{Root: root, Load: notesResolver}
	for _, line := range finder.Block(ctx, string(st.Scope), n) {
		fmt.Println(line)
	}
	return nil
}

// notesEdit opens the note in the author's own editor, the way `git commit` does.
//
// This is the entire "no text editor" story. Notes are markdown files at real paths, so the
// tool that edits them is the one the author already uses (vim, VS Code, Obsidian, anything)
// and magus writes no editor of its own.
func notesEdit(ctx context.Context, root string, args []string) error {
	var editShared, editPrivate *bool
	var anchors stringList
	pos, err := commandParse("notes edit", args, func(fs *flag.FlagSet) {
		editShared, editPrivate = notesScopeFlags(fs)
		fs.Var(&anchors, "anchor", "Anchor as kind:target (repeatable), e.g. --anchor symbol:'m pkg/Type#Method().' - required when writing a NEW note from stdin")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus notes edit <name> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Open one note in $VISUAL (else $EDITOR). A name that does not exist yet is")
			fmt.Fprintln(os.Stderr, "created from a scaffold, so the first save already has a valid shape.")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("magus notes edit: requires exactly one note name")
	}
	scope, err := store.ScopeFromFlags(*editShared, *editPrivate)
	if err != nil {
		return err
	}
	stores, err := review.ResolveNoteStores(root, noteDirs(), scope)
	if err != nil {
		return err
	}
	// An existing note is edited where it already lives; a NEW one defaults to the first
	// declared store (shared when both exist), because team knowledge is the case worth
	// making easy and a private note is the deliberate exception.
	target := stores[0]
	// existing is the file the note was READ from, and using it is what makes "edited where
	// it already lives" true. store.Path answers a different question (where a note of this
	// name WOULD go), and for a note whose declared id no longer matches its filename the two
	// answers differ, so editing one opened a blank scaffold at the id's path and left the
	// real note untouched beside it.
	var existing string
	switch found, st, err := review.FindNote(stores, pos[0]); {
	case err == nil:
		target, existing = st, found.Path
	case errors.Is(err, review.ErrAmbiguousNote):
		// Never guess which store was meant: the two mean different things to a reader,
		// and silently editing the shared one is the mistake this error exists to prevent.
		return err
	case errors.Is(err, review.ErrUnreadableNote):
		// The name is TAKEN, just not readable. Falling through would treat it as free
		// and scaffold a second copy beside the broken one, so refuse and say what to fix.
		return fmt.Errorf("fix the file's permissions or contents, then edit it again: %w", err)
	}
	dir := target.Dir
	path := existing
	if path == "" {
		path, err = store.Path(dir, pos[0])
		if err != nil {
			return fmt.Errorf("magus notes edit: %w", err)
		}
	}
	// A pipe is the non-interactive way to author a note: `pg_dump ... | magus notes edit`
	// is the same act as opening the editor, just without a terminal to open one in. It is
	// still a PERSON writing: an agent reaching for this is denied by the command guard,
	// which is where the write boundary is enforced for shell commands (the file-write rule
	// only sees file writes, and a pipe is not one).
	if !termkit.StdinIsTerminal() {
		return notesWriteFromStdin(ctx, root, dir, target, pos[0], path, anchors)
	}
	editor := strings.TrimSpace(store.FirstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR")))
	if editor == "" {
		return fmt.Errorf("magus notes edit: neither $VISUAL nor $EDITOR is set, set one to your editor, pipe the body in, or open %s directly", path)
	}
	// scaffolded holds the bytes Save just laid down, so the cleanup below can tell an
	// untouched placeholder from something the author started writing. Read back rather
	// than re-rendered: what was actually written is the only thing "untouched" can mean.
	var scaffolded []byte
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := store.Save(dir, store.Scaffold(pos[0])); err != nil {
			return fmt.Errorf("magus notes edit: %w", err)
		}
		scaffolded, _ = os.ReadFile(path)
	}
	// The editor owns the terminal: inherit all three streams and wait. `sh -c` so a
	// value like `code --wait` or `emacsclient -nw` works as written, which is what a
	// reader's $EDITOR usually is.
	cmd := exec.Command("sh", "-c", editor+" \"$1\"", "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		// An editor that never started (a bad $EDITOR, a missing binary) would otherwise
		// leave the placeholder behind: listed by `notes ls`, assembled into the graph,
		// and swept up by the next `git add`. Only a scaffold this run created is removed,
		// and only while it is still untouched: an unreadable write-back leaves len 0, which
		// removes nothing, because a file magus cannot prove is a placeholder is the author's.
		if len(scaffolded) != 0 {
			if body, rerr := os.ReadFile(path); rerr == nil && bytes.Equal(body, scaffolded) {
				_ = os.Remove(path)
			}
		}
		return fmt.Errorf("magus notes edit: %s: %w", editor, err)
	}
	// Report what the author just wrote rather than assuming it is well-formed: a note
	// that fails to parse is far cheaper to hear about now than at the next verify.
	n, err := store.Get(dir, pos[0])
	if err != nil {
		return fmt.Errorf("magus notes edit %q: %w", pos[0], err)
	}
	fmt.Printf("Saved %s [%s] (%s).\n", path, target.Scope, store.AnchorSummary(n))

	// Re-attestation, and the reason it lives HERE rather than inside verify: the person
	// just had this note and its anchors in front of them, so recording the anchored
	// code's current fingerprint is them saying "this still holds". Because notes are in
	// the checkout, that statement lands as a reviewed commit under their name.
	//
	// Verify never does this. A tool that re-records a fingerprint on its own clears real
	// drift without anyone reading the prose, which is the failure every anchoring product
	// in this space eventually shipped. It is also why the count is PRINTED: silently
	// accepting a drift flag would be the same failure wearing a human's name.
	res, err := notesResolver(ctx, root)
	if err != nil {
		// The note IS saved: that write already succeeded and is not undone by this. What
		// failed is the re-attestation, and reporting it as success would leave the author
		// believing their note is fingerprinted against today's code when it is not. So it
		// is a real error, with the saved path named so nobody goes looking for lost work.
		return fmt.Errorf("magus notes edit: saved %s, but its anchors could not be fingerprinted because the knowledge graph would not load, run `%s` again once it does: %w", path, hint.NotesEdit.With(pos[0]), err)
	}
	changed, err := store.RecordDigests(ctx, dir, pos[0], store.Revision(ctx, root), res.ForScope(string(target.Scope)))
	if err != nil {
		return fmt.Errorf("magus notes edit %q: recording anchors: %w", pos[0], err)
	}
	if changed != 0 {
		fmt.Printf("Recorded the anchored code's current fingerprint for %d anchor%s: this note now reads as reviewed against the code as it is today.\n",
			changed, plural.Word(changed, "", "s"))
	}
	return nil
}

func notesVerify(ctx context.Context, root string, args []string) error {
	var vShared, vPrivate *bool
	var strict bool
	_, err := commandParse("notes verify", args, func(fs *flag.FlagSet) {
		vShared, vPrivate = notesScopeFlags(fs)
		// Dangling only, and never drift. A dangling anchor is unambiguous, always fixable,
		// and its cause is in the diff that broke it: the properties a gate needs. Drift is
		// a judgment call whose base rate is low, and gating on judgment calls is how a
		// check earns a permanent `|| true`.
		fs.BoolVar(&strict, "strict", false,
			"Exit non-zero on a dangling anchor as well as an invalid note (for CI). Drift never fails.")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus notes verify [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Check every note for malformed frontmatter, an invalid shape, and anchors that")
			fmt.Fprintln(os.Stderr, "no longer resolve. Errors exit non-zero.")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	scope, err := store.ScopeFromFlags(*vShared, *vPrivate)
	if err != nil {
		return err
	}
	stores, err := review.ResolveNoteStores(root, noteDirs(), scope)
	if err != nil {
		return err
	}
	var report store.Verification
	for _, st := range stores {
		r, err := store.Verify(st.Dir)
		if err != nil {
			return err
		}
		report.Notes += r.Notes
		report.Issues = append(report.Issues, r.Issues...)
	}
	// Anchor resolution needs the graph, and a workspace that cannot be loaded is a
	// reason to say less rather than to fail: the structural check above still stands on
	// its own, and a note store is readable long before the graph is buildable.
	// Anchor checking is half of what verify means, so failing to do it cannot report
	// [pass]. A gate wired to this command would otherwise read green in exactly the
	// situation where nothing was checked, which is worse than a red gate.
	res, resErr := notesResolver(ctx, root)
	if resErr != nil {
		return fmt.Errorf("magus notes verify: %d note(s) are structurally valid, but their anchors were NOT checked because the knowledge graph would not load: %w", report.Notes, resErr)
	}
	for _, st := range stores {
		anchorIssues, err := store.AnchorIssues(ctx, st.Dir, res.ForScope(string(st.Scope)))
		if err != nil {
			return err
		}
		report.Issues = append(report.Issues, anchorIssues...)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	// A verification has no name of its own, so the one-token-per-line identity is what
	// the reader would act on next: the note each issue is about (its file, for an issue
	// about the store rather than a note). A clean store prints nothing.
	if opts.Format == outputName {
		if err := emitNames(os.Stdout, store.IssueSubjects(report.Issues)); err != nil {
			return err
		}
		return store.IssuesError(report.Issues, strict)
	}
	if opts.Format != outputText {
		if err := emitFormatted(opts, report); err != nil {
			return err
		}
		return store.IssuesError(report.Issues, strict)
	}
	if len(report.Issues) == 0 {
		fmt.Printf("[pass] notes: %d verified\n", report.Notes)
		return nil
	}
	if err := store.PrintIssues(report.Issues, strict); err != nil {
		return err
	}
	return nil
}

// notesResolver loads the graph once and returns a resolver over it. The caller binds a
// scope with ForScope before checking a store's notes; the type is concrete rather than
// store.Resolver precisely so that step cannot be skipped.
func notesResolver(ctx context.Context, root string) (knowledge.NoteResolver, error) {
	// includeSymbols: a note anchored to a symbol is the whole point of symbol anchors,
	// and without the symbol shards loaded every one of them would report as dangling.
	g, err := loadKnowledgeGraph(ctx, root, false, false, true)
	if err != nil {
		return knowledge.NoteResolver{}, err
	}
	return knowledge.NewNoteResolver(root, g), nil
}

// notesWriteFromStdin authors a note from piped input instead of an editor.
//
// The prose is stdin; everything structural comes from flags, because a pipe carries no
// frontmatter and inventing one would put words in the author's mouth. An EXISTING note
// keeps its title and anchors and has only its body replaced, so re-piping is a rewrite of
// the prose rather than a silent reshaping of what the note is about.
func notesWriteFromStdin(ctx context.Context, root, dir string, target review.NoteStore, name, path string, anchors []string) error {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("magus notes edit: reading stdin: %w", err)
	}
	if strings.TrimSpace(string(body)) == "" {
		return errors.New("magus notes edit: stdin was empty, a note with no prose is not a note")
	}

	n, err := store.Get(dir, name)
	switch {
	case err == nil:
		// An existing note keeps what it is ABOUT and has only its prose replaced, so
		// re-piping is a rewrite rather than a reshaping. Appending --anchor here would
		// accumulate duplicates on every run, and each duplicate re-reports every dangling
		// or drifted finding for the rest of the note's life.
		if len(anchors) != 0 {
			return fmt.Errorf("magus notes edit: %q already exists, so --anchor is refused, its anchors are what the note is about and piping new prose does not change that, edit the note with `%s` (no pipe) to change them", name, hint.NotesEdit.With(name))
		}
	case errors.Is(err, os.ErrNotExist):
		n = store.Note{Name: name, Title: strings.ReplaceAll(name, "-", " ")}
		for _, a := range anchors {
			parsed, perr := store.ParseAnchor(a)
			if perr != nil {
				return fmt.Errorf("magus notes edit: %w", perr)
			}
			n.Anchors = append(n.Anchors, parsed)
		}
	default:
		return fmt.Errorf("magus notes edit: %q exists but could not be read, repair it before overwriting: %w", name, err)
	}
	if len(n.Anchors) == 0 {
		return errors.New("magus notes edit: a new note piped from stdin needs at least one --anchor (kind:target), an unanchored note is a diary entry nobody will find again")
	}
	n.Body = string(body)
	if err := store.Save(dir, n); err != nil {
		return fmt.Errorf("magus notes edit: %w", err)
	}
	fmt.Printf("Wrote %s [%s] (%s).\n", path, target.Scope, store.AnchorSummary(n))

	res, err := notesResolver(ctx, root)
	if err != nil {
		return fmt.Errorf("magus notes edit: wrote %s, but its anchors could not be fingerprinted because the knowledge graph would not load: %w", path, err)
	}
	changed, err := store.RecordDigests(ctx, dir, name, store.Revision(ctx, root), res.ForScope(string(target.Scope)))
	if err != nil {
		return fmt.Errorf("magus notes edit: wrote %s, but recording its anchors failed: %w", path, err)
	}
	if changed != 0 {
		fmt.Printf("Recorded the anchored code's current fingerprint for %d anchor%s.\n", changed, plural.Word(changed, "", "s"))
	}
	return nil
}

// --- capture -----------------------------------------------------------------------------
//
// `capture` is the one write path besides `edit`, and it is not the `put` this file refuses
// above. The distinction is what a caller supplies: `put` would take PROSE, and prose from a
// program is prose with nobody behind it. `capture` takes a REFERENCE to a conversation magus
// already holds, and magus renders the transcript itself. The worst a caller can cause is a
// faithful record of something that actually happened.
//
// It exists because a review thread is otherwise scattered. The changeset store persists the
// unsent remarks a person wrote, and the forge holds what colleagues said back; nothing joins
// them into one readable record that outlives the review. A running server holds MORE than the
// store does (an agent's remarks, and remarks already published), and capture says so when it
// had to read the store instead, because a transcript quietly missing half a conversation is
// worse than no transcript.

// notesCapture writes the current review thread into a store as one note.
func notesCapture(ctx context.Context, root string, args []string) error {
	var title, name string
	var tags tagList
	var anchors stringList
	var gap bool
	var shared, private *bool
	pos, err := commandParse("notes capture", args, func(fs *flag.FlagSet) {
		fs.StringVar(&title, "title", "", "Title for the note (defaults to naming the reviewed base)")
		fs.StringVar(&name, "name", "", "Note name (defaults to review-<patch digest>)")
		fs.Var(&tags, "tag", "Tag to set on the note; repeatable")
		fs.BoolVar(&gap, "gap", false, "Capture a harness gap: positional arguments are the refs it was seen in, and every --anchor must resolve")
		command.NegatedBoolVar(fs, &gap, "gap")
		fs.Var(&anchors, "anchor", "With --gap, what the gap is about (repeatable): symbol:<key>, file:<path>#<decl>, diagnostic:<code>, or any note anchor")
		shared, private = notesScopeFlags(fs)
		negateScopeFlags(fs, shared, private)
	})
	if err != nil {
		return err
	}
	if gap {
		return captureEnv(root).Gap(ctx, store.Gap{Title: title, Anchors: anchors, Refs: pos, Tags: tags}, name, *shared, *private)
	}
	if len(anchors) > 0 {
		return exit.Usagef("magus notes capture: --anchor needs --gap, a review thread is anchored by the files it discussed")
	}
	if len(pos) > 0 {
		return exit.Usagef("magus notes capture: unexpected argument %q; the thread to capture is the review under way", pos[0])
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("magus notes capture: %w", err)
	}
	sess, fromServer, err := review.CaptureSession(ctx, reviewServerGet, func(ctx context.Context) string {
		patch, _ := m.WorkingDiff(ctx, nil)
		return patch
	}, m.CacheDir())
	if err != nil {
		return fmt.Errorf("magus notes capture: %w", err)
	}
	// The colleagues' half. Read separately and never required: a review with no forge behind
	// it is the ordinary case, and the local conversation is worth keeping on its own.
	comments, partial := review.Remarks(ctx, reviewServerGet, m.ReviewOrigin(ctx), m.CacheDir())
	if len(sess.Comments) == 0 && len(comments) == 0 {
		return errors.New("magus notes capture: this review has no comments yet, and a transcript of an empty conversation is not worth a note")
	}

	target, err := captureEnv(root).Store(*shared, *private)
	if err != nil {
		return err
	}

	capture := review.CaptureFromSession(sess, comments, title, tags)
	noteName := name
	if noteName == "" {
		noteName = review.CaptureName(sess)
	}
	n, err := capture.Note(noteName)
	if err != nil {
		return fmt.Errorf("magus notes capture: %w", err)
	}
	if _, err := store.Get(target.Dir, noteName); err == nil {
		return fmt.Errorf("magus notes capture: %q already exists in the %s store, pass --name to keep both because overwriting it would destroy a transcript nothing can recreate", noteName, target.Scope)
	}
	if err := store.Save(target.Dir, n); err != nil {
		return fmt.Errorf("magus notes capture: %w", err)
	}
	// Read back rather than reporting the value just written. notePath needs Note.Path, which
	// is observed on read and empty on a note that was built; printing the built one names
	// the store directory instead of the file. The round trip also proves the transcript
	// parses as a note, which is worth one stat for something nothing can recreate.
	saved, err := store.Get(target.Dir, noteName)
	if err != nil {
		return fmt.Errorf("magus notes capture: wrote %q, but it does not read back as a note: %w", noteName, err)
	}
	// Every remark, both halves. Counting only the local ones would understate a transcript
	// whose most useful line came from somebody else.
	said := len(sess.Comments) + len(comments)
	fmt.Printf("Captured %d comment%s into %s [%s] (%s).\n",
		said, plural.Word(said, "", "s"),
		store.Locate(root, target.Scope, saved), target.Scope, store.AnchorSummary(saved))
	if line := review.NewRemarkLine(comments); line != "" {
		fmt.Println(line)
	}
	// Said out loud, because a transcript is exactly the artifact nobody re-checks. A capture
	// that quietly omitted part of the review would be discovered, if ever, by the person who
	// went looking for what a colleague said and concluded they had said nothing.
	if partial != "" {
		fmt.Printf("Part of the review could not be read, so this transcript is incomplete: %s\n", partial)
	}
	// Named for the same reason. The store keeps your unsent remarks and nothing else, so a
	// capture taken without a server is missing anything an agent said and anything already
	// published to the review, and the reader has to be told which record they are holding.
	if !fromServer {
		fmt.Println("Read from the local review store, so this holds your unsent remarks. A running server would also carry an agent's remarks and any already published.")
	}

	// Fingerprint the anchored files, exactly as a written note is fingerprinted on save. It
	// matters MORE here: a transcript is about code as it stood during one review, so the
	// question a reader will have later is whether it has moved since, and only a recorded
	// digest lets `notes verify` answer it. Without this the capture reports "unverified"
	// forever, which is the one verdict that means nothing was ever checked.
	res, err := notesResolver(ctx, root)
	if err != nil {
		return fmt.Errorf("magus notes capture: captured the thread, but its anchors could not be fingerprinted because the knowledge graph would not load: %w", err)
	}
	changed, err := store.RecordDigests(ctx, target.Dir, noteName, store.Revision(ctx, root), res.ForScope(string(target.Scope)))
	if err != nil {
		return fmt.Errorf("magus notes capture: captured the thread, but recording its anchors failed: %w", err)
	}
	if changed != 0 {
		fmt.Printf("Recorded the reviewed code's current fingerprint for %d anchor%s.\n", changed, plural.Word(changed, "", "s"))
	}
	return nil
}

// tagList collects a repeatable --tag flag.
type tagList []string

func (t *tagList) String() string { return strings.Join(*t, ",") }

func (t *tagList) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errors.New("tag must not be empty")
	}
	*t = append(*t, v)
	return nil
}

// noteDirs is the notes locations the loaded config declares.
func noteDirs() review.NoteDirs {
	declared := globalCfg.Knowledge.Notes
	return review.NoteDirs{Shared: declared.Shared, Private: declared.Private}
}

func captureEnv(root string) review.CaptureEnv {
	return review.CaptureEnv{Root: root, Dirs: noteDirs(), Load: notesResolver}
}

// reviewServerGet reads path from the running server with a short-lived token, which it
// revokes before returning.
func reviewServerGet(ctx context.Context, path string) ([]byte, bool) {
	addr := mcpAddrString()
	token, tokenID, ok := difftui.MintToken(ctx, cliTokens{}, addr, difftui.CallTokenTTL)
	if !ok {
		return nil, false
	}
	defer difftui.RevokeToken(ctx, cliTokens{}, tokenID)
	ctx, cancel := context.WithTimeout(ctx, difftui.AttachTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s%s", addr, path), nil)
	if err != nil {
		return nil, false
	}
	difftui.SetBearer(req, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	raw, err := io.ReadAll(resp.Body)
	return raw, err == nil
}
