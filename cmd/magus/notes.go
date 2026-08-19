package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/graph/knowledge"
	store "github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// There is deliberately no `put` subcommand, and adding one would defeat the feature.
//
// A note's only provenance is the person who wrote it: nothing in the repo can corroborate
// it, now or in a year. That is exactly why the write path is a human in an editor and not
// an API. `put` is the affordance an agent reaches for first, so its absence is the design,
// not an omission - `edit` opens $EDITOR and gets out of the way.
func notesCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		notesUsage()
		return nil
	}
	switch args[0] {
	case "ls":
		return notesList(root, args[1:])
	case "get":
		return notesGet(root, args[1:])
	case "edit":
		return notesEdit(ctx, root, args[1:])
	case "verify":
		return notesVerify(ctx, root, args[1:])
	case "put":
		// Named rather than left to the generic unknown-subcommand error, because the
		// reason it is absent is the whole point of the store and is worth stating at the
		// moment someone reaches for it.
		return usagef("magus notes: there is no `put`; a note is written by a person, not a program (run `magus notes edit <name>`)")
	default:
		return usagef("magus notes: unknown subcommand %q (want ls, get, edit, or verify)", args[0])
	}
}

func notesUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus notes <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Read the workspace's human-authored notes: prose a person wrote about the code,")
	fmt.Fprintln(os.Stderr, "anchored to graph entities but derived from none of them. Notes are committed to")
	fmt.Fprintln(os.Stderr, "the repository, so git attributes them and review sees them.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  ls       show notes and any repair warnings")
	fmt.Fprintln(os.Stderr, "  get      show one note")
	fmt.Fprintln(os.Stderr, "  edit     open one note in $VISUAL or $EDITOR (creates it if absent)")
	fmt.Fprintln(os.Stderr, "  verify   check malformed notes and anchors that no longer resolve")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Set knowledge.notes.shared (in the repo, your team gets it) or knowledge.notes.private")
	fmt.Fprintln(os.Stderr, "(anywhere, this machine only) in magus.yaml to enable a store. There is no `put`:")
	fmt.Fprintln(os.Stderr, "notes are written by people, in an editor, and committed under their own name.")
}

// notesDir resolves the declared store, turning the disabled case into a message that says
// how to enable it rather than an empty listing that looks like an empty store.
// notesStore is one resolved location plus the scope that says what putting a note there
// means. Both stores hold the same shape of note; the difference is entirely in who ends
// up able to read it, which is why the scope travels with every listing.
type notesStore struct {
	dir   string
	scope store.Scope
}

// notesStores resolves every declared location, in the order a reader should see them.
//
// Neither being declared is an error with instructions rather than an empty listing: an
// empty list would say "you have no notes" when the truth is "this workspace has nowhere
// to put one", and those call for completely different next actions.
func notesStores(root, only string) ([]notesStore, error) {
	var out []notesStore
	add := func(declared string, scope store.Scope) error {
		if only != "" && only != string(scope) {
			return nil
		}
		dir, err := store.Dir(root, scope, declared)
		if errors.Is(err, store.ErrDisabled) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("magus notes: %w", err)
		}
		out = append(out, notesStore{dir: dir, scope: scope})
		return nil
	}
	if err := add(globalCfg.Knowledge.Notes.Shared, store.ScopeShared); err != nil {
		return nil, err
	}
	if err := add(globalCfg.Knowledge.Notes.Private, store.ScopePrivate); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		if only != "" {
			return nil, fmt.Errorf("magus notes: this workspace declares no %s notes store; set knowledge.notes.%s in magus.yaml", only, only)
		}
		return nil, errors.New("magus notes: this workspace declares no notes store.\n" +
			"  knowledge.notes.shared   a directory IN the repo: your team gets these, and git records who wrote each one\n" +
			"  knowledge.notes.private  a directory anywhere: only this machine has them, and nothing attributes them")
	}
	return out, nil
}

// findNote locates one note across the declared stores. A name present in both is an
// error rather than a silent preference: the two mean different things to a reader, so
// guessing which was meant is the one thing that must not happen.
func findNote(stores []notesStore, name string) (store.Note, notesStore, error) {
	var found []notesStore
	var note store.Note
	for _, st := range stores {
		n, err := store.Get(st.dir, name)
		switch {
		case err == nil:
			note, found = n, append(found, st)
		case errors.Is(err, os.ErrNotExist):
			// Genuinely absent here; keep looking in the other store.
		default:
			// The note EXISTS and could not be read. Reporting that as "no such note"
			// sends the reader hunting for a missing file, and lets `notes edit` scaffold
			// a second copy in the other store on top of the broken one.
			return store.Note{}, notesStore{}, fmt.Errorf("magus notes: %q exists in the %s store but could not be read: %w", name, st.scope, err)
		}
	}
	switch len(found) {
	case 0:
		return store.Note{}, notesStore{}, fmt.Errorf("magus notes: no note named %q", name)
	case 1:
		return note, found[0], nil
	default:
		return store.Note{}, notesStore{}, fmt.Errorf("magus notes: %q exists in both stores; say which with --shared or --private: %w", name, errAmbiguousNote)
	}
}

// errAmbiguousNote marks a name present in both stores, so a caller can tell it apart from
// "not found" instead of treating both as "nothing here".
var errAmbiguousNote = errors.New("the name exists in more than one store")

// notesScopeFlags binds the pair of filters every subcommand accepts.
func notesScopeFlags(fs *flag.FlagSet) (*bool, *bool) {
	return fs.Bool("shared", false, "Only notes committed to this repository (your team has these)"),
		fs.Bool("private", false, "Only your own notes (this machine only; nothing attributes them)")
}

func notesScope(shared, private bool) (string, error) {
	switch {
	case shared && private:
		return "", errors.New("magus notes: --shared and --private are opposites; pass neither to see both")
	case shared:
		return knowledge.ScopeShared, nil
	case private:
		return knowledge.ScopePrivate, nil
	default:
		return "", nil
	}
}

// scopedNote is a note plus where it lives, so a listing can never present a private
// note as if the team had it.
type scopedNote struct {
	store.Note
	Scope string `json:"scope" yaml:"scope"`
	// Path is where the note lives, and it is here so a structured listing is enough to
	// ACT on: without it a consumer knows a note exists and cannot open it, which is the
	// difference between a report and an export. Same rendering as the notes service
	// (internal/handler/notes.Service.notePath), so the CLI and the RPC name one file the
	// same way rather than each having its own idea of where a note is.
	Path string `json:"path" yaml:"path"`
	// Modified shadows the embedded store.Note.Modified so an export can OMIT it instead of
	// emitting a zero timestamp. It is the file's mtime, which git rewrites on checkout, so
	// a committed export that carries it reports drift on every fresh clone with the
	// timestamp as the entire diff. A pointer is what makes "absent" expressible.
	Modified *time.Time `json:"modified,omitempty" yaml:"-"`
}

// noteModified picks the mtime to serialize. Under --reproducible it is dropped rather than
// zeroed: a zero time is a value a reader can misread as "never touched", where an absent
// field says the export chose not to answer.
func noteModified(n store.Note, reproducible bool) *time.Time {
	if reproducible {
		return nil
	}
	m := n.Modified
	return &m
}

// notePath renders where a note lives: workspace-relative for a shared note, absolute for a
// private one. The split follows the scope - a private store sits outside the workspace, so a
// relative path would be a lie about a file the reader still has to find.
//
// The path is the one the note was READ from (store.Note.Path), never one rebuilt from its
// name: a declared id is deliberately independent of the filename, so rebuilding names a
// file that stops existing the moment someone renames the note.
func notePath(root string, st notesStore, n store.Note) string {
	if st.scope != store.ScopeShared {
		return n.Path
	}
	return relativeToRoot(root, n.Path)
}

// relativeToRoot renders a path inside the workspace as workspace-relative, and leaves one
// outside it alone - a private store sits anywhere on disk, and a ../../.. walk out of the
// checkout names the file no more usefully than the absolute path does.
func relativeToRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}

// notesStoreOutput names one declared store in a structured listing. Only DECLARED stores
// reach a listing at all (notesStores skips the rest), so a consumer reading this knows the
// location exists; an empty Notes list against a non-empty Stores list is the "declared and
// empty" case, which is a different fact from "this workspace has nowhere to put a note".
type notesStoreOutput struct {
	Scope string `json:"scope" yaml:"scope"`
	Path  string `json:"path" yaml:"path"`
}

type notesListOutput struct {
	Stores []notesStoreOutput `json:"stores"`
	Notes  []scopedNote       `json:"notes"`
	Issues []store.Issue      `json:"issues"`
}

func notesList(root string, args []string) error {
	var onlyShared, onlyPrivate, reproducible *bool
	_, err := cmdParse("notes ls", args, func(fs *flag.FlagSet) {
		onlyShared, onlyPrivate = notesScopeFlags(fs)
		reproducible = fs.Bool("reproducible", false, "Omit values that are real but unstable (file mtimes, absolute paths), so the output can be committed")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus notes ls [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "List every note with its anchors. Warnings identify broken notes without hiding them.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	scope, err := notesScope(*onlyShared, *onlyPrivate)
	if err != nil {
		return err
	}
	stores, err := notesStores(root, scope)
	if err != nil {
		return err
	}
	var found []scopedNote
	var issues []store.Issue
	listed := make([]notesStoreOutput, 0, len(stores))
	for _, st := range stores {
		dir := st.dir
		if *reproducible {
			dir = relativeToRoot(root, dir)
		}
		listed = append(listed, notesStoreOutput{Scope: string(st.scope), Path: dir})
		ns, is, err := store.Inspect(st.dir)
		if err != nil {
			return err
		}
		for _, n := range ns {
			found = append(found, scopedNote{
				Note:     n,
				Scope:    string(st.scope),
				Path:     notePath(root, st, n),
				Modified: noteModified(n, *reproducible),
			})
		}
		if *reproducible {
			// An issue names the file it is about by absolute path, which identifies the
			// machine that ran this as much as the note. Relative to the workspace it
			// still points at the same file for whoever reads the export.
			for i := range is {
				is[i].Path = relativeToRoot(root, is[i].Path)
			}
		}
		issues = append(issues, is...)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		if err := emitFormatted(opts, notesListOutput{Stores: listed, Notes: found, Issues: issues}); err != nil {
			return err
		}
		return notesIssuesError(issues)
	}
	if len(found) == 0 {
		fmt.Println("No notes yet. Write the first one with `magus notes edit <name>`.")
	} else {
		for _, n := range found {
			fmt.Printf("%-8s %s  %s  (%s)\n", n.Scope, n.Name, n.Title, notesAnchorSummary(n.Note))
		}
	}
	return printNotesIssues(issues)
}

func notesGet(root string, args []string) error {
	var getShared, getPrivate *bool
	pos, err := cmdParse("notes get", args, func(fs *flag.FlagSet) {
		getShared, getPrivate = notesScopeFlags(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus notes get <name> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Show one note: its anchors, its prose, and when it was last touched.")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("magus notes get: requires exactly one note name")
	}
	scope, err := notesScope(*getShared, *getPrivate)
	if err != nil {
		return err
	}
	stores, err := notesStores(root, scope)
	if err != nil {
		return err
	}
	n, st, err := findNote(stores, pos[0])
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitFormatted(opts, scopedNote{
			Note:     n,
			Scope:    string(st.scope),
			Path:     notePath(root, st, n),
			Modified: noteModified(n, false),
		})
	}
	printNote(n, st.scope)
	return nil
}

// notesEdit opens the note in the author's own editor, the way `git commit` does.
//
// This is the entire "no text editor" story. Notes are markdown files at real paths, so the
// tool that edits them is the one the author already uses - vim, VS Code, Obsidian, anything
// - and magus writes no editing surface of its own.
func notesEdit(ctx context.Context, root string, args []string) error {
	var editShared, editPrivate *bool
	var anchors stringList
	pos, err := cmdParse("notes edit", args, func(fs *flag.FlagSet) {
		editShared, editPrivate = notesScopeFlags(fs)
		fs.Var(&anchors, "anchor", "Anchor as kind:target (repeatable), e.g. --anchor symbol:'m pkg/Type#Method().' - required when writing a NEW note from stdin")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus notes edit <name> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Open one note in $VISUAL (else $EDITOR). A name that does not exist yet is")
			fmt.Fprintln(os.Stderr, "created from a scaffold, so the first save already has a valid shape.")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("magus notes edit: requires exactly one note name")
	}
	scope, err := notesScope(*editShared, *editPrivate)
	if err != nil {
		return err
	}
	stores, err := notesStores(root, scope)
	if err != nil {
		return err
	}
	// An existing note is edited where it already lives; a NEW one defaults to the first
	// declared store (shared when both exist), because team knowledge is the case worth
	// making easy and a private note is the deliberate exception.
	target := stores[0]
	// existing is the file the note was READ from, and using it is what makes "edited where
	// it already lives" true. store.Path answers a different question - where a note of this
	// name WOULD go - and for a note whose declared id no longer matches its filename the two
	// answers differ, so editing one opened a blank scaffold at the id's path and left the
	// real note untouched beside it.
	var existing string
	switch found, st, err := findNote(stores, pos[0]); {
	case err == nil:
		target, existing = st, found.Path
	case errors.Is(err, errAmbiguousNote):
		// Never guess which store was meant: the two mean different things to a reader,
		// and silently editing the shared one is the mistake this error exists to prevent.
		return err
	}
	dir := target.dir
	path := existing
	if path == "" {
		path, err = store.Path(dir, pos[0])
		if err != nil {
			return fmt.Errorf("magus notes edit: %w", err)
		}
	}
	// A pipe is the non-interactive way to author a note: `pg_dump ... | magus notes edit`
	// is the same act as opening the editor, just without a terminal to open one in. It is
	// still a PERSON writing - an agent reaching for this is denied by the command guard,
	// which is where the write boundary is enforced for the command surface (the path rule
	// only sees file writes, and a pipe is not one).
	if !stdinIsTerminal() {
		return notesWriteFromStdin(ctx, root, dir, target, pos[0], path, anchors)
	}
	editor := strings.TrimSpace(firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR")))
	if editor == "" {
		return fmt.Errorf("magus notes edit: neither $VISUAL nor $EDITOR is set; set one to your editor, pipe the body in, or open %s directly", path)
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
	cmd := exec.Command("sh", "-c", editor+" \"$1\"", "sh", path) //nolint:gosec // the editor is the user's own configured command, by design
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		// An editor that never started (a bad $EDITOR, a missing binary) would otherwise
		// leave the placeholder behind: listed by `notes ls`, assembled into the graph,
		// and swept up by the next `git add`. Only a scaffold this run created is removed,
		// and only while it is still untouched - an unreadable write-back leaves len 0, which
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
	fmt.Printf("Saved %s [%s] (%s).\n", path, target.scope, notesAnchorSummary(n))

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
		// The note IS saved - that write already succeeded and is not undone by this. What
		// failed is the re-attestation, and reporting it as success would leave the author
		// believing their note is fingerprinted against today's code when it is not. So it
		// is a real error, with the saved path named so nobody goes looking for lost work.
		return fmt.Errorf("magus notes edit: saved %s, but its anchors could not be fingerprinted because the knowledge graph would not load; run `magus notes edit %s` again once it does: %w", path, pos[0], err)
	}
	changed, err := store.RecordDigests(ctx, dir, pos[0], notesRevision(ctx, root), res.ForScope(string(target.scope)))
	if err != nil {
		return fmt.Errorf("magus notes edit %q: recording anchors: %w", pos[0], err)
	}
	if changed != 0 {
		fmt.Printf("Recorded the anchored code's current fingerprint for %d anchor%s: this note now reads as reviewed against the code as it is today.\n",
			changed, pluralSuffix(changed, "", "s"))
	}
	return nil
}

func notesVerify(ctx context.Context, root string, args []string) error {
	var vShared, vPrivate *bool
	_, err := cmdParse("notes verify", args, func(fs *flag.FlagSet) {
		vShared, vPrivate = notesScopeFlags(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus notes verify [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Check every note for malformed frontmatter, an invalid shape, and anchors that")
			fmt.Fprintln(os.Stderr, "no longer resolve. Errors exit non-zero.")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	scope, err := notesScope(*vShared, *vPrivate)
	if err != nil {
		return err
	}
	stores, err := notesStores(root, scope)
	if err != nil {
		return err
	}
	var report store.Verification
	for _, st := range stores {
		r, err := store.Verify(st.dir)
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
		anchorIssues, err := store.ResolveAnchors(ctx, st.dir, res.ForScope(string(st.scope)))
		if err != nil {
			return err
		}
		report.Issues = append(report.Issues, anchorIssues...)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		if err := emitFormatted(opts, report); err != nil {
			return err
		}
		return notesIssuesError(report.Issues)
	}
	if len(report.Issues) == 0 {
		fmt.Printf("[pass] notes: %d verified\n", report.Notes)
		return nil
	}
	return printNotesIssues(report.Issues)
}

// notesRevision is the commit the re-attestation is recorded AT, so a later drift report can
// say what to diff rather than only that something changed.
//
// It reports HEAD even when the tree is dirty, deliberately. A note is normally written
// alongside the very work it describes, so demanding a clean tree would leave the provenance
// empty in the common case - and HEAD-at-review is still the right base: it is the parent of
// the commit that will carry both the note and the change, so diffing from it shows the
// author exactly the work they were looking at. Empty when there is no resolvable VCS, which
// omits the provenance rather than blocking the write.
func notesRevision(ctx context.Context, root string) string {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return ""
	}
	meta, err := res.VCS.Metadata(ctx, root)
	if err != nil {
		return ""
	}
	return magus.ShortRevision(meta.ID)
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
func notesWriteFromStdin(ctx context.Context, root, dir string, target notesStore, name, path string, anchors []string) error {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("magus notes edit: reading stdin: %w", err)
	}
	if strings.TrimSpace(string(body)) == "" {
		return errors.New("magus notes edit: stdin was empty; a note with no prose is not a note")
	}

	n, err := store.Get(dir, name)
	switch {
	case err == nil:
		// An existing note keeps what it is ABOUT and has only its prose replaced, so
		// re-piping is a rewrite rather than a reshaping. Appending --anchor here would
		// accumulate duplicates on every run, and each duplicate re-reports every dangling
		// or drifted finding for the rest of the note's life.
		if len(anchors) != 0 {
			return fmt.Errorf("magus notes edit: %q already exists, so --anchor is refused; its anchors are what the note is about, and piping new prose does not change that. Edit the note to change them", name)
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
		return fmt.Errorf("magus notes edit: %q exists but could not be read; repair it before overwriting: %w", name, err)
	}
	if len(n.Anchors) == 0 {
		return errors.New("magus notes edit: a new note piped from stdin needs at least one --anchor (kind:target); an unanchored note is a diary entry nobody will find again")
	}
	n.Body = string(body)
	if err := store.Save(dir, n); err != nil {
		return fmt.Errorf("magus notes edit: %w", err)
	}
	fmt.Printf("Wrote %s [%s] (%s).\n", path, target.scope, notesAnchorSummary(n))

	res, err := notesResolver(ctx, root)
	if err != nil {
		return fmt.Errorf("magus notes edit: wrote %s, but its anchors could not be fingerprinted because the knowledge graph would not load: %w", path, err)
	}
	changed, err := store.RecordDigests(ctx, dir, name, notesRevision(ctx, root), res.ForScope(string(target.scope)))
	if err != nil {
		return fmt.Errorf("magus notes edit: wrote %s, but recording its anchors failed: %w", path, err)
	}
	if changed != 0 {
		fmt.Printf("Recorded the anchored code's current fingerprint for %d anchor%s.\n", changed, pluralSuffix(changed, "", "s"))
	}
	return nil
}

// firstNonEmpty returns a when it holds anything but whitespace, else b.
func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func notesAnchorSummary(n store.Note) string {
	parts := make([]string, 0, len(n.Anchors))
	for _, a := range n.Anchors {
		parts = append(parts, string(a.Kind)+":"+a.Target)
	}
	return strings.Join(parts, ", ")
}

func printNote(n store.Note, scope store.Scope) {
	fmt.Printf("%s  [%s]\n%s\n", n.Name, scope, n.Title)
	if len(n.Tags) != 0 {
		fmt.Printf("tags: %s\n", strings.Join(n.Tags, ", "))
	}
	fmt.Println("anchors:")
	for _, a := range n.Anchors {
		line := fmt.Sprintf("  %s: %s", a.Kind, a.Target)
		if a.Commit != "" {
			line += fmt.Sprintf("  (reviewed at %s)", a.Commit)
		}
		fmt.Println(line)
	}
	if n.Body != "" {
		fmt.Printf("\n%s\n", n.Body)
	}
	fmt.Printf("\nmodified: %s\n", n.Modified.UTC().Format(time.RFC3339))
}

func printNotesIssues(issues []store.Issue) error {
	for _, issue := range issues {
		glyph := "[warn]"
		if issue.Severity == "error" {
			glyph = "[fail]"
		}
		fmt.Printf("%s %s: %s\n  %s\n", glyph, issue.Path, issue.Message, issue.Hint)
	}
	return notesIssuesError(issues)
}

func notesIssuesError(issues []store.Issue) error {
	var failures int
	for _, issue := range issues {
		if issue.Severity == "error" {
			failures++
		}
	}
	if failures != 0 {
		return fmt.Errorf("magus notes verify: %d invalid note%s", failures, pluralSuffix(failures, "", "s"))
	}
	return nil
}
