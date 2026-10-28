package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/suite"
	"google.golang.org/protobuf/proto"
)

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
