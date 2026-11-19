package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type GlueSuite struct {
	suite.Suite
	root  string
	asset HarnessRuntimeAsset
}

func TestGlue(t *testing.T) { suite.Run(t, new(GlueSuite)) }

const gluePath = ".magus/harness/hooks/magus-command.buzz"

func (s *GlueSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.asset = HarnessRuntimeAsset{Path: gluePath, Content: stampGlue([]byte("import \"std\";\n")), Glue: true}
}

func (s *GlueSuite) write(relative string, body []byte) {
	path := filepath.Join(s.root, relative)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, body, 0o644))
}

func (s *GlueSuite) TestStampCarriesTheVersionAndTheDigestOfTheBody() {
	rest, stamp := readGlueStamp(s.asset.Content)
	s.Equal("import \"std\";\n", string(rest))
	s.Equal(glueStamp{version: GuardTemplateVersion, digest: glueDigest(rest), ok: true}, stamp)
}

func (s *GlueSuite) TestTextAfterTheStampReadsAsNoStamp() {
	_, stamp := readGlueStamp(append(append([]byte{}, s.asset.Content...), "// mine\n"...))
	s.False(stamp.ok)
}

func (s *GlueSuite) TestTheBinarysOwnCopyIsCurrent() {
	s.write(gluePath, s.asset.Content)
	s.Empty(glueProblem(s.root, s.asset))
}

func (s *GlueSuite) TestAMissingCopyNamesTheRemedy() {
	s.Equal("hook glue "+gluePath+" is missing, so every hook that runs it fails closed; run `magus agent harness install`",
		glueProblem(s.root, s.asset))
}

func (s *GlueSuite) TestAnEditedCopyIsDrift() {
	s.write(gluePath, append(append([]byte{}, s.asset.Content...), "// mine\n"...))
	s.Equal("hook glue "+gluePath+" was edited after install, so it is not the glue this magus ships; run `magus agent harness install`",
		glueProblem(s.root, s.asset))
}

func (s *GlueSuite) TestAnOlderVersionNamesBoth() {
	body := []byte("old\n")
	s.write(gluePath, fmt.Appendf(append([]byte{}, body...), "%s glue-version: magus.guard-template/v1alpha0; glue-content: %s; do not edit, re-run to update\n", glueStampPrefix, glueDigest(body)))
	s.Equal(fmt.Sprintf("hook glue %s is glue version magus.guard-template/v1alpha0, and this magus ships version %s; run `magus agent harness install`", gluePath, GuardTemplateVersion),
		glueProblem(s.root, s.asset))
}

func (s *GlueSuite) TestAnotherBuildAtTheSameVersionNamesBothDigests() {
	other := stampGlue([]byte("other\n"))
	s.write(gluePath, other)
	_, have := readGlueStamp(other)
	_, want := readGlueStamp(s.asset.Content)
	s.Equal(fmt.Sprintf("hook glue %s was installed by another magus build (glue-content %s, and this magus ships %s); run `magus agent harness install`",
		gluePath, have.digest, want.digest), glueProblem(s.root, s.asset))
}

func (s *GlueSuite) TestALinkToTheWorkspacesOwnSourceIsCurrentAndInstallLeavesIt() {
	s.write("src/magus-command.buzz", []byte("edited source\n"))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, filepath.Dir(gluePath)), 0o755))
	s.Require().NoError(os.Symlink("../../../src/magus-command.buzz", filepath.Join(s.root, gluePath)))
	s.Empty(glueProblem(s.root, s.asset))

	s.write(".magus/harnesses/h/assets.jsonl", []byte(`{"path":"a","digest":"b"}`+"\n"+`{"path":"c","digest":"d"}`+"\n"))
	plan := types.HarnessInstallation{Files: map[string]types.HarnessFile{}}
	s.Require().NoError(planHarnessAssets(s.root, HarnessDescriptor{ID: "h", Assets: []HarnessRuntimeAsset{s.asset}}, &plan))
	s.Empty(plan.Files, "a linked copy plans neither its file nor an empty receipt")
	_, err := ApplyHarnessInstallation(context.Background(), s.root, plan, false)
	s.Require().NoError(err)

	s.Require().NoError(os.Remove(filepath.Join(s.root, "src/magus-command.buzz")))
	s.Contains(glueProblem(s.root, s.asset), "links to a file that does not exist")
}

func (s *GlueSuite) TestInstallOverwritesAnEditedCopyWithoutAReceipt() {
	s.write(gluePath, []byte("edited\n"))
	plan := types.HarnessInstallation{Files: map[string]types.HarnessFile{}}
	s.Require().NoError(planHarnessAssets(s.root, HarnessDescriptor{ID: "h", Assets: []HarnessRuntimeAsset{s.asset}}, &plan))
	s.Equal(s.asset.Content, plan.Files[gluePath].Bytes)
}

func (s *GlueSuite) TestTheLinkerReplacesAStampedCopy() {
	s.write(gluePath, s.asset.Content)
	s.write("src/magus-command.buzz", []byte("source\n"))
	files, err := InstallHarnessSourceLinks(context.Background(), s.root, map[string]string{gluePath: "src/magus-command.buzz"}, false)
	s.Require().NoError(err)
	s.Equal([]string{gluePath}, files)
	linked, err := glueLinked(s.root, gluePath)
	s.Require().NoError(err)
	s.True(linked)
}

func (s *GlueSuite) TestTheLinkerStillRefusesAFileMagusDidNotStamp() {
	s.write(gluePath, []byte("a person's file\n"))
	s.write("src/magus-command.buzz", []byte("source\n"))
	_, err := InstallHarnessSourceLinks(context.Background(), s.root, map[string]string{gluePath: "src/magus-command.buzz"}, false)
	s.ErrorContains(err, "refuse conflicting file")
}
