package deps

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rogpeppe/go-internal/txtar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/types"
)

// extractFixture writes testdata/<name> into a temp dir and returns the dir. The
// fixtures are txtar archives because a bare lockfile under testdata would be neither
// a declared source of this module nor safe from the repository's lockfile checks.
func extractFixture(t *testing.T, name string) string {
	t.Helper()
	archive, err := txtar.ParseFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	dir := t.TempDir()
	for _, f := range archive.Files {
		path := filepath.Join(dir, f.Name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, f.Data, 0o644))
	}
	return dir
}

// writeFile writes body to name inside a new temp dir and returns its path.
func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	return writeFileIn(t, t.TempDir(), name, body)
}

func writeFileIn(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestReadersCoverEveryManager(t *testing.T) {
	t.Parallel()
	managers := map[string]string{}
	for manifest, r := range Readers {
		managers[manifest] = r.Manager
		require.NotNil(t, r.Read, manifest)
	}
	assert.Equal(t, map[string]string{
		"go.mod":         "gomod",
		"package.json":   "npm",
		"pyproject.toml": "python",
		"Cargo.toml":     "cargo",
	}, managers)
	assert.True(t, Readers["package.json"].UnderstandsLock("/w/console/pnpm-lock.yaml"))
	assert.False(t, Readers["package.json"].UnderstandsLock("/w/console/bun.lockb"))
	assert.False(t, Readers["go.mod"].UnderstandsLock("/w/go.sum"), "go.mod reads no lock")
}

// unread names every manifest and lock candidate a shipped spell declares that
// Readers deliberately does not read, with the reason. Shrinking it is the way to add
// a format; growing it needs a reason a reviewer accepts.
var unread = map[string]string{
	"setup.py":  "executable, and no lock pins what it would install",
	"setup.cfg": "no lock pins its ranges",
	"go.sum":    "go.mod pins exact versions itself; go.sum holds hashes, not a resolution",
	"bun.lockb": "binary; the text bun.lock is not yet a declared lock candidate",
}

// TestEverySpellManifestHasAReader is the parity between what the shipped spells
// declare and what the knowledge graph reads: a spell that adds a manifest or a lock
// candidate fails here until Readers reads it or unread says why not.
func TestEverySpellManifestHasAReader(t *testing.T) {
	t.Parallel()
	builtins := spell.Builtins()
	require.NotEmpty(t, builtins)

	declared := 0
	for name, d := range builtins {
		for _, m := range d.Manifests {
			declared++
			r, ok := Readers[m.Value]
			if !ok {
				assert.Contains(t, unread, m.Value, "spell %s declares manifest %s: add a reader to Readers or a reason to unread", name, m.Value)
				continue
			}
			for _, lock := range m.LockCandidates {
				if slices.Contains(r.Locks, lock) {
					continue
				}
				assert.Contains(t, unread, lock, "spell %s declares lock %s for %s: teach Readers[%q] to read it or add a reason to unread", name, lock, m.Value, m.Value)
			}
		}
	}
	assert.Positive(t, declared, "no built-in spell declared a manifest; the descriptors did not load")
}

// TestUnreadNamesOnlyWhatASpellDeclares keeps the allowlist from outliving the
// declaration it excuses.
func TestUnreadNamesOnlyWhatASpellDeclares(t *testing.T) {
	t.Parallel()
	declared := map[string]bool{}
	for _, d := range spell.Builtins() {
		for _, m := range d.Manifests {
			declared[m.Value] = true
			for _, l := range m.LockCandidates {
				declared[l] = true
			}
		}
	}
	for name := range unread {
		assert.True(t, declared[name], "unread excuses %s, which no shipped spell declares", name)
	}
}

func TestNormalizePythonFollowsPEP503(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Pillow":             "pillow",
		"pydantic_core":      "pydantic-core",
		"zope.interface":     "zope-interface",
		"Foo__Bar-._baz":     "foo-bar-baz",
		"already-normalized": "already-normalized",
	} {
		assert.Equal(t, want, normalizePython(in), in)
	}
}

func writeGoMod(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "go.mod")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// TestGoModule_RequireBlock covers the case that makes go.mod usable as an inventory
// without a lockfile or a toolchain: its require lines carry exact versions, so what is
// declared IS what resolves. It also pins the indirect marker, which is what separates
// "a dependency we chose" from "one something else dragged in".
func TestGoModule_RequireBlock(t *testing.T) {
	t.Parallel()
	got := GoModule(writeGoMod(t, `
module example.com/app

go 1.25

require (
	connectrpc.com/connect v1.20.0
	github.com/bmatcuk/doublestar/v4 v4.10.0
)

require golang.org/x/sys v0.30.0 // indirect
`))
	assert.Equal(t, []types.KnowledgePackage{
		{Manager: "gomod", Name: "connectrpc.com/connect", Version: "v1.20.0"},
		{Manager: "gomod", Name: "github.com/bmatcuk/doublestar/v4", Version: "v4.10.0"},
		{Manager: "gomod", Name: "golang.org/x/sys", Version: "v0.30.0", Indirect: true},
	}, got)
}

// TestGoModule_ReplaceRecordsWhatBuilds pins the decision that a replaced module is
// recorded at its REPLACEMENT's version. Recording the original requirement would
// describe a version that is not on disk and never compiled (the precise flavour of
// wrong this graph exists to prevent), so the node follows what builds and the Replaced
// flag is what keeps that visible rather than silent.
func TestGoModule_ReplaceRecordsWhatBuilds(t *testing.T) {
	t.Parallel()
	got := GoModule(writeGoMod(t, `
module example.com/app

go 1.25

require github.com/pkg/errors v0.8.0

replace github.com/pkg/errors => github.com/pkg/errors v0.9.1
`))
	assert.Equal(t, []types.KnowledgePackage{
		{Manager: "gomod", Name: "github.com/pkg/errors", Version: "v0.9.1", Replaced: true},
	}, got)
}

// TestGoModule_LocalReplaceIsDropped covers the replacement form that has no version to
// record at all. A directory replacement is how this repo wires libs/gopherbuzz and
// libs/diagnostics, so it is the common case here, not an exotic one, and a local
// module is a sibling project the graph already knows as a project node, not a
// third-party package.
func TestGoModule_LocalReplaceIsDropped(t *testing.T) {
	t.Parallel()
	got := GoModule(writeGoMod(t, `
module example.com/app

go 1.25

require (
	example.com/lib v0.0.0
	connectrpc.com/connect v1.20.0
)

replace example.com/lib => ./libs/lib
`))
	assert.Equal(t, []types.KnowledgePackage{
		{Manager: "gomod", Name: "connectrpc.com/connect", Version: "v1.20.0"},
	}, got, "a directory replacement has no pin to record and is not third-party")
}

// TestGoModule_UnreadableYieldsNothing pins the best-effort contract. Every caller is a
// graph loader assembling a shard, where one project's malformed manifest must be that
// project's absent data rather than a failed workspace build.
func TestGoModule_UnreadableYieldsNothing(t *testing.T) {
	t.Parallel()
	assert.Nil(t, GoModule(filepath.Join(t.TempDir(), "absent", "go.mod")), "a missing file")
	assert.Nil(t, GoModule(writeGoMod(t, "this is not a go.mod\n")), "an unparseable file")
	assert.Nil(t, GoModule(writeGoMod(t, "module example.com/app\n\ngo 1.25\n")), "no requires")
}
