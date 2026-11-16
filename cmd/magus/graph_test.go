package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/internal/maintenance"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/libs/oci"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"google.golang.org/protobuf/proto"
)

// TestEncodeFragmentDeterminism confirms that render.EncodeFragmentRaw produces
// byte-for-byte identical output for the same input across two calls. This relies
// on gzip.NewWriter leaving the header ModTime at its zero value by default, so the
// compressed stream is deterministic: a necessary property for stable #data= URL
// fragments in MAGUS.md. The test exercises the shared encoder that both the render
// package (per-project MAGUS.md deep links) and cmd/magus (graph open) use, proving
// browser wire-format parity is preserved when a single implementation is used.
func TestEncodeFragmentDeterminism(t *testing.T) {
	payload := []byte(`{"projects":[{"path":"pkg/foo","engine":"buzz","nodes":[{"name":"build","dependencies":["fmt"]},{"name":"fmt"}]}]}`)

	first, err := render.EncodeFragmentRaw(payload)
	require.NoError(t, err, "first EncodeFragmentRaw")

	second, err := render.EncodeFragmentRaw(payload)
	require.NoError(t, err, "second EncodeFragmentRaw")

	assert.Equal(t, first, second, "EncodeFragmentRaw must be deterministic:\n  first:  %s\n  second: %s", first, second)
}

// graph build exited 0 when the root's reindex was refused by the run it was nested in,
// and quiet hid the refusal, so the target that ran it read a stale index. A missing
// indexer must still not fail the build.
func TestGraphBuildFailsOnlyOnALockRefusal(t *testing.T) {
	refusal := fmt.Errorf("magus: %w", types.DiagnosticErrorf(types.ProjectLockHeldByAncestor,
		"project . is locked by the magus run this one is nested inside"))
	missing := errors.New("docs: exit status 127; install scip-typescript")

	require.NoError(t, maintenance.ReindexRefusals(missing), "a missing indexer stays a warning")

	got := maintenance.ReindexRefusals(errors.Join(missing, refusal))
	require.ErrorIs(t, got, types.ProjectLockHeldByAncestor)
	assert.NotContains(t, got.Error(), "scip-typescript", "only the refusal fails the build")

	require.ErrorIs(t, maintenance.ReindexRefusals(refusal), types.ProjectLockHeldByAncestor, "an unjoined refusal counts too")

	full := types.DiagnosticErrorf(types.MachineBudgetExhausted, "not starting scip: this machine's build budget is full")
	got = maintenance.ReindexRefusals(errors.Join(missing, full))
	require.ErrorIs(t, got, types.MachineBudgetExhausted, "a full budget fails the build")
	assert.NotContains(t, got.Error(), "scip-typescript", "only the refusal fails the build")
}

type GraphReindexPressureSuite struct {
	suite.Suite
}

func TestGraphReindexPressureSuite(t *testing.T) { suite.Run(t, new(GraphReindexPressureSuite)) }

func (s *GraphReindexPressureSuite) TestARefusedReindexExitsTempFail() {
	short := types.ExitError{Code: exit.TempFail, Err: types.DiagnosticErrorf(types.MemoryPressureHigh, "not starting scip: this machine is short of memory")}
	missing := errors.New("docs: exit status 127; install scip-typescript")
	got := maintenance.ReindexRefusals(errors.Join(missing, short))
	s.Require().ErrorIs(got, types.MemoryPressureHigh)
	s.NotContains(got.Error(), "scip-typescript", "only the refusal fails the build")
	s.Equal(exit.TempFail, exit.Code(got))
}

// GraphExportSCIPSuite pins `magus graph export -o scip`: the symbol index as SCIP, for
// tools that read it, written only where a binary stream belongs.
type GraphExportSCIPSuite struct {
	suite.Suite
}

func TestGraphExportSCIPSuite(t *testing.T) { suite.Run(t, new(GraphExportSCIPSuite)) }

func (s *GraphExportSCIPSuite) TestRefusesASelection() {
	err := graphExportSCIP(s.T().Context(), s.T().TempDir(), &bytes.Buffer{}, neverTerminalProbe{}, true)
	s.ErrorContains(err, "drop --select")
}

func (s *GraphExportSCIPSuite) TestRefusesATerminal() {
	f, err := os.Create(filepath.Join(s.T().TempDir(), "tty"))
	s.Require().NoError(err)
	defer f.Close()
	err = graphExportSCIP(s.T().Context(), s.T().TempDir(), f, fixedSizeTerminal{w: 80, h: 24}, false)
	s.ErrorContains(err, "redirect it to a file")
}

func (s *GraphExportSCIPSuite) TestWritesAnIndexRootedAtTheWorkspace() {
	w := testkit.NewWorkspace(s.T())
	w.Magusfile("import \"magus\";\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\n")
	var out bytes.Buffer
	s.Require().NoError(graphExportSCIP(s.T().Context(), w.Root(), &out, neverTerminalProbe{}, false))

	var idx scip.Index
	s.Require().NoError(proto.Unmarshal(out.Bytes(), &idx))
	s.Equal("magus", idx.Metadata.ToolInfo.Name)
	s.Contains(idx.Metadata.ProjectRoot, filepath.ToSlash(w.Root()))
	s.Empty(idx.Documents, "a workspace no spell indexes exports no documents")
}

// tableCache points the XDG cache at a fresh directory and returns the path the
// table would occupy in it.
func tableCache(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	resetStartupSingletons()
	t.Cleanup(resetStartupSingletons)
	path, err := embedtable.Options{}.Path()
	require.NoError(t, err)
	return path
}

func TestGraphTablePullRefusesALayerOtherThanThePin(t *testing.T) {
	path := tableCache(t)
	reg := newSpellRegistry(t)
	pin, err := embed.Pin()
	require.NoError(t, err)
	repo := oci.Reference{Registry: reg.host(), Repository: "egladman/magus-embed"}
	wrong := oci.Artifact{ArtifactType: "application/vnd.magus.embed-table.v1", Blobs: []oci.Blob{
		{Title: filepath.Base(path), MediaType: "application/vnd.magus.embed-table.layer.v1", Data: []byte("other bytes")},
	}}
	_, err = (&oci.Client{HTTP: reg.srv.Client()}).PushArtifact(t.Context(), repo, wrong, pin)
	require.NoError(t, err)

	err = graphPull(t.Context(), t.TempDir(), []string{"-table", "--ref", reg.host() + "/egladman/magus-embed"})
	require.ErrorContains(t, err, "the pin is sha256:"+pin)
	_, statErr := os.Stat(path)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "a refused layer leaves the cache as it was")
}

func TestGraphTablePullRefusesOut(t *testing.T) {
	tableCache(t)
	err := graphPull(t.Context(), t.TempDir(), []string{"-table", "--out", filepath.Join(t.TempDir(), "x")})
	require.ErrorContains(t, err, "--out names nothing")
}

func TestGraphTablePushRefusesATableOffThePin(t *testing.T) {
	path := tableCache(t)
	reg := newSpellRegistry(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("a table from another conversion"), 0o644))

	var err error
	withStdin(t, "token", func() {
		err = graphPush(t.Context(), t.TempDir(), []string{"-table", "--ref", reg.host() + "/egladman/magus-embed", "--username", "u"})
	})
	require.ErrorContains(t, err, "never published under another tag")
	assert.Zero(t, reg.uploads)
}

func TestGraphTablePushWithoutATableNamesTheCommand(t *testing.T) {
	tableCache(t)
	reg := newSpellRegistry(t)
	var err error
	withStdin(t, "token", func() {
		err = graphPush(t.Context(), t.TempDir(), []string{"-table", "--ref", reg.host() + "/egladman/magus-embed", "--username", "u"})
	})
	require.ErrorContains(t, err, "no embedding table in the cache")
	assert.Contains(t, err.Error(), "magus run table libs/embed/train")
	assert.Zero(t, reg.uploads)
}
