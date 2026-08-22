package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/docs"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/spells"
)

// useRepoSpells points the example and doc-comment readers at the real spells/
// tree. The tests run in this command's own directory; main() takes the path as
// a flag because the caller knows where spells live in its own context.
func useRepoSpells(t *testing.T) {
	t.Helper()
	orig := spellsDir
	t.Cleanup(func() { spellsDir = orig })
	spellsDir = "../../spells"
}

// TestEveryBuiltinHasEditorialMetadata is the check main() makes before writing
// anything, as a test: a spell added to the registry with no spellMeta entry
// would otherwise only fail at generate time, and the page it needs is prose
// nobody can derive from the Descriptor.
func TestEveryBuiltinHasEditorialMetadata(t *testing.T) {
	for name := range spellruntime.Builtins() {
		meta, ok := spellMeta[name]
		if !assert.True(t, ok, "built-in spell %q has no spellMeta entry", name) {
			continue
		}
		assert.NotEmpty(t, meta.dir, "%s names no source directory", name)
		assert.NotEmpty(t, meta.description, "%s has no frontmatter description", name)
		assert.NotEmpty(t, meta.intro, "%s has no page intro", name)

		_, err := os.Stat(filepath.Join("../../spells", meta.dir, "spell.buzz"))
		assert.NoError(t, err, "%s points at a source directory that does not exist", name)
	}

	for name := range spellMeta {
		assert.Contains(t, spellruntime.Builtins(), name, "spellMeta describes %q, which is not a built-in", name)
	}
}

// TestRenderSpellReportsTheDescriptor walks every built-in rather than one
// hand-picked spell: the page is generated FROM the Descriptor, so the only
// honest assertion is that each fact on the page follows from the registry entry
// that produced it.
func TestRenderSpellReportsTheDescriptor(t *testing.T) {
	useRepoSpells(t)

	for name, d := range spellruntime.Builtins() {
		t.Run(name, func(t *testing.T) {
			body := renderSpell(d)
			meta := spellMeta[name]

			fm, ok := docs.ParseFrontmatter(body)
			require.True(t, ok, "the page has no parsable frontmatter")
			assert.Equal(t, name+" spell", fm.Title)
			assert.Equal(t, meta.description, fm.Description)
			assert.Equal(t, "spells/"+meta.dir+"/spell.buzz", fm.GeneratedFrom)
			assert.Equal(t, fm.Tags[0], name, "the spell's own name leads its tags")
			assert.Equal(t, len(fm.Tags), len(slices.Compact(slices.Sorted(slices.Values(fm.Tags)))), "tags are deduped: %v", fm.Tags)

			assert.Contains(t, body, "# "+name+"\n\n"+meta.intro)
			assert.Contains(t, body, "**Runtime name:** `"+name+"` (source `spells/"+meta.dir+"/`)")
			assert.Contains(t, body, "## Passing arguments to ops")

			if d.Opaque {
				assert.Contains(t, body, "**Opaque:** yes")
			} else {
				assert.NotContains(t, body, "**Opaque:**")
			}
			if len(d.Provides) > 0 {
				assert.Contains(t, body, "**Provides:** "+codeList(d.Provides))
			} else {
				assert.NotContains(t, body, "**Provides:**")
			}

			probed := false
			for _, tool := range d.Tools {
				if tool.Probe.Bin != "" {
					probed = true
				}
			}
			if probed {
				assert.NotContains(t, body, "**Version probe:** none")
			} else {
				assert.Contains(t, body, "**Version probe:** none")
			}

			for _, op := range d.OpNames() {
				assert.Contains(t, body, "\n## "+op+"\n", "no section for op %s", op)
				if cmd := resolvedArgv(d.Ops[op]); cmd != "" {
					assert.Contains(t, body, "**Command:** `"+cmd+"`")
				} else {
					assert.Contains(t, body, "**Command:** none; this op composes the spell's other ops")
				}
			}
		})
	}
}

// TestExamplesAreMarkedRunnable pins the recorder marker: without it the block
// is inert text, and the whole point of the example is that a reader can see the
// plan a target would produce without the toolchain installed.
func TestExamplesAreMarkedRunnable(t *testing.T) {
	useRepoSpells(t)

	d := spellruntime.Builtins()["go"]
	require.NotEmpty(t, d.Ops)
	body := renderSpell(d)

	require.NotEmpty(t, readExample("go", "go-build"), "this test is pinned to an op that ships an example")
	assert.Contains(t, body, "### Example\n\n<!-- magus-run-recorder -->\n```buzz\n")
	assert.Contains(t, body, "\n```\n", "the example block is closed")
}

// TestOpDocsComeFromTheSource: built-in Descriptors carry no per-op Doc (the
// bytecode strips it), so the handler comments only survive in spell.buzz. A
// silent read or parse miss renders every op with no description at all.
func TestOpDocsComeFromTheSource(t *testing.T) {
	useRepoSpells(t)

	docsByOp := parseOpDocs("golang")
	require.NotEmpty(t, docsByOp, "no op docs recovered from spells/golang/spell.buzz")

	d := spellruntime.Builtins()["go"]
	for op, doc := range docsByOp {
		assert.Contains(t, d.Ops, op, "recovered a doc for %q, which is not an op of the go spell", op)
		assert.NotContains(t, doc, "\n", "a doc lands in Markdown prose, so it is flowed to one paragraph")
		assert.Equal(t, strings.TrimSpace(doc), doc)
	}
}

func TestParseOpDocsIsBestEffort(t *testing.T) {
	useRepoSpells(t)
	assert.Nil(t, parseOpDocs("no-such-spell"), "a missing source yields no docs rather than an error")
}

func TestCleanDoc(t *testing.T) {
	assert.Equal(t, "one flowed paragraph from two", cleanDoc("one flowed\nparagraph\n\nfrom two"))
	assert.Equal(t, "", cleanDoc("   \n\n  "))
}

// TestDescribeCharm renders the argv patch in English, because a reader should
// not have to parse JSON Pointer to learn what `:rw` does.
func TestDescribeCharm(t *testing.T) {
	args := []string{"fmt", "-l", "./..."}
	for _, tc := range []struct {
		what string
		ops  []spells.PatchOp
		want string
	}{
		{"append", []spells.PatchOp{{Op: "add", Path: "/-", Value: "-w"}}, "Appends `-w`."},
		{"insert", []spells.PatchOp{{Op: "add", Path: "/1", Value: "-w"}}, "Inserts `-w`."},
		{"remove a named argument", []spells.PatchOp{{Op: "remove", Path: "/1"}}, "Drops `-l`."},
		{"remove an argument it cannot name", []spells.PatchOp{{Op: "remove", Path: "/9"}}, "Drops an argument."},
		{"replace a named argument", []spells.PatchOp{{Op: "replace", Path: "/1", Value: "-w"}}, "Replaces `-l` with `-w`."},
		{"replace an argument it cannot name", []spells.PatchOp{{Op: "replace", Path: "/-", Value: "-w"}}, "Sets an argument to `-w`."},
		{"move", []spells.PatchOp{{Op: "move", Path: "/2", From: "/1"}}, "Moves an argument."},
		{"copy", []spells.PatchOp{{Op: "copy", Path: "/2", From: "/1"}}, "Copies an argument."},
		{"several", []spells.PatchOp{{Op: "remove", Path: "/1"}, {Op: "add", Path: "/-", Value: "-w"}}, "Drops `-l`, appends `-w`."},
		{"a shape it does not describe", []spells.PatchOp{{Op: "test", Path: "/1", Value: "-l"}}, "A 1-op argv patch."},
		{"nothing at all", nil, "A 0-op argv patch."},
	} {
		t.Run(tc.what, func(t *testing.T) {
			assert.Equal(t, tc.want, describeCharm(spells.Charm{Ops: tc.ops}, args))
		})
	}
}

func TestArgAt(t *testing.T) {
	args := []string{"fmt", "-l"}
	assert.Equal(t, "fmt", argAt(args, "/0"))
	assert.Equal(t, "-l", argAt(args, "/1"))
	assert.Equal(t, "", argAt(args, "/2"), "out of range")
	assert.Equal(t, "", argAt(args, "/-"), "the append position names no element")
	assert.Equal(t, "", argAt(args, "1"), "a JSON Pointer starts with a slash")
	assert.Equal(t, "", argAt(args, "/-1"))
}

// TestCharmPatchJSON pins the raw notation behind the details dropdown: it is
// there for a reader who wants RFC 6902, so it has to round-trip as one.
func TestCharmPatchJSON(t *testing.T) {
	ops := []spells.PatchOp{{Op: "replace", Path: "/1", Value: "-w"}, {Op: "move", Path: "/2", From: "/0"}}
	got := charmPatchJSON(spells.Charm{Ops: ops})

	var round []spells.PatchOp
	require.NoError(t, json.Unmarshal([]byte(got), &round))
	assert.Equal(t, ops, round)
	assert.Contains(t, got, "\n  ", "the patch is indented for a reader")
}

func TestCapitalize(t *testing.T) {
	assert.Equal(t, "Drops `-l`", capitalize("drops `-l`"))
	assert.Equal(t, "Already up", capitalize("Already up"))
	assert.Equal(t, "`-l` stays", capitalize("`-l` stays"))
	assert.Equal(t, "", capitalize(""))
}

func TestSortedCharmNames(t *testing.T) {
	assert.Equal(t, []string{"quiet", "rw", "strict"}, sortedCharmNames(map[string]spells.Charm{
		"rw": {}, "strict": {}, "quiet": {},
	}))
	assert.Empty(t, sortedCharmNames(nil))
}

func TestResolvedArgv(t *testing.T) {
	assert.Equal(t, "go build ./...", resolvedArgv(spells.Op{Command: spells.Command{Bin: "go", Args: []string{"build", "./..."}}}))
	assert.Equal(t, "go", resolvedArgv(spells.Op{Command: spells.Command{Bin: "go"}}))
	assert.Equal(t, "", resolvedArgv(spells.Op{}), "a marker op with no command composes others")
}

func TestCodeListAndDedupe(t *testing.T) {
	assert.Equal(t, "`a`, `b`", codeList([]string{"a", "b"}))
	assert.Equal(t, "", codeList(nil))
	assert.Equal(t, []string{"a", "b", "c"}, dedupe([]string{"a", "b", "a", "c", "b"}))
}

// TestWriteArgsSectionNamesTheInvoker: one helper renders both the per-spell
// pages and the index, so the options contract cannot be stated two ways.
func TestWriteArgsSectionNamesTheInvoker(t *testing.T) {
	var b strings.Builder
	writeArgsSection(&b, "go")
	got := b.String()

	assert.Contains(t, got, "Every op is invoked as `go[\"<op>\"](ctx, opts?)`")
	assert.Contains(t, got, "| `args` | `[str]` |")
	assert.Contains(t, got, "| `stdin` | `str` |")
	assert.Contains(t, got, "`go[\"<op>\"](ctx.withCwd(\"sub\"))`")
	assert.Contains(t, got, docs.RepoBlob+"/"+spellOptsSource, "each key links to the line that parses it")
}

// TestReadExampleReturnsNothingForAMissingFile: a missing example simply skips
// the Example section rather than failing the page.
func TestReadExampleReturnsNothingForAMissingFile(t *testing.T) {
	useRepoSpells(t)
	assert.Equal(t, "", readExample("go", "no-such-op"))
}

// TestPruneUnregisteredRemovesRenamedPages is the fix for a rename that left
// four stale pages committed and still linked: nothing looked wrong until a
// clean swept them and the link gate failed on pages every checkout still had.
func TestPruneUnregisteredRemovesRenamedPages(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"go.md", "ts.md", "notes.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub.md"), 0o755))

	require.NoError(t, pruneUnregistered(dir, []string{"go"}))

	for _, kept := range []string{"go.md", "notes.txt", "sub.md"} {
		_, err := os.Stat(filepath.Join(dir, kept))
		assert.NoError(t, err, "%s was removed", kept)
	}
	_, err := os.Stat(filepath.Join(dir, "ts.md"))
	assert.True(t, os.IsNotExist(err), "the renamed spell's page survived")
}

func TestPruneUnregisteredReportsAnUnreadableDirectory(t *testing.T) {
	assert.Error(t, pruneUnregistered(filepath.Join(t.TempDir(), "absent"), nil))
}

// landing builds a stand-in for docs/spells.md: hand-written prose either side
// of the generated region.
func landing(begin, end string) string {
	return "# Spells\n\nA spell is a plugin.\n\n" + begin + "\nstale table\n" + end + "\n\nMore prose.\n"
}

// TestInjectSpellListRewritesOnlyTheMarkedRegion: the at-a-glance list lives on
// the hand-written concept page so /spells/ both explains what a spell is and
// lists every one, which only works if regenerating leaves the prose alone.
func TestInjectSpellListRewritesOnlyTheMarkedRegion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spells.md")
	require.NoError(t, os.WriteFile(path, []byte(landing(spellListBegin+" -->", spellListEnd)), 0o644))

	builtins := spellruntime.Builtins()
	names := slices.Sorted(maps.Keys(builtins))
	require.NoError(t, injectSpellList(path, builtins, names))

	got := readFile(t, path)
	assert.Contains(t, got, "A spell is a plugin.")
	assert.Contains(t, got, "More prose.")
	assert.NotContains(t, got, "stale table")
	assert.Contains(t, got, "| Spell | Language | Ops | Purpose |")
	for _, name := range names {
		meta := spellMeta[name]
		lang := meta.language
		if lang == "" {
			lang = "-"
		}
		assert.Contains(t, got, "| [`"+name+"`](spells/"+name+".md) | "+lang+" | ")
	}

	// Re-running is idempotent: an unchanged region is not rewritten, so the
	// generate drift gate does not report a file that did not move.
	require.NoError(t, injectSpellList(path, builtins, names))
	assert.Equal(t, got, readFile(t, path))
}

func TestInjectSpellListRefusesAMalformedPage(t *testing.T) {
	builtins := spellruntime.Builtins()
	names := slices.Sorted(maps.Keys(builtins))

	for _, tc := range []struct {
		what, content, wantErr string
	}{
		{"no begin marker", "# Spells\n" + spellListEnd + "\n", spellListBegin},
		{"no end marker", "# Spells\n" + spellListBegin + " -->\n", spellListEnd},
		{"end before begin", spellListEnd + "\n" + spellListBegin + " -->\n", spellListEnd},
		{"a begin marker with no line to end", "# Spells\n" + spellListBegin, "malformed begin marker"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "spells.md")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o644))
			assert.ErrorContains(t, injectSpellList(path, builtins, names), tc.wantErr)
		})
	}

	assert.Error(t, injectSpellList(filepath.Join(t.TempDir(), "absent.md"), builtins, names))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}
