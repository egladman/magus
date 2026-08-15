package deps

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

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
// describe a version that is not on disk and never compiled - the precise flavour of
// wrong this graph exists to prevent - so the node follows what builds and the Replaced
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
// libs/diagnostics, so it is the common case here, not an exotic one - and a local
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
