// Package notes is the workspace's human-authored knowledge store: discrete entries
// (one markdown file per note, YAML frontmatter carrying the structured fields) that
// attach to graph entities without being derived from any of them.
//
// It is the counterpart to internal/memory, and the two differ on every axis that
// matters:
//
//   - WHO WRITES. A note is written by a person in their editor. Nothing here is an
//     agent-facing write path, and there is deliberately no Put: `magus notes edit`
//     opens $EDITOR and gets out of the way.
//   - WHERE IT LIVES. In the CHECKOUT, at a path the workspace declares - not in XDG
//     state like memory, whose package doc explains that "a developer's working memory
//     does not belong in a shared checkout". A note inverts exactly that clause: a
//     team's shared understanding does belong there. Being in the checkout is also what
//     buys per-author attribution for free, since the @vcs shard already mints author
//     nodes for files it can see, and nothing outside the checkout can ever have that.
//   - WHAT IT MAY SAY. memory REQUIRES a ref, because an agent-written claim has to be
//     anchored to something checkable. A note is the class that cannot be checked that
//     way: its only provenance is a person. So anchors are required for FINDABILITY,
//     but prose is the payload rather than a caption.
//
// Everything in the graph other than a note is DERIVED from workspace content - docs
// from markdown, rationale from comments, symbols from an index, authors from git.
// Delete the graph and rebuild and you get all of it back. A note is the one node class
// that is injected rather than extracted, and no rebuild recovers it.
package notes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"gopkg.in/yaml.v3"
)

// ErrDisabled reports that this workspace declares no notes path, which is the default
// and is not a failure. Every entry point returns it rather than inventing a location:
// the guard rule in particular must fire only on a DECLARED path, because a deny fired
// on a guessed one blocks real work.
var ErrDisabled = errors.New("notes: this workspace declares no notes path (set knowledge.notes.shared or knowledge.notes.private in magus.yaml)")

// AnchorKind is the closed set of things a note may attach to.
//
// Deliberately absent: any kind carrying a POSITION. A node ID is checkable, so its
// breakage is reportable; a line number is not, so its breakage is invisible - it
// changes on the next edit above it with nothing to detect. That single distinction is
// what separates this from every line-anchored review comment, code tour, and web
// annotation, all of which rot and then paper over it with fuzzy re-matching.
type AnchorKind string

const (
	AnchorSymbol  AnchorKind = "symbol"
	AnchorFile    AnchorKind = "file"
	AnchorProject AnchorKind = "project"
	AnchorTarget  AnchorKind = "target"
	AnchorNote    AnchorKind = "note"
)

// Anchor is one typed attachment from a note to a graph entity.
type Anchor struct {
	Kind   AnchorKind `json:"kind" yaml:"kind"`
	Target string     `json:"target" yaml:"target"`
	// Digest fingerprints the anchored content as of the last review, so a CHANGE is
	// detectable and not merely a deletion. Existence checks catch a rename or a delete;
	// this catches the far more common case where the code still exists and quietly
	// stopped meaning what the note says. Empty when the kind has no content to hash,
	// and empty until the note has been verified once.
	Digest string `json:"digest,omitempty" yaml:"digest,omitempty"`
	// Commit is the revision this anchor was last reviewed against. PROVENANCE ONLY -
	// resolution always runs against the working tree. A pinned revision never breaks,
	// which is precisely why it must never be the anchor: it would go on pointing at
	// correct-looking frozen content long after the thing it described was deleted.
	Commit string `json:"commit,omitempty" yaml:"commit,omitempty"`
}

// Note is one human-authored entry. Name is the identity and the on-disk basename.
//
// The frontmatter carries only what cannot be derived. There is no Author field and no
// Trust field: both would be self-attested and therefore forgeable by whatever wrote the
// file, when authorship already comes from git via the @vcs shard and trust is structural
// (it follows from which store an entry is in, not from a value the writer chose).
//
// There are no created/updated fields either, and that is the same argument. These files
// are edited by hand, so a stored timestamp is wrong the first time someone saves without
// going through magus - a self-maintained field that rots is exactly the failure this
// whole store is built to avoid. Modified is derived from the file and never serialized;
// git carries the real history.
type Note struct {
	// Name is the note's identity in memory: its ID when one is declared, otherwise its
	// path within the store. Never serialized under this key - see ID.
	Name string `json:"name" yaml:"-"`
	// ID is an optional stable identity, and it is what makes a store survive being a
	// vault someone reorganizes.
	//
	// Without it a note is identified by its path, and Obsidian's rename - which helpfully
	// rewrites every [[wikilink]] and knows nothing about magus - silently changes the
	// note's graph ID and dangles every note-kind anchor pointing at it. That is the exact
	// failure this feature argues against everywhere else: an identity that encodes a
	// location. magus stamps one on every note it creates; a hand-written vault note gets
	// path identity until its author adds one.
	ID      string   `json:"id,omitempty" yaml:"id,omitempty"`
	Title   string   `json:"title" yaml:"title"`
	Tags    []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Anchors []Anchor `json:"anchors" yaml:"anchors"`
	Body    string   `json:"body,omitempty" yaml:"-"`
	// Modified is the file's modification time, filled in on read. It is observed, not
	// stored, so it cannot disagree with the file it describes.
	Modified time.Time `json:"modified" yaml:"-"`
}

// Severity separates the two things verify reports, and the split is load-bearing rather
// than cosmetic: an error means the store could not be READ as declared, so a caller acting
// on the result is acting on an incomplete store; a warning means a note was read fine and
// what it points AT has moved. Only the first should ever stop a caller.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// IssueCode names what verify found, so a caller branches on the code rather than matching
// the message - the messages are written for a person and change freely.
type IssueCode string

const (
	// CodeInvalidEntry: a file under the store declares a magus block that does not parse
	// or does not validate. The note is unreadable, not merely stale.
	CodeInvalidEntry IssueCode = "invalid-entry"
	// CodeStoreTruncated: the walk stopped at MaxNotes, so the report describes a prefix
	// of the store rather than the store.
	CodeStoreTruncated IssueCode = "store-truncated"
	// CodeMissingNote: a note anchors to another note that is not in the store.
	CodeMissingNote IssueCode = "missing-note"
	// CodeDanglingAnchor: an anchor names an entity the graph no longer has.
	CodeDanglingAnchor IssueCode = "dangling-anchor"
	// CodeDriftedAnchor: the anchored entity still exists, and its content changed since a
	// person last reviewed the note against it.
	CodeDriftedAnchor IssueCode = "drifted-anchor"
	// CodeUnverifiableAnchor: the anchored entity exists, and its fingerprint could not be
	// computed - so whether the note still holds is UNKNOWN rather than wrong.
	CodeUnverifiableAnchor IssueCode = "unverifiable-anchor"
)

// Issue is one problem found while scanning, in the same shape memory reports, so a
// frontend can render both stores' findings the same way.
type Issue struct {
	Severity Severity  `json:"severity" yaml:"severity"`
	Code     IssueCode `json:"code" yaml:"code"`
	Path     string    `json:"path,omitempty" yaml:"path,omitempty"`
	Note     string    `json:"note,omitempty" yaml:"note,omitempty"`
	Message  string    `json:"message" yaml:"message"`
	Hint     string    `json:"hint,omitempty" yaml:"hint,omitempty"`
}

// Verification is the summary `magus notes verify` reports.
type Verification struct {
	Notes  int     `json:"notes" yaml:"notes"`
	Issues []Issue `json:"issues" yaml:"issues"`
}

// nameRE is the note name shape: a kebab slug. It doubles as the on-disk basename, so it
// must be filesystem-safe - lowercase alphanumerics joined by single hyphens, no slashes
// or dots, which keeps a name from escaping the notes directory.
var nameRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// syncConflictRE matches the copies a file-sync tool leaves beside a note it could not
// merge. Each is a full duplicate, magus block included.
//
//	Obsidian Sync  Note.sync-conflict-20260813-090000-ABCDEFG.md
//	Syncthing      Note.sync-conflict-20260813-090000.md
//	Dropbox        Note (conflicted copy 2026-08-13).md
var syncConflictRE = regexp.MustCompile(`(?i)\.sync-conflict-|\(conflicted copy [^)]*\)|\(.*'s conflicted copy .*\)`)

func isSyncConflict(name string) bool { return syncConflictRE.MatchString(name) }

// obsidianTemplateDir returns the vault's configured template folder, or "" when the store
// is not a vault or configures none. Reading .obsidian/templates.json is what makes this
// exact: a hardcoded folder name would skip a directory someone uses for real notes, and
// miss a vault that named theirs something else.
func obsidianTemplateDir(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".obsidian", "templates.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Folder string `json:"folder"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil || strings.TrimSpace(cfg.Folder) == "" {
		return ""
	}
	return filepath.Join(dir, filepath.FromSlash(cfg.Folder))
}

// MaxNotes bounds one scan. A store may be a vault of any size, and a graph build must not
// become unbounded work because someone pointed magus at their whole writing life. The cap
// is REPORTED rather than silently applied, since quietly loading half a store is the kind
// of partial truth this whole feature exists to avoid.
const MaxNotes = 5000

// scipLocalPrefix marks a SCIP local symbol. Local symbols are index-scoped counters
// ("local 0", "local 1"), carry no name, and the SCIP spec forbids using them outside the
// Document that produced them. They are therefore structurally unanchorable: accepting
// one yields a note that dangles on the next index build for a reason no reader could
// diagnose from the note itself.
const scipLocalPrefix = "local "

// Scope names which of the two stores a path belongs to. The two differ in exactly one
// way that matters - whether the location can attribute a note to anyone - and every
// other difference below follows from it.
type Scope string

const (
	// ScopeShared is the team's store: inside the checkout, so a commit attributes each
	// note and review saw it.
	ScopeShared Scope = "shared"
	// ScopePrivate is the reader's own store: anywhere on disk, attributed to nobody.
	ScopePrivate Scope = "private"
)

// ConfigKey is the magus.yaml key that declares this scope's directory, so a diagnostic
// names the line the reader has to edit rather than the concept.
func (s Scope) ConfigKey() string { return "knowledge.notes." + string(s) }

// Dir resolves a scope's notes directory from its declared path, returning ErrDisabled
// when nothing is declared.
//
// One entry point rather than a resolver per scope, because every caller has the scope in
// hand and had to pair it with the matching function by eye - a pairing nothing checked,
// and getting it backwards would silently grant a shared store the private store's freedom
// to live outside the checkout.
//
// The path is DECLARED rather than discovered by convention for the same reason the
// declared-output guard rule is definitive while the filename heuristics are not: an
// advisory fired on a guess trains the reader to ignore it, and a deny fired on a guess
// blocks work that was never opted in.
func Dir(root string, scope Scope, declared string) (string, error) {
	switch scope {
	case ScopeShared:
		return sharedDir(root, declared)
	case ScopePrivate:
		return privateDir(root, declared)
	default:
		return "", fmt.Errorf("notes: unknown scope %q (want %q or %q)", scope, ScopeShared, ScopePrivate)
	}
}

// sharedDir resolves the TEAM's notes directory, which must stay inside the checkout.
//
// That is not an arbitrary restriction: outside it there is no commit to attribute a note
// to and no review to have seen it, so "shared" would be a claim the location cannot back.
// ScopePrivate is the scope for everything else.
func sharedDir(root, declared string) (string, error) {
	declared = strings.TrimSpace(declared)
	if declared == "" {
		return "", ErrDisabled
	}
	if filepath.IsAbs(declared) {
		return "", fmt.Errorf("notes: %s %q must be workspace-relative, not absolute", ScopeShared.ConfigKey(), declared)
	}
	clean := filepath.Clean(declared)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("notes: %s %q escapes the workspace; a note outside the checkout is not shared with anyone, so use %s for that", ScopeShared.ConfigKey(), declared, ScopePrivate.ConfigKey())
	}
	// The workspace root itself is not a notes store. Allowing it would make every
	// markdown file in the repo a note (so README.md reports as a malformed one) and,
	// worse, would make the agent guard treat every path under the root as protected -
	// one config line turning a targeted rule into a workspace-wide deny.
	if clean == "." {
		return "", fmt.Errorf("notes: %s %q is the workspace root; name a directory inside it", ScopeShared.ConfigKey(), declared)
	}
	dir := filepath.Join(root, clean)
	// Lexical checks alone are not containment: `docs/team/notes` can be a symlink out of
	// the tree, and then notes that nothing attributes land in a shard that IS exported to
	// the shared cache. denyNotesWrite already resolves symlinks before comparing; the
	// function that is supposed to be the containment authority must do at least as much.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			realRoot = root
		}
		if rel, err := filepath.Rel(realRoot, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("notes: %s %q resolves outside the workspace; use %s for a location that is not committed", ScopeShared.ConfigKey(), declared, ScopePrivate.ConfigKey())
		}
	}
	return dir, nil
}

// privateDir resolves the reader's own notes directory, which unlike the shared one may
// live anywhere on disk - that is the whole difference between the two stores.
//
// `~` is expanded because this is a hand-written path in a config file and a literal `~`
// directory is never what anyone meant. A relative path resolves against the workspace,
// which keeps `private: .notes-local` working for someone who wants a gitignored
// directory in the checkout rather than a vault outside it.
func privateDir(root, declared string) (string, error) {
	declared = strings.TrimSpace(declared)
	if declared == "" {
		return "", ErrDisabled
	}
	if declared == "~" || strings.HasPrefix(declared, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("notes: %s %q: %w", ScopePrivate.ConfigKey(), declared, err)
		}
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(declared, "~"), "/")), nil
	}
	if filepath.IsAbs(declared) {
		return filepath.Clean(declared), nil
	}
	return filepath.Join(root, filepath.Clean(declared)), nil
}

// Validate enforces the note schema on the way IN and on the way OUT, so no caller can
// hold a shape the model does not expect.
func Validate(n Note) error {
	if !nameRE.MatchString(n.Name) {
		return fmt.Errorf("notes: name %q must be a kebab slug (lowercase alphanumerics and hyphens)", n.Name)
	}
	return validateBody(n)
}

// validateStored is Validate minus the kebab rule, for a note magus is READING.
//
// The rule exists so a name magus chooses is filesystem-safe; a name derived from a path
// magus just walked is safe by construction, and holding a vault to a naming convention it
// never agreed to is how "your notes are wrong" gets printed 1,500 times. Traversal is
// still refused, because the name is joined back onto the store directory by Get.
func validateStored(n Note) error {
	if n.Name == "" || filepath.IsAbs(n.Name) {
		return fmt.Errorf("notes: invalid name %q", n.Name)
	}
	for _, seg := range strings.Split(n.Name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("notes: name %q must not contain a %q path segment", n.Name, seg)
		}
	}
	return validateBody(n)
}

// validateBody holds the rules that apply however the note arrived.
func validateBody(n Note) error {
	if strings.TrimSpace(n.Title) == "" {
		return errors.New("notes: a note needs a title")
	}
	if len(n.Anchors) == 0 {
		return errors.New("notes: a note needs at least one anchor; an unanchored note is a diary entry, not workspace knowledge that anyone will find again")
	}
	for _, a := range n.Anchors {
		switch a.Kind {
		case AnchorSymbol, AnchorFile, AnchorProject, AnchorTarget, AnchorNote:
		default:
			return fmt.Errorf("notes: anchor kind must be one of symbol, file, project, target, note (got %q)", a.Kind)
		}
		target := strings.TrimSpace(a.Target)
		if target == "" {
			return fmt.Errorf("notes: anchor of kind %q has an empty target", a.Kind)
		}
		if strings.ContainsAny(target, "\n\r") {
			return fmt.Errorf("notes: anchor target %q must be a single line", target)
		}
		if a.Kind == AnchorSymbol && strings.HasPrefix(target, scipLocalPrefix) {
			return fmt.Errorf("notes: anchor target %q is a SCIP local symbol, which is scoped to one index and cannot be resolved later; anchor the enclosing function or the file instead", target)
		}
	}
	return nil
}

// ParseAnchor parses one "kind:target" anchor, the form a flag or a config file carries.
//
// Split on the FIRST colon only: a SCIP symbol key is full of them
// ("m internal/cache/Store#Put()."), so splitting on the last would silently truncate
// exactly the anchors this store cares most about.
func ParseAnchor(s string) (Anchor, error) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return Anchor{}, fmt.Errorf("notes: anchor %q must be written as kind:target (kinds: symbol, file, project, target, note)", s)
	}
	a := Anchor{Kind: AnchorKind(strings.TrimSpace(s[:i])), Target: strings.TrimSpace(s[i+1:])}
	if err := Validate(Note{Name: "x", Title: "x", Anchors: []Anchor{a}}); err != nil {
		return Anchor{}, err
	}
	return a, nil
}

// List returns every note in name order. An error-severity issue fails the call rather
// than silently skipping the note; run Verify for every problem and its repair hint.
func List(dir string) ([]Note, error) {
	out, issues, err := Inspect(dir)
	if err != nil {
		return nil, err
	}
	if err := issuesError(issues); err != nil {
		return nil, err
	}
	return out, nil
}

// Verify scans every entry without hiding malformed files or broken links. A missing
// directory is valid and reports zero entries: declaring a path before writing the first
// note is a normal state, not a failure.
func Verify(dir string) (Verification, error) {
	out, issues, err := Inspect(dir)
	if err != nil {
		return Verification{}, err
	}
	return Verification{Notes: len(out), Issues: issues}, nil
}

// Inspect returns every magus note in the store, plus any issue found reading one.
//
// A FILE IN THE STORE IS NOT AUTOMATICALLY A NOTE. The store may be a directory magus
// owns, or it may be someone's Obsidian vault with thousands of files that have nothing to
// do with this workspace - and pointing at a vault is a supported thing to do. So the
// discriminator is explicit: a file is a magus note when its frontmatter declares
// `anchors:`, and anything else is somebody's writing that magus reads past in silence.
//
// Getting this wrong is not a small matter of tone. Treating every .md as a malformed note
// turned a 1,900-file vault into 1,500 error-severity issues and half a megabyte of output
// from `magus notes ls`, which then exited non-zero - the feature reporting the user's own
// notes as damage.
//
// The walk is RECURSIVE because vaults are foldered; a flat read silently ignored 400
// notes in a subdirectory, which is worse than failing. Dot-directories are skipped
// (.obsidian, .git, .trash all hold machinery rather than prose), and so is node_modules.
func Inspect(dir string) ([]Note, []Issue, error) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil // a declared store nobody has written to yet is normal
	}
	// A vault's template folder holds notes-in-waiting: placeholders that carry a magus
	// block because they are meant to be copied into one. Loading them mints notes for
	// documents nobody wrote. The folder is READ from the vault's own config rather than
	// guessed by name, so this is precise where a hardcoded "Templates" would be a guess.
	templates := obsidianTemplateDir(dir)
	out := make([]Note, 0)
	issues := make([]Issue, 0)
	scanned := 0

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is skipped, not fatal: one bad file must not hide a whole vault
		}
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return fs.SkipDir
			}
			if templates != "" && path == templates {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil // images and attachments are not notes and never were
		}
		// A sync conflict is a byte-for-byte copy of a note, magus block and all, so it
		// would otherwise load as a second note competing with the original - and if the
		// original declares an id, as a duplicate of that id. Obsidian Sync, Syncthing and
		// Dropbox each spell it differently; all three are recognisable.
		if isSyncConflict(d.Name()) {
			return nil
		}
		if scanned >= MaxNotes {
			return fs.SkipAll
		}
		scanned++

		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil //nolint:nilerr // unreachable for a path the walk just produced
		}
		name := strings.TrimSuffix(filepath.ToSlash(rel), ".md")

		n, isNote, readErr := readNoteFile(path, name)
		switch {
		case !isNote:
			return nil // not addressed to magus; leave it alone
		case readErr != nil:
			issues = append(issues, Issue{
				Severity: SeverityError, Code: CodeInvalidEntry, Path: path, Note: name,
				Message: readErr.Error(),
				Hint:    "This file carries a `magus:` frontmatter block, so magus reads it as a note. Repair it, or remove that block to keep the file out of the store.",
			})
		default:
			out = append(out, n)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("notes: list: %w", err)
	}
	if scanned >= MaxNotes {
		issues = append(issues, Issue{
			Severity: "warning", Code: CodeStoreTruncated, Path: dir,
			Message: fmt.Sprintf("stopped after scanning %d markdown files; any note beyond that is not loaded", MaxNotes),
			Hint:    "Point knowledge.notes.private at a subdirectory of the vault rather than its root, so magus reads the notes you anchored instead of everything you have ever written.",
		})
	}
	slices.SortFunc(out, func(a, b Note) int { return strings.Compare(a.Name, b.Name) })

	known := make(map[string]bool, len(out))
	for _, n := range out {
		known[n.Name] = true
	}
	for _, n := range out {
		for _, a := range n.Anchors {
			if a.Kind != AnchorNote {
				continue
			}
			if !known[a.Target] {
				issues = append(issues, Issue{
					Severity: SeverityError, Code: CodeMissingNote, Path: filepath.Join(dir, n.Name+".md"), Note: n.Name,
					Message: fmt.Sprintf("anchors to missing note %q", a.Target),
					Hint:    "Write the referenced note, correct the anchor, or remove it.",
				})
			}
		}
	}
	return out, issues, nil
}

func issuesError(issues []Issue) error {
	for _, issue := range issues {
		if issue.Severity == "error" {
			return fmt.Errorf("notes: store has invalid entries; run `magus notes verify` for repair steps (%s)", issue.Message)
		}
	}
	return nil
}

// Get returns one note by name, or os.ErrNotExist if it is absent.
func Get(dir, name string) (Note, error) {
	if err := validateStored(Note{Name: name, Title: "x", Anchors: []Anchor{{Kind: AnchorProject, Target: "."}}}); err != nil {
		return Note{}, err
	}
	// Fast path: the name is the note's path within the store, which is the common case.
	n, isNote, err := readNoteFile(filepath.Join(dir, filepath.FromSlash(name)+".md"), name)
	if err == nil && isNote && n.Name == name {
		return n, nil
	}
	// Otherwise the name may be a declared id whose file has since been moved or renamed -
	// which is the entire reason ids exist - so look for it rather than reporting it gone.
	if found, _, ferr := Inspect(dir); ferr == nil {
		for _, c := range found {
			if c.Name == name {
				return c, nil
			}
		}
	}
	if err != nil {
		return Note{}, err
	}
	if !isNote {
		// The file may well exist; it just is not a note. Reporting "not exist" keeps
		// callers' absent-vs-broken branching honest, since there is no note here either way.
		return Note{}, fmt.Errorf("notes: %q is not a magus note (it carries no `magus:` frontmatter block): %w", name, os.ErrNotExist)
	}
	return n, nil
}

// Path returns the file a note lives at, without reading it. `magus notes edit` needs
// this to hand a path to $EDITOR, including for a note that does not exist yet.
func Path(dir, name string) (string, error) {
	if !nameRE.MatchString(name) {
		return "", fmt.Errorf("notes: invalid name %q", name)
	}
	return filepath.Join(dir, name+".md"), nil
}

// Scaffold is the stub `magus notes edit` writes for a new note, so the author starts
// from a valid shape rather than a blank file. It is intentionally the smallest thing
// that passes Validate once the placeholder anchor is replaced.
//
// It returns a Note rather than the rendered bytes so that CREATING a note goes through
// Save like every other write here. Handing back bytes invited the caller to os.WriteFile
// them, which is what it did: the one path that brings a note into existence was the one
// path with neither validation nor an atomic write.
func Scaffold(name string) Note {
	n := Note{
		Name: name,
		// Stamped so the note survives being moved or renamed later, which is the normal
		// life of a file in a vault.
		ID:      name,
		Title:   strings.ReplaceAll(name, "-", " "),
		Anchors: []Anchor{{Kind: AnchorProject, Target: "."}},
	}
	n.Body = strings.Join([]string{
		"<!-- Replace the anchor above with what this note is actually about:",
		"     symbol:  a stable SCIP symbol key   (magus refs <name>)",
		"     file:    a workspace-relative path",
		"     project: a project path",
		"     target:  a target id",
		"     note:    another note's name",
		"     Anchor as narrowly as the knowledge allows; a coarse anchor is more",
		"     durable, and a symbol anchor survives the code moving file and line. -->",
		"",
		"Why this is true, and what a later reader has to know that the code does not say.",
	}, "\n")
	return n
}

// Save writes a note atomically after validating it. It is NOT a general write API for
// callers to build on: it exists for `magus notes edit` to lay down a scaffold and for
// verify to record a re-anchoring. There is no Put, and no agent-facing write path.
func Save(dir string, n Note) error {
	if n.ID == "" {
		n.ID = n.Name
	}
	if err := Validate(n); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("notes: save: %w", err)
	}
	path := filepath.Join(dir, filepath.FromSlash(n.Name)+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("notes: save: %w", err)
	}
	data, err := marshalNote(path, n)
	if err != nil {
		return err
	}
	// 0644 because a shared note is committed and read by everyone who clones; the obvious
	// temp-file-and-rename idiom leaves 0600. WriteFileAtomic also fsyncs, which the local
	// copy this replaced did not, so its crash-safety comment described what it did not do.
	if err := file.WriteFileAtomic(path, data, 0o644); err != nil {
		return fmt.Errorf("notes: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// frontmatterRE splits a note file into its YAML frontmatter and markdown body.
// \r? throughout: a note saved by a Windows editor (or checked out with
// core.autocrlf=true) would otherwise fail to match, and since a parse failure is
// error-severity that would take the ENTIRE store down rather than one file.
var frontmatterRE = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?(.*)\z`)

// FrontmatterKey is the single frontmatter key magus owns. Everything magus stores lives
// under it, and everything outside it belongs to someone else.
//
// One namespaced key rather than bare `id:`/`anchors:` at the top level, because a notes
// store may be an Obsidian vault whose frontmatter is already busy: `tags`, `aliases`,
// `cssclass`, and whatever the plugin ecosystem invented this month. `id` in particular is
// common enough that squatting on it would silently capture other tools' notes as magus
// ones. Claiming exactly one key makes "is this addressed to magus" a precise question
// rather than a guess about a generic name.
const FrontmatterKey = "magus"

// notePayload is what lives under FrontmatterKey. Split from Note so the wire shape is
// stated in one place and cannot drift from what Note happens to hold.
type notePayload struct {
	ID      string   `yaml:"id,omitempty"`
	Title   string   `yaml:"title"`
	Tags    []string `yaml:"tags,omitempty"`
	Anchors []Anchor `yaml:"anchors"`
}

// magusNode returns the value node under FrontmatterKey, or nil when the document does not
// address magus at all.
func magusNode(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == FrontmatterKey {
			return m.Content[i+1]
		}
	}
	return nil
}

// setMagusNode replaces (or appends) the magus block, leaving every other key and its
// ORDER untouched.
//
// Preserving the rest is not politeness, it is a correctness requirement: magus rewrites a
// note when it records an anchor fingerprint, and a naive re-marshal would silently delete
// the author's tags, aliases, and plugin metadata from their own vault.
func setMagusNode(doc *yaml.Node, payload notePayload) error {
	var value yaml.Node
	if err := value.Encode(payload); err != nil {
		return err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		m := &yaml.Node{Kind: yaml.MappingNode}
		doc.Kind, doc.Content = yaml.DocumentNode, []*yaml.Node{m}
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		m.Kind, m.Tag, m.Content, m.Value = yaml.MappingNode, "", nil, ""
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == FrontmatterKey {
			m.Content[i+1] = &value
			return nil
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: FrontmatterKey}, &value)
	return nil
}

// declaresMagus reports whether frontmatter is addressed to magus.
//
// A boolean rather than an error, because "this is someone else's note" is not a failure -
// it is the majority answer in any vault. Unparseable frontmatter answers false for the
// same reason: it cannot claim to be a magus note, so magus does not claim it.
func declaresMagus(frontmatter []byte) (*yaml.Node, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(frontmatter, &doc); err != nil {
		// Frontmatter magus cannot parse cannot be claiming to be a magus note, so it is
		// left alone rather than reported - the majority answer in any vault.
		return nil, false
	}
	return &doc, magusNode(&doc) != nil
}

// readNoteFile reads one file and reports (note, isNote, err).
//
// isNote false means the file is not addressed to magus - no frontmatter, unparseable
// frontmatter, or frontmatter that declares no anchors. That is the common case in a vault
// and it is not an error; only a file that DOES declare anchors is held to the schema.
//
// name is supplied by the caller because it comes from the path relative to the store, so
// a nested note is "daily/2026-08-01" rather than a basename that could collide.
func readNoteFile(path, name string) (Note, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Note{}, false, err // callers distinguish os.ErrNotExist
	}
	m := frontmatterRE.FindSubmatch(b)
	if m == nil {
		return Note{}, false, nil // plain prose; not ours
	}
	doc, ok := declaresMagus(m[1])
	if !ok {
		return Note{}, false, nil // frontmatter, but someone else's
	}
	var payload notePayload
	if err := magusNode(doc).Decode(&payload); err != nil {
		// It claimed the magus key and then would not decode, which IS worth reporting -
		// unlike the case above, where the file never addressed magus at all.
		return Note{}, true, fmt.Errorf("notes: %s: %w", filepath.Base(path), err)
	}

	var modified time.Time
	if st, err := os.Stat(path); err == nil {
		modified = st.ModTime()
	}
	n := Note{
		ID:       payload.ID,
		Title:    payload.Title,
		Tags:     payload.Tags,
		Anchors:  payload.Anchors,
		Body:     strings.TrimSpace(string(m[2])),
		Modified: modified,
	}
	// A declared id wins over the path, so moving or renaming the file inside the vault
	// keeps the note's identity and every anchor pointing at it.
	if n.ID != "" {
		if !nameRE.MatchString(n.ID) {
			return Note{}, true, fmt.Errorf("notes: %s: magus.id %q must be a kebab slug (lowercase alphanumerics and hyphens)", filepath.Base(path), n.ID)
		}
		n.Name = n.ID
	} else {
		n.Name = name
	}
	if err := validateStored(n); err != nil {
		return Note{}, true, fmt.Errorf("notes: %s: %w", filepath.Base(path), err)
	}
	return n, true, nil
}

// marshalNote renders a note, merging its magus block into any frontmatter already at
// path and leaving every other key untouched. An absent or unreadable file starts fresh.
func marshalNote(path string, n Note) ([]byte, error) {
	doc := &yaml.Node{}
	if existing, err := os.ReadFile(path); err == nil {
		if fm := frontmatterRE.FindSubmatch(existing); fm != nil {
			if parsed, ok := declaresMagus(fm[1]); ok || parsed != nil {
				doc = parsed
			}
		}
	}
	if err := setMagusNode(doc, notePayload{
		ID: n.ID, Title: n.Title, Tags: n.Tags, Anchors: n.Anchors,
	}); err != nil {
		return nil, fmt.Errorf("notes: render %s: %w", filepath.Base(path), err)
	}
	fm, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("notes: render %s: %w", filepath.Base(path), err)
	}

	body := strings.TrimSpace(n.Body)
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}
