package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validNote() Note {
	return Note{
		Name:    "cache-invalidation-pairing",
		Title:   "The two caches invalidate together",
		Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m internal/cache/Store#Put()."}},
		Body:    "Nothing in the code says these must be cleared in the same operation.",
	}
}

// TestSharedDirRequiresADeclaration is the load-bearing test for the guard rule: with nothing
// declared the feature is entirely off, so no path can be judged and none is guessed.
func TestSharedDirRequiresADeclaration(t *testing.T) {
	root := t.TempDir()

	_, err := Dir(root, ScopeShared, "")
	assert.ErrorIs(t, err, ErrDisabled, "no declaration means the feature is off, not that a default location is invented")
	_, err = Dir(root, ScopeShared, "   ")
	assert.ErrorIs(t, err, ErrDisabled, "whitespace is not a declaration")

	dir, err := Dir(root, ScopeShared, "notes")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "notes"), dir)

	dir, err = Dir(root, ScopeShared, "docs/team/notes")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "docs", "team", "notes"), dir, "the path is the workspace's to choose, not a fixed convention")
}

// TestSharedDirRejectsEscapes keeps a declaration from reaching outside the checkout, which
// would put notes somewhere git cannot attribute them.
func TestSharedDirRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	for _, declared := range []string{"/etc/notes", "../notes", "../../elsewhere"} {
		_, err := Dir(root, ScopeShared, declared)
		assert.Error(t, err, "declared path %q must be rejected", declared)
		assert.NotErrorIs(t, err, ErrDisabled, "an escaping path is a misconfiguration, not a disabled feature")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Note)
		wantErr string
	}{
		{name: "valid", mutate: func(*Note) {}},
		{
			name:    "name must be a kebab slug",
			mutate:  func(n *Note) { n.Name = "Not A Slug" },
			wantErr: "kebab slug",
		},
		{
			name:    "name may not escape the directory",
			mutate:  func(n *Note) { n.Name = "../escape" },
			wantErr: "kebab slug",
		},
		{
			name:    "title is required",
			mutate:  func(n *Note) { n.Title = "  " },
			wantErr: "needs a title",
		},
		{
			name:    "at least one anchor",
			mutate:  func(n *Note) { n.Anchors = nil },
			wantErr: "at least one anchor",
		},
		{
			name:    "anchor kind is a closed set",
			mutate:  func(n *Note) { n.Anchors[0].Kind = "line" },
			wantErr: "anchor kind must be one of",
		},
		{
			name:    "anchor target is required",
			mutate:  func(n *Note) { n.Anchors[0].Target = " " },
			wantErr: "empty target",
		},
		{
			name:    "anchor target is single-line",
			mutate:  func(n *Note) { n.Anchors[0].Target = "a\nb" },
			wantErr: "single line",
		},
		{
			// SCIP local symbols are index-scoped counters the spec forbids using outside
			// their own Document. Accepting one produces a note that dangles on the next
			// index build for a reason the note itself cannot explain.
			name:    "a SCIP local symbol is refused",
			mutate:  func(n *Note) { n.Anchors[0].Target = "local 0" },
			wantErr: "SCIP local symbol",
		},
		{
			name:   "a coarse anchor is legitimate",
			mutate: func(n *Note) { n.Anchors = []Anchor{{Kind: AnchorProject, Target: "."}} },
		},
		{
			name: "prose is the payload, not a caption",
			mutate: func(n *Note) {
				n.Body = "A long explanation that no ref could ever stand in for."
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := validNote()
			tc.mutate(&n)
			err := Validate(n)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestRoundTrip proves a note survives disk without losing prose, anchors, or the
// fields verify later writes back.
func TestRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	want := validNote()
	want.Tags = []string{"cache", "gotcha"}
	want.Anchors = append(want.Anchors, Anchor{
		Kind: AnchorFile, Target: "internal/cache/cache.go",
		Digest: "abc123", Commit: "deadbeef",
	})
	require.NoError(t, Save(dir, want))

	got, err := Get(dir, want.Name)
	require.NoError(t, err)
	assert.Equal(t, want.Title, got.Title)
	assert.Equal(t, want.Tags, got.Tags)
	assert.Equal(t, want.Anchors, got.Anchors)
	assert.Equal(t, want.Body, got.Body)
	assert.False(t, got.Modified.IsZero(), "Modified is observed from the file, not stored")
}

// TestTimestampsAreNotSerialized: these files are hand-edited, so a stored timestamp is
// wrong the first time someone saves without going through magus. Deriving it is the only
// way it cannot disagree with the file.
func TestTimestampsAreNotSerialized(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, validNote()))

	raw, err := os.ReadFile(filepath.Join(dir, "cache-invalidation-pairing.md"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "magus:")
	assert.NotContains(t, string(raw), "created:")
	assert.NotContains(t, string(raw), "updated:")
	assert.NotContains(t, string(raw), "modified:")
}

// TestIdSurvivesARename is the property that makes a vault usable. Obsidian's rename
// rewrites every [[wikilink]] and knows nothing about magus, so identity that encodes a
// location would break every note-kind anchor the moment someone tidied a folder.
func TestIdSurvivesARename(t *testing.T) {
	dir := t.TempDir()
	body := "---\nmagus:\n  id: cache-pairing\n  title: Two caches\n  anchors:\n    - kind: project\n      target: .\n---\n\nProse.\n"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "Archive", "2026"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Some Note.md"), []byte(body), 0o644))

	n, err := Get(dir, "cache-pairing")
	require.NoError(t, err)
	assert.Equal(t, "Two caches", n.Title)

	// The author reorganizes the vault. The identity does not move with the file.
	require.NoError(t, os.Rename(filepath.Join(dir, "Some Note.md"),
		filepath.Join(dir, "Archive", "2026", "Renamed Entirely.md")))

	n, err = Get(dir, "cache-pairing")
	require.NoError(t, err, "a declared id must outlive the path it was first written at")
	assert.Equal(t, "Two caches", n.Title)

	found, issues, err := Inspect(dir)
	require.NoError(t, err)
	assert.Empty(t, issues)
	require.Len(t, found, 1)
	assert.Equal(t, "cache-pairing", found[0].Name, "the id is the identity, not the path")
}

// TestPathIsTheIdentityWithoutAnId: a hand-written vault note that never declared one is
// still addressable, just by where it sits.
func TestPathIsTheIdentityWithoutAnId(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "Daily"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Daily", "Note.md"),
		[]byte("---\nmagus:\n  title: T\n  anchors:\n    - kind: project\n      target: .\n---\n\nx\n"), 0o644))

	n, err := Get(dir, "Daily/Note")
	require.NoError(t, err)
	assert.Equal(t, "Daily/Note", n.Name)
	assert.Empty(t, n.ID)
}

// TestIdMustBeASlug: the id becomes a graph node ID, so it cannot be free text.
func TestIdMustBeASlug(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.md"),
		[]byte("---\nmagus:\n  id: Not A Slug\n  title: T\n  anchors:\n    - kind: project\n      target: .\n---\n\nx\n"), 0o644))

	_, issues, err := Inspect(dir)
	require.NoError(t, err)
	require.Len(t, issues, 1, "a note that claims a bad id IS addressed to magus, so it is reported")
	assert.Contains(t, issues[0].Message, "kebab slug")
}

// TestMissingDirectoryIsValid: declaring a path before writing the first note is a
// normal state. The store must not treat an empty feature as a broken one.
func TestMissingDirectoryIsValid(t *testing.T) {
	v, err := Verify(filepath.Join(t.TempDir(), "never-created"))
	require.NoError(t, err)
	assert.Equal(t, Verification{Notes: 0, Issues: nil}, v)
}

func TestInspectReportsInvalidEntries(t *testing.T) {
	dir := t.TempDir()
	// Declares anchors, so it IS addressed to magus - and is broken. That is the only
	// shape that earns an error; see TestForeignFilesAreNotNotes for what does not.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.md"),
		[]byte("---\nmagus:\n  title: T\n  anchors:\n    - kind: nonsense\n      target: x\n---\n\nprose\n"), 0o644))
	require.NoError(t, Save(dir, validNote()))

	got, issues, err := Inspect(dir)
	require.NoError(t, err)
	assert.Len(t, got, 1, "the readable note is still returned")
	require.Len(t, issues, 1)
	assert.Equal(t, SeverityError, issues[0].Severity)
	assert.Equal(t, CodeInvalidEntry, issues[0].Code)

	_, err = List(dir)
	assert.Error(t, err, "List refuses to quietly skip a broken entry")
}

func TestInspectReportsMissingNoteAnchor(t *testing.T) {
	dir := t.TempDir()
	n := validNote()
	n.Anchors = []Anchor{{Kind: AnchorNote, Target: "was-never-written"}}
	require.NoError(t, Save(dir, n))

	_, issues, err := Inspect(dir)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, CodeMissingNote, issues[0].Code)
	assert.Equal(t, n.Name, issues[0].Note)
}

// TestScaffoldIsValidOnceAnchored proves `magus notes edit <new>` hands the author a
// file that already passes the schema, so the first save is not a guessing game.
//
// It goes through Save rather than writing bytes, because that is what the command does
// and it is the whole reason Scaffold returns a Note: Save validates, so this call
// succeeding IS the proof that the placeholder satisfies the schema.
func TestScaffoldIsValidOnceAnchored(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Save(dir, Scaffold("fresh-note")))

	got, err := Get(dir, "fresh-note")
	require.NoError(t, err)
	assert.Equal(t, "fresh note", got.Title)
	require.Len(t, got.Anchors, 1)
	assert.Equal(t, AnchorProject, got.Anchors[0].Kind)
	assert.Contains(t, got.Body, "Replace the anchor above")
}

// TestForeignFilesAreNotNotes is the guard on the case that will actually happen: someone
// points knowledge.notes.private at their Obsidian vault. Files that are not addressed to
// magus must be invisible to it, not reported as damage.
func TestForeignFilesAreNotNotes(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "Daily Notes"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".obsidian"), 0o755))

	write := func(rel, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(body), 0o644))
	}
	write("Note About Thing.md", "---\ntags: [project]\naliases: [thing]\n---\n\n# Thing\n")                 // Obsidian frontmatter
	write("plain.md", "no frontmatter at all\n")                                                             // plain prose
	write("Daily Notes/2026-08-01.md", "---\ntags: [daily]\n---\n\nWhat I did.\n")                           // nested, foreign
	write(".obsidian/workspace.md", "---\nmagus:\n  anchors:\n    - kind: project\n      target: .\n---\nx") // machinery, skipped
	write("attachment.png", "not markdown")
	// The one real note, nested, with a name no kebab rule would accept as a filename.
	write("Daily Notes/Cache Pairing.md", "---\nmagus:\n  title: Two caches\n  anchors:\n    - kind: project\n      target: .\n---\n\nProse.\n")

	got, issues, err := Inspect(dir)
	require.NoError(t, err)
	assert.Empty(t, issues, "a vault full of someone else's writing is not a pile of errors")
	require.Len(t, got, 1, "only the file declaring a magus block is a magus note")
	assert.Equal(t, "Daily Notes/Cache Pairing", got[0].Name, "the name is the path within the store, so nesting cannot collide")
	assert.Equal(t, "Two caches", got[0].Title)

	// And it is reachable by that name.
	n, err := Get(dir, "Daily Notes/Cache Pairing")
	require.NoError(t, err)
	assert.Equal(t, "Two caches", n.Title)

	// A file that exists but is not a note reads as absent, so callers branch correctly.
	_, err = Get(dir, "plain")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// TestGetRefusesTraversal: the name is joined back onto the store directory, and it now
// admits slashes, so the traversal check is what stands between a name and the filesystem.
func TestGetRefusesTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../escape", "a/../../escape", "/etc/passwd", "", "."} {
		_, err := Get(dir, name)
		assert.Error(t, err, "name %q must be refused", name)
	}
}

// TestSaveKeepsForeignFrontmatter is the coexistence guarantee. magus rewrites a note when
// it records an anchor fingerprint, so a naive re-marshal would silently delete the
// author's tags, aliases, and plugin metadata from their own vault - turning a read-mostly
// integration into one that quietly damages what it touches.
func TestSaveKeepsForeignFrontmatter(t *testing.T) {
	dir := t.TempDir()
	original := "---\n" +
		"tags:\n    - architecture\n    - cache\n" +
		"aliases:\n    - the pairing note\n" +
		"cssclass: wide\n" +
		"magus:\n    id: cache-pairing\n    title: Old title\n    anchors:\n        - kind: project\n          target: .\n" +
		"---\n\nProse the author wrote.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cache-pairing.md"), []byte(original), 0o644))

	n, err := Get(dir, "cache-pairing")
	require.NoError(t, err)
	n.Title = "New title"
	n.Anchors[0].Digest = "abc123abc123abc1"
	require.NoError(t, Save(dir, n))

	raw, err := os.ReadFile(filepath.Join(dir, "cache-pairing.md"))
	require.NoError(t, err)
	got := string(raw)

	assert.Contains(t, got, "architecture", "the author's tags survive")
	assert.Contains(t, got, "the pairing note", "and their aliases")
	assert.Contains(t, got, "cssclass", "and any plugin key magus has never heard of")
	assert.Contains(t, got, "New title", "while the magus block is updated")
	assert.Contains(t, got, "abc123abc123abc1")
	assert.Contains(t, got, "Prose the author wrote.", "and the body is untouched")

	// Key ORDER is preserved too: a diff of someone's vault should show the magus block
	// changing and nothing else moving.
	assert.Less(t, strings.Index(got, "tags:"), strings.Index(got, "magus:"))
	assert.Less(t, strings.Index(got, "aliases:"), strings.Index(got, "magus:"))
}

// TestSyncConflictsAreNotNotes: a file-sync tool leaves a byte-for-byte copy of a note
// beside it, magus block and all. Loading one mints a second note competing with the
// original - and where the original declares an id, a duplicate of that id.
func TestSyncConflictsAreNotNotes(t *testing.T) {
	dir := t.TempDir()
	note := "---\nmagus:\n  id: cache-pairing\n  title: Two caches\n  anchors:\n    - kind: project\n      target: .\n---\n\nProse.\n"
	for _, name := range []string{
		"cache-pairing.md",
		"cache-pairing.sync-conflict-20260813-090000-ABCDEFG.md", // Obsidian Sync
		"cache-pairing.sync-conflict-20260813-090000.md",         // Syncthing
		"cache-pairing (conflicted copy 2026-08-13).md",          // Dropbox
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(note), 0o644))
	}

	got, issues, err := Inspect(dir)
	require.NoError(t, err)
	assert.Empty(t, issues)
	require.Len(t, got, 1, "only the note itself loads; its conflicted copies are not notes")
	assert.Equal(t, "cache-pairing", got[0].Name)
}

// TestObsidianTemplateFolderIsSkipped: a template carries a magus block because it is meant
// to be COPIED into one, so loading it mints a note for a document nobody wrote. The folder
// is read from the vault's own config rather than guessed, so a store that happens to have a
// directory called Templates and no vault config keeps every note in it.
func TestObsidianTemplateFolderIsSkipped(t *testing.T) {
	note := "---\nmagus:\n  title: T\n  anchors:\n    - kind: project\n      target: .\n---\n\nx\n"

	withVaultConfig := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(withVaultConfig, ".obsidian"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(withVaultConfig, "Meta", "Templates"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(withVaultConfig, ".obsidian", "templates.json"),
		[]byte(`{"folder":"Meta/Templates"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(withVaultConfig, "Meta", "Templates", "Note.md"), []byte(note), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(withVaultConfig, "real.md"), []byte(note), 0o644))

	got, _, err := Inspect(withVaultConfig)
	require.NoError(t, err)
	require.Len(t, got, 1, "the configured template folder contributes nothing")
	assert.Equal(t, "real", got[0].Name)

	// No vault config: nothing is skipped by name alone.
	plain := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(plain, "Templates"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(plain, "Templates", "Note.md"), []byte(note), 0o644))

	got, _, err = Inspect(plain)
	require.NoError(t, err)
	assert.Len(t, got, 1, "a directory named Templates is only a template folder if the vault says so")
}

// TestSaveWritesAWorldReadableNote pins the permission, because the obvious atomic-write
// idiom gets it wrong: os.CreateTemp makes 0600, and a rename carries that through. A
// shared note is committed and read by everyone who clones the repo, so a store that
// quietly wrote owner-only files would produce a repo other people cannot read.
func TestSaveWritesAWorldReadableNote(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Save(dir, Scaffold("perms")))

	info, err := os.Stat(filepath.Join(dir, "perms.md"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}
