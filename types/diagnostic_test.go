package types

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/diagnostics"
)

// TestAllDiagnosticCodesEnumerated guards the hand-maintained allDiagnosticCodes
// slice against the const declarations: every declared MGS#### code must be enumerated,
// so tooling that lists codes (the knowledge graph builds a node per code) never
// silently drops one. Mirrors the source-scan idiom in diagnostic_doc_test.go.
func TestAllDiagnosticCodesEnumerated(t *testing.T) {
	// go test's cwd is this package dir, regardless of -trimpath.
	root, err := filepath.Abs("..")
	require.NoError(t, err)

	src, err := os.ReadFile(filepath.Join(root, "types", "diagnostic.go"))
	require.NoError(t, err)

	declared := regexp.MustCompile(`DiagnosticCode = "(MGS\d+)"`).FindAllStringSubmatch(string(src), -1)
	require.NotEmpty(t, declared)

	enumerated := map[DiagnosticCode]bool{}
	for _, c := range AllDiagnosticCodes() {
		assert.Falsef(t, enumerated[c], "duplicate code %s in AllDiagnosticCodes", c)
		enumerated[c] = true
	}

	for _, m := range declared {
		code := DiagnosticCode(m[1])
		assert.Truef(t, enumerated[code], "%s is declared but missing from allDiagnosticCodes", code)
	}
	assert.Len(t, AllDiagnosticCodes(), len(declared), "allDiagnosticCodes has entries without a const")
}

func TestManifestScriptDelegationRoutesToMagusfileDocs(t *testing.T) {
	assert.Equal(t, "https://eli.gladman.cc/magus/reference/codes/magusfile/MGS1049/", CodeURL(ManifestScriptDelegation))
}

func TestSpellContractViolatedRoutesToMagusfileDocs(t *testing.T) {
	assert.Equal(t, "https://eli.gladman.cc/magus/reference/codes/magusfile/MGS1051/", CodeURL(SpellContractViolated))
}

// SetupSuite registers the MGS catalog as magus's startup does, since origin and collisions
// derive from the registry.
func (s *typesSuite) SetupSuite() {
	s.Require().NoError(diagnostics.RegisterCatalog(DiagnosticOwnerMagus, AllDiagnosticCodes()))
}

func (s *typesSuite) TestCodeURLRoutesAWorkspaceScopeCodeToSandboxDocs() {
	s.Equal("https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3044/", CodeURL("MGS3044"))
}

func (s *typesSuite) TestCodeURLOfAnUnknownPrefixIsEmpty() {
	s.Empty(CodeURL("ACME1001"))
}

func (s *typesSuite) TestDiagnosticDocsResolveTheWorkspacePrefix() {
	d := DiagnosticDocs{Prefix: "WS", Docs: "docs/codes/{code}.md"}
	s.Equal("docs/codes/WS1003.md", d.URL("WS1003"))
}

func (s *typesSuite) TestDiagnosticDocsKeepABuiltinCodeOnItsOwnPage() {
	d := DiagnosticDocs{Prefix: "WS", Docs: "docs/codes/{code}.md"}
	s.Equal(CodeURL(WorkspaceNeedsNewerMagus), d.URL(WorkspaceNeedsNewerMagus))
}

func (s *typesSuite) TestDiagnosticDocsOfAForeignPrefixIsEmpty() {
	d := DiagnosticDocs{Prefix: "WS", Docs: "docs/codes/{code}.md"}
	s.Empty(d.URL("ACME1001"))
}

func (s *typesSuite) TestOriginOfAnMGSCodeIsBuiltin() {
	s.Equal(DiagnosticOriginBuiltin, OriginOf(WorkspaceNeedsNewerMagus))
}

func (s *typesSuite) TestOriginOfAnUnreservedCodeIsWorkspace() {
	s.Equal(DiagnosticOriginWorkspace, OriginOf("WS1001"))
}

func (s *typesSuite) TestRegisterDiagnosticPrefixAcceptsWS() {
	s.NoError(RegisterDiagnosticPrefix("WS"))
}

func (s *typesSuite) TestRegisterDiagnosticPrefixRefusesMGS() {
	s.ErrorContains(RegisterDiagnosticPrefix("MGS"), "collides with MGS")
}

func (s *typesSuite) TestRegisterDiagnosticPrefixRefusesAPrefixOfMGS() {
	s.ErrorContains(RegisterDiagnosticPrefix("MG"), "collides with MGS")
}

func (s *typesSuite) TestRegisterDiagnosticPrefixRefusesOneMGSStarts() {
	s.ErrorContains(RegisterDiagnosticPrefix("MGSX"), "collides with MGS")
}

func (s *typesSuite) TestRegisterDiagnosticPrefixRefusesLowercase() {
	s.ErrorContains(RegisterDiagnosticPrefix("ws"), "capital letters")
}

func (s *typesSuite) TestWorkspaceDiagnosticCodeMintsUnderThePrefix() {
	c, err := WorkspaceDiagnosticCode("WS", "WS1003")
	s.Require().NoError(err)
	s.Equal(DiagnosticCode("WS1003"), c)
}

func (s *typesSuite) TestWorkspaceDiagnosticCodeRefusesWithNoPrefixDeclared() {
	_, err := WorkspaceDiagnosticCode("", "WS1003")
	s.ErrorContains(err, "diagnostics.prefix in magus.yaml")
}

func (s *typesSuite) TestWorkspaceDiagnosticCodeRefusesAnMGSCode() {
	_, err := WorkspaceDiagnosticCode("WS", "MGS1021")
	s.ErrorContains(err, "MGS catalog magus ships")
}

func (s *typesSuite) TestWorkspaceDiagnosticCodeRefusesAForeignPrefix() {
	_, err := WorkspaceDiagnosticCode("WS", "ACME1001")
	s.ErrorContains(err, "not under this workspace's prefix WS")
}

func (s *typesSuite) TestWorkspaceDiagnosticCodeRefusesAMalformedCode() {
	_, err := WorkspaceDiagnosticCode("WS", "WS12")
	s.ErrorContains(err, "four digits")
}
