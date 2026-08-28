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
	"path/filepath"
	"sort"
	"strings"
	"time"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/interp/bindings"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/memory"
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
	case "capture":
		return notesCapture(ctx, root, args[1:])
	case "promote":
		return notesPromote(ctx, root, args[1:])
	case "put":
		// Named rather than left to the generic unknown-subcommand error, because the
		// reason it is absent is the whole point of the store and is worth stating at the
		// moment someone reaches for it.
		return usagef("magus notes: there is no `put`; a note is written by a person, not a program (run `magus notes edit <name>`)")
	default:
		return usagef("magus notes: unknown subcommand %q (want ls, get, edit, verify, capture, or promote)", args[0])
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
	fmt.Fprintln(os.Stderr, "  capture  keep the review conversation, yours and the host's, as a note")
	fmt.Fprintln(os.Stderr, "  promote  edit an agent-drafted memory record into a note of your own")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Set knowledge.notes.shared (in the repo, your team gets it) or knowledge.notes.private")
	fmt.Fprintln(os.Stderr, "(anywhere, this machine only) in magus.yaml to enable a store. There is no `put`:")
	fmt.Fprintln(os.Stderr, "notes are written by people, in an editor, and committed under their own name.")
	fmt.Fprintln(os.Stderr, "`capture` is not an exception to that - it takes a conversation that already")
	fmt.Fprintln(os.Stderr, "happened and quotes it, and the note it writes says so in its own frontmatter.")
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
		fs.Bool("private", false, "Only your own notes (superseded: `magus memory` plus `notes promote` is the drafting tier now)")
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
		// Never strict: `ls` is a listing, and a listing that exits non-zero because the
		// store has a repair pending is one nothing can pipe. The gate lives in `verify`.
		return notesIssuesError(issues, false)
	}
	if len(found) == 0 {
		fmt.Println("No notes yet. Write the first one with `magus notes edit <name>`.")
	} else {
		for _, n := range found {
			fmt.Printf("%-8s %s  %s  (%s)\n", n.Scope, n.Name, n.Title, notesAnchorSummary(n.Note))
		}
	}
	return printNotesIssues(issues, false)
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
	var strict bool
	_, err := cmdParse("notes verify", args, func(fs *flag.FlagSet) {
		vShared, vPrivate = notesScopeFlags(fs)
		// Dangling only, and never drift. A dangling anchor is unambiguous, always fixable,
		// and its cause is in the diff that broke it - the properties a gate needs. Drift is
		// a judgment call whose base rate is low, and gating on judgment calls is how a
		// check earns a permanent `|| true`.
		fs.BoolVar(&strict, "strict", false,
			"Exit non-zero on a dangling anchor as well as an invalid note (for CI). Drift never fails.")
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
		anchorIssues, err := store.AnchorIssues(ctx, st.dir, res.ForScope(string(st.scope)))
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
		return notesIssuesError(report.Issues, strict)
	}
	if len(report.Issues) == 0 {
		fmt.Printf("[pass] notes: %d verified\n", report.Notes)
		return nil
	}
	if err := printNotesIssues(report.Issues, strict); err != nil {
		return err
	}
	return nil
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

func printNotesIssues(issues []store.Issue, strict bool) error {
	for _, issue := range issues {
		glyph := "[warn]"
		if issue.Severity == "error" || (strict && issue.Code == store.CodeDanglingAnchor) {
			glyph = "[fail]"
		}
		fmt.Printf("%s %s: %s\n  %s\n", glyph, issue.Path, issue.Message, issue.Hint)
	}
	return notesIssuesError(issues, strict)
}

// notesIssuesError decides the exit status. An invalid note always fails: the store could not
// be READ as declared, so anything acting on the result is acting on a partial store.
//
// Under --strict a dangling anchor fails too, and nothing else ever does. The line is drawn
// there because of what each finding costs to act on: a dangling anchor names something that
// was renamed or deleted in a diff someone is already looking at, and the fix is bounded. A
// drift finding asks a person to re-read prose against code and decide, and a gate that
// blocks a merge pending a judgment call is one people route around permanently. The only
// actively-maintained tool in this space is a CI check that fails a pull request when covered
// files move - the gate is the mechanism that works, and keeping it narrow is what keeps it.
func notesIssuesError(issues []store.Issue, strict bool) error {
	var failures, dangling int
	for _, issue := range issues {
		if issue.Severity == "error" {
			failures++
		}
		if strict && issue.Code == store.CodeDanglingAnchor {
			dangling++
		}
	}
	if failures == 0 && dangling != 0 {
		return fmt.Errorf("magus notes verify: %d dangling anchor%s; re-anchor the note or remove the anchor",
			dangling, pluralSuffix(dangling, "", "s"))
	}
	if failures != 0 {
		return fmt.Errorf("magus notes verify: %d invalid note%s", failures, pluralSuffix(failures, "", "s"))
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
// them into one readable record that outlives the review. A running daemon holds MORE than the
// store does - an agent's remarks, and remarks already published - and capture says so when it
// had to read the store instead, because a transcript quietly missing half a conversation is
// worse than no transcript.

// notesCapture writes the current review thread into a store as one note.
func notesCapture(ctx context.Context, root string, args []string) error {
	var title, name string
	var tags tagList
	var shared, private *bool
	pos, err := cmdParse("notes capture", args, func(fs *flag.FlagSet) {
		fs.StringVar(&title, "title", "", "Title for the note (defaults to naming the reviewed base)")
		fs.StringVar(&name, "name", "", "Note name (defaults to review-<patch digest>)")
		fs.Var(&tags, "tag", "Tag to set on the note; repeatable")
		shared, private = notesScopeFlags(fs)
	})
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("magus notes capture: unexpected argument %q; the thread to capture is the review under way", pos[0])
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("magus notes capture: %w", err)
	}
	sess, fromDaemon := captureSession(ctx, m)
	// The colleagues' half. Read separately and never required: a review with no forge behind
	// it is the ordinary case, and the local conversation is worth keeping on its own.
	threads, partial := reviewThreads(ctx, m)
	if len(sess.Comments) == 0 && len(threads) == 0 {
		return errors.New("magus notes capture: this review has no comments yet, and a transcript of an empty conversation is not worth a note")
	}

	// Private by default, and the asymmetry is deliberate rather than timid. A capture is
	// quoted material whose value is to the person who was in the room; committing one puts
	// somebody else's half-finished sentence in front of review under your name. Ask for
	// --shared when the team should have it.
	scope, err := notesScope(*shared, *private)
	if err != nil {
		return err
	}
	defaulted := scope == ""
	if defaulted {
		scope = knowledge.ScopePrivate
	}
	stores, err := notesStores(root, scope)
	if err != nil {
		// A workspace that declares only a shared store is the common one, so the default
		// landing on a store it has not got must teach both ways out rather than only the
		// one notesStores knows about. It does NOT quietly fall back to shared: that would
		// turn a safety default into a surprise commit of somebody's half-finished sentence.
		if defaulted {
			return fmt.Errorf("%w\n  --shared            put this transcript in the repository, where review sees it", err)
		}
		return err
	}
	target := stores[0]

	capture := captureFromSession(sess, threads, title, tags)
	noteName := name
	if noteName == "" {
		noteName = captureName(sess)
	}
	n, err := capture.Note(noteName)
	if err != nil {
		return fmt.Errorf("magus notes capture: %w", err)
	}
	if _, err := store.Get(target.dir, noteName); err == nil {
		return fmt.Errorf("magus notes capture: %q already exists in the %s store; pass --name to keep both, because overwriting it would destroy a transcript nothing can recreate", noteName, target.scope)
	}
	if err := store.Save(target.dir, n); err != nil {
		return fmt.Errorf("magus notes capture: %w", err)
	}
	// Read back rather than reporting the value just written. notePath needs Note.Path, which
	// is observed on read and empty on a note that was built - printing the built one names
	// the store directory instead of the file. The round trip also proves the transcript
	// parses as a note, which is worth one stat for something nothing can recreate.
	saved, err := store.Get(target.dir, noteName)
	if err != nil {
		return fmt.Errorf("magus notes capture: wrote %q, but it does not read back as a note: %w", noteName, err)
	}
	// Every remark, both halves. Counting only the local ones would understate a transcript
	// whose most useful line came from somebody else.
	said := len(sess.Comments) + len(threads)
	fmt.Printf("Captured %d comment%s into %s [%s] (%s).\n",
		said, pluralSuffix(said, "", "s"),
		notePath(root, target, saved), target.scope, notesAnchorSummary(saved))
	// Said out loud, because a transcript is exactly the artifact nobody re-checks. A capture
	// that quietly omitted part of the review would be discovered, if ever, by the person who
	// went looking for what a colleague said and concluded they had said nothing.
	if partial != "" {
		fmt.Printf("Part of the review could not be read, so this transcript is incomplete: %s\n", partial)
	}
	// Named for the same reason. The store keeps your unsent remarks and nothing else, so a
	// capture taken without a daemon is missing anything an agent said and anything already
	// published to the review - and the reader has to be told which record they are holding.
	if !fromDaemon {
		fmt.Println("Read from the local review store, so this holds your unsent remarks. A running daemon would also carry an agent's remarks and any already published.")
	}

	// Fingerprint the anchored files, exactly as a written note is fingerprinted on save. It
	// matters MORE here: a transcript is about code as it stood during one review, so the
	// question a reader will have later is whether it has moved since - and only a recorded
	// digest lets `notes verify` answer it. Without this the capture reports "unverified"
	// forever, which is the one verdict that means nothing was ever checked.
	res, err := notesResolver(ctx, root)
	if err != nil {
		return fmt.Errorf("magus notes capture: captured the thread, but its anchors could not be fingerprinted because the knowledge graph would not load: %w", err)
	}
	changed, err := store.RecordDigests(ctx, target.dir, noteName, notesRevision(ctx, root), res.ForScope(string(target.scope)))
	if err != nil {
		return fmt.Errorf("magus notes capture: captured the thread, but recording its anchors failed: %w", err)
	}
	if changed != 0 {
		fmt.Printf("Recorded the reviewed code's current fingerprint for %d anchor%s.\n", changed, pluralSuffix(changed, "", "s"))
	}
	return nil
}

// captureFromSession maps a review session onto the store's source-agnostic capture shape.
// The mapping lives here rather than in internal/notes so the store never has to know what a
// diff is - and so a second source can be added without it learning.
//
// threads are the remarks already on the host's review, and they are captured ALONGSIDE the
// session's own. A transcript holding only your half of a conversation is not a transcript of
// the conversation: the question a reader has months later is what was decided, and the answer
// is nearly always in what somebody else said back.
func captureFromSession(sess *types.DiffSession, threads []types.ReviewThread, title string, tags []string) store.Capture {
	// Grouped by file, because that is how a reviewer looks for a thread later ("what did we
	// say about key.go"). Comment order within a file is left alone: it is the order the
	// conversation happened in, and sorting it would break the replies.
	byPath := map[string][]store.CaptureEntry{}
	paths := []string{}
	add := func(path string, e store.CaptureEntry) {
		if _, seen := byPath[path]; !seen {
			paths = append(paths, path)
		}
		byPath[path] = append(byPath[path], e)
	}
	// The host's threads first, per file. What a colleague said usually came before the
	// remark it provoked, and a transcript that opened with the reply reads backwards.
	for _, t := range threads {
		add(t.Path, store.CaptureEntry{
			Subject: t.Path,
			Locator: store.LineLocator(t.Line),
			// The author the host reported, verbatim. Nothing here verifies it, and nothing
			// should: a capture records who a conversation says spoke, and a store that
			// second-guessed that would be making the authorship claim it exists to avoid.
			Author: t.Author,
			Body:   t.Body,
		})
	}
	for _, c := range sess.Comments {
		add(c.Path, store.CaptureEntry{
			Subject:  c.Path,
			Locator:  store.HunkLocator(c.Hunk),
			Author:   commentAuthor(c),
			Body:     c.Body,
			Resolved: c.Resolved,
		})
	}
	sort.Strings(paths)

	entries := []store.CaptureEntry{}
	for _, p := range paths {
		entries = append(entries, byPath[p]...)
	}
	return store.Capture{
		Title: firstNonEmpty(title, "Review thread on "+firstNonEmpty(sess.Base, "the working tree")),
		Tags:  tags,
		Source: store.Source{
			Kind:     store.SourceReviewThread,
			Ref:      sess.ID,
			AsOf:     sess.AsOf,
			Captured: time.Now(),
		},
		Entries: entries,
	}
}

// commentAuthor renders who said something. An agent is named as one: a transcript that let a
// tool's output read as a colleague's opinion would be the capture telling the reader the one
// thing it must not.
func commentAuthor(c types.DiffComment) string {
	if c.Author != types.DiffAuthorAgent {
		// "reviewer", not the "human" the enum spells, and not a name. The session records
		// that a person wrote this and never which person, so a name would be invented; and
		// "You" would be a lie to everyone except the one reader who captured it, which is
		// exactly the wrong reader to optimize a committed note for.
		return "reviewer"
	}
	if c.AgentName == "" {
		return "agent"
	}
	return c.AgentName + " (agent)"
}

// captureName derives a note name from the patch the thread was written against, so two
// captures from different reviews cannot collide and a repeat of the SAME review is caught by
// the exists check rather than silently overwriting.
func captureName(sess *types.DiffSession) string {
	digest := sess.AsOf
	if len(digest) > 12 {
		digest = digest[:12]
	}
	if digest == "" {
		return "review-thread"
	}
	return "review-" + digest
}

// captureSession is the local half of the conversation to transcribe, and whether it came from
// a running daemon.
//
// The daemon is preferred because it holds strictly more: an agent's remarks, and remarks
// already published to the review, neither of which the store keeps. But the store keeps the
// unsent human ones, which is what a person writing alone in `magus diff` produces, so the
// absence of a daemon is a smaller transcript rather than no transcript. The caller says which
// one it got - see notesCapture's output.
func captureSession(ctx context.Context, m *magus.Magus) (*types.DiffSession, bool) {
	if sess := daemonDiffSession(ctx); sess != nil {
		return sess, true
	}
	patch, _ := m.WorkingDiff(ctx, nil)
	return storedDiffSession(m.CacheDir(), patch), false
}

// storedDiffSession rebuilds what a capture needs from the files the store persists.
//
// AsOf is the patch digest attachDiffSession would have stamped with no daemon in the picture,
// so the note this capture names is the one either path would have named for this tree. No
// patch leaves it EMPTY rather than digesting the empty string, which would name every such
// capture the same note and make the second one collide with the first.
func storedDiffSession(cacheDir, patch string) *types.DiffSession {
	sess := &types.DiffSession{Comments: changeset.NewStore(cacheDir).LoadDrafts()}
	if patch != "" {
		sess.AsOf = changeset.PatchDigest(patch)
	}
	return sess
}

// daemonDiffSession reads the running daemon's review session, or nil when there is no daemon,
// no token, or nothing attached. Every failure is nil rather than an error: the caller has one
// message to print for all of them, and it is about the session rather than the transport.
//
// /api/v1/diff/session, NOT /api/v1/diff. The latter is the annotated changeset - it loads the
// symbol shards and walks a reverse closure, and it wants the paths under review. This one
// hands back the attached session as it stands, which is all a transcript needs, and answers
// 409 when nothing is attached.
func daemonDiffSession(ctx context.Context) *types.DiffSession {
	token, err := auth.Load()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, diffBridgeAttach)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+mcpAddrString()+"/api/v1/diff/session", nil)
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
	return &sess
}

// reviewThreads reads what colleagues said on the review this branch has open, and the reason
// the read was incomplete when there is one.
//
// The daemon answers when one is running, because its session also knows which threads the
// reader has already had on screen. Without one the forge is asked directly: a colleague's
// remark is a fact about the review, not about whether a background process happens to be up,
// and the same patch on the same branch must not show a different conversation either way.
func reviewThreads(ctx context.Context, m *magus.Magus) ([]types.ReviewThread, string) {
	if threads, reason, served := daemonReviewThreads(ctx); served {
		return threads, reason
	}
	return localReviewThreads(ctx, m.ReviewOrigin(ctx), m.CacheDir())
}

// daemonReviewThreads reads the threads from a running daemon. served says whether the daemon
// answered at all, which is what separates "no daemon, ask the forge yourself" from "the daemon
// looked and there is no review open".
//
// The reason is separate from the emptiness, and only non-empty when magus READ the review and
// could not understand part of it. That is the one case a caller must not pass over quietly: a
// transcript silently missing a colleague's remark is worse than no transcript.
func daemonReviewThreads(ctx context.Context) (threads []types.ReviewThread, reason string, served bool) {
	token, err := auth.Load()
	if err != nil {
		return nil, "", false
	}
	ctx, cancel := context.WithTimeout(ctx, diffBridgeAttach)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+mcpAddrString()+"/api/v1/diff/review", nil)
	if err != nil {
		return nil, "", false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", false
	}
	var body struct {
		ID      string               `json:"id"`
		Threads []types.ReviewThread `json:"threads"`
		Reason  string               `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, "", false
	}
	if body.ID == "" {
		// No review open. The reason names which ordinary situation that is, and none of them
		// is worth a line during a capture - there is simply no second half.
		return nil, "", true
	}
	return body.Threads, body.Reason, true
}

// localReviewThreads asks the forge itself, the way the check-review job and the daemon's own
// review handler do. Placement is left to the caller: the daemon resolves threads against the
// working tree, and a caller showing some other patch has to place them against that one.
//
// The origin and the cache dir are passed rather than a workspace, so a test can answer them
// without a repository - the narrowing the daemon's own review source uses.
func localReviewThreads(ctx context.Context, from types.ReviewOrigin, cacheDir string) ([]types.ReviewThread, string) {
	at := bindings.FindReview(ctx, from.Branch, from.Remote)
	if !at.Open() {
		return nil, ""
	}
	threads, err := bindings.ReviewThreads(ctx, at)
	// The watermark is PERSISTED, so the new-thread mark survives without the daemon that
	// normally applies it. Reading it here never moves it, for the reason the handler gives.
	watermark := types.DiffSession{SeenThreads: changeset.NewStore(cacheDir).LoadSeenThreads()}
	fresh := make(map[string]struct{})
	for _, id := range watermark.UnseenThreads(threads) {
		fresh[id] = struct{}{}
	}
	for i := range threads {
		if _, ok := fresh[threads[i].ID]; ok {
			threads[i].New = true
		}
	}
	if err != nil {
		// The threads that DID decode still travel, and the reason rides beside them - the
		// handler's posture, for the handler's reason.
		return threads, err.Error()
	}
	return threads, ""
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

// --- promote -----------------------------------------------------------------------------
//
// Promotion is the second write path, and it is the one that answers why this store is empty.
//
// A note costs a person a deliberate act of composition, and the evidence is that they do not
// pay it: across 921 GitHub projects carrying architecture decision records, about half of
// those that adopt the shape at all end up with between one and five entries. This repository
// has one. The same knowledge - what bit us, what was rejected, what is invisible in the
// resulting code - accumulated by the hundreds of files in an agent-written store instead.
// That is not a demand failure; it is capture cost landing on the party that collects none of
// the benefit, which Grudin published in 1996 and nothing since has repealed.
//
// So the expensive step moves. An agent drafts into memory, which is cheap and already
// happens; a person promotes, and the promotion is the signature. What makes it a signature
// rather than a rubber stamp is that it goes through $EDITOR and REFUSES an unmodified body:
// promoting without changing a word means nobody read it, and a store of unread claims signed
// by a person is worse than an empty one.
//
// Anchors come from the record's own node refs rather than being asked for again. A memory
// record already points into the graph, and making the promoter restate that is the same
// friction this path exists to remove.

// notesPromote turns an agent-drafted memory record into a human-authored note.
func notesPromote(ctx context.Context, root string, args []string) error {
	var name string
	pos, err := cmdParse("notes promote", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", "Note name (defaults to the record's name)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus notes promote <record> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Open an agent-drafted memory record in $VISUAL (else $EDITOR) and write it to the")
			fmt.Fprintln(os.Stderr, "shared notes store under your own name. Anchors come from the record's node refs.")
			fmt.Fprintln(os.Stderr, "A body you did not change is refused: promotion is an act of reading.")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("magus notes promote: requires exactly one record name")
	}
	rec, err := memory.Get(root, pos[0])
	if err != nil {
		return fmt.Errorf("magus notes promote: %w", err)
	}
	anchors, err := anchorsFromRefs(rec)
	if err != nil {
		return err
	}
	// Shared, always. A promotion whose destination was machine-local would be a signature
	// nobody can read; the point is that git records who vouched for this.
	stores, err := notesStores(root, knowledge.ScopeShared)
	if err != nil {
		return err
	}
	target := stores[0]

	noteName := firstNonEmpty(name, rec.Name)
	if _, gerr := store.Get(target.dir, noteName); gerr == nil {
		return fmt.Errorf("magus notes promote: note %q already exists; pass --name to promote alongside it", noteName)
	}
	draft := store.Note{
		Name:    noteName,
		Title:   firstNonEmpty(rec.Status, strings.ReplaceAll(noteName, "-", " ")),
		Anchors: anchors,
		Body:    promoteBody(rec),
	}
	if err := store.Save(target.dir, draft); err != nil {
		return fmt.Errorf("magus notes promote: %w", err)
	}
	path, err := store.Path(target.dir, noteName)
	if err != nil {
		return fmt.Errorf("magus notes promote: %w", err)
	}
	before, _ := os.ReadFile(path)

	if err := openInEditor(path); err != nil {
		// The draft exists only because this command wrote it a moment ago, so an editor
		// that never started leaves nothing behind rather than a half-promoted note.
		_ = os.Remove(path)
		return fmt.Errorf("magus notes promote: %w", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("magus notes promote: reading back %s: %w", path, err)
	}
	// The refusal, and the whole reason this is a promotion rather than a copy. An unmodified
	// body is an agent's prose with a person's name on the commit, which is exactly the
	// unattributable author this store exists to keep out.
	if bytes.Equal(before, after) {
		_ = os.Remove(path)
		return fmt.Errorf("magus notes promote: nothing was changed, so nothing was promoted.\n"+
			"  A promotion is you vouching for this prose, and an unedited draft is an agent's claim with your name on the commit.\n"+
			"  Read it against the code, fix what is wrong or unclear, and run this again. The record is still in memory as %q", rec.Name)
	}
	if _, err := store.Get(target.dir, noteName); err != nil {
		return fmt.Errorf("magus notes promote: %s no longer reads as a note after editing: %w", path, err)
	}
	fmt.Printf("Promoted %s to %s [shared]. Commit it to put your name on it.\n",
		rec.Name, relativeToRoot(root, path))

	res, err := notesResolver(ctx, root)
	if err != nil {
		return fmt.Errorf("magus notes promote: wrote %s, but its anchors could not be fingerprinted: %w", path, err)
	}
	changed, err := store.RecordDigests(ctx, target.dir, noteName, notesRevision(ctx, root), res.ForScope(string(target.scope)))
	if err != nil {
		return fmt.Errorf("magus notes promote: wrote %s, but recording its anchors failed: %w", path, err)
	}
	if changed != 0 {
		fmt.Printf("Recorded the anchored code's current fingerprint for %d anchor%s.\n", changed, pluralSuffix(changed, "", "s"))
	}
	return nil
}

// anchorsFromRefs derives a note's anchors from the record's node refs.
//
// Node refs only. A query, a command and an output ref are re-runnable strings rather than
// graph entities, so they carry into the body as evidence and cannot anchor anything. A
// record with none of them is refused rather than given an anchor of convenience: an anchor
// the note is not really about passes verify forever while pointing at the wrong thing.
func anchorsFromRefs(rec memory.Record) ([]store.Anchor, error) {
	var anchors []store.Anchor
	for _, ref := range rec.Refs {
		if ref.Kind != memory.RefKindNode {
			continue
		}
		a, err := store.ParseAnchor(ref.Target)
		if err != nil {
			continue // a node id whose prefix is not an anchor kind (a package, say)
		}
		anchors = append(anchors, a)
	}
	if len(anchors) == 0 {
		return nil, fmt.Errorf("magus notes promote: %q has no node ref naming a symbol, file, project or target, so the note would be unanchored and nobody would find it again.\n"+
			"  Add one with `magus memory put %s --ref node:<id>`, or write the note directly with `magus notes edit`", rec.Name, rec.Name)
	}
	return anchors, nil
}

// promoteBody seeds the editor with the record's prose and the evidence behind it, so the
// promoter edits a draft rather than facing a blank file.
//
// The re-runnable refs are listed because they are what makes the claim checkable, and
// checking it is the act being asked for. They stay prose: a note's anchors are its
// structure, and copying a query into frontmatter would create a second thing to keep in step.
func promoteBody(rec memory.Record) string {
	var b strings.Builder
	if body := strings.TrimSpace(rec.Body); body != "" {
		b.WriteString(body + "\n")
	} else {
		b.WriteString("(This record carried no prose. Say what is true, and why it is worth keeping.)\n")
	}
	var evidence []string
	for _, ref := range rec.Refs {
		if ref.Kind == memory.RefKindNode {
			continue // already an anchor
		}
		evidence = append(evidence, string(ref.Kind)+": "+ref.Target)
	}
	if len(evidence) > 0 {
		b.WriteString("\nEvidence recorded with the draft:\n")
		for _, e := range evidence {
			b.WriteString("  " + e + "\n")
		}
	}
	b.WriteString("\nDrafted by an agent as memory record " + rec.Name +
		". Delete this line once the prose above is yours.\n")
	return b.String()
}

// openInEditor runs the reader's own $VISUAL/$EDITOR against path and waits.
func openInEditor(path string) error {
	editor := strings.TrimSpace(firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR")))
	if editor == "" {
		return fmt.Errorf("neither $VISUAL nor $EDITOR is set; set one, or open %s directly", path)
	}
	cmd := exec.Command("sh", "-c", editor+" \"$1\"", "sh", path) //nolint:gosec // the editor is the user's own configured command, by design
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", editor, err)
	}
	return nil
}
