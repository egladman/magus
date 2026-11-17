package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/libs/oci"
	"github.com/egladman/magus/libs/testkit"
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
	assert.Contains(t, err.Error(), "magus run table libs/train")
	assert.Zero(t, reg.uploads)
}

// The printed link must never carry the bearer token. liveExplorerLink used to load it
// and embed it, which put a live credential in stdout, and so in scrollback, in any
// captured run log, and in the context of whatever agent ran `magus explain`. This
// asserts the omission at the seam that actually prints, not just at buildGraphLink,
// because the regression would be a one-line reintroduction of the auth.Load() call.
func TestLiveExplorerLinkCarriesNoToken(t *testing.T) {
	got := liveExplorerLink(console.GraphLinkOpts{View: "blast", Node: "spell:go"})
	if got == "" {
		t.Skip("no server address configured here, so there is no link to assert on")
	}
	require.NotContains(t, got, "token=",
		"the deep-link must stay unauthenticated; the token is composed in by console.AuthHint")
}

// TestConsoleLinksCarryNoToken extends TestLiveExplorerLinkCarriesNoToken to the other two
// console links, which kept embedding the token long after the Graph Explorer stopped.
//
// They relied on a terminal check instead: the token only reached an interactive user. That
// is a weaker property than it sounds: an interactive terminal is still scrollback, a
// termcast, and a screen share, and this repository has already had to rotate tokens that
// escaped through exactly those. The gate is gone now because there is no secret left to
// gate, and this is the assertion that keeps a one-line auth.Load() from bringing it back.
func TestConsoleLinksCarryNoToken(t *testing.T) {
	for name, got := range map[string]string{
		"dashboard": consoleWatchURL(),
		"diff":      consoleDiffURL(),
	} {
		if got == "" {
			continue // console disabled in this environment
		}
		require.NotContains(t, got, "token=",
			"the %s link must stay unauthenticated; the token is composed in by console.AuthHint", name)
	}
}
