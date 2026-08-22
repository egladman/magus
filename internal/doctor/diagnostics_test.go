package doctor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeCodePage plants a docs page for an MGS1### code under a fake root. The path is
// spelled out rather than taken from codeDocPath, so the fixture pins the site-to-tree
// mapping independently of the code under test.
func writeCodePage(t *testing.T, root string, code types.DiagnosticCode, body string) {
	t.Helper()
	dir := filepath.Join(root, "docs", "reference", "codes", "magusfile")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, string(code)+".md"), []byte(body), 0o644))
}

func TestCheckDiagnosticDocs(t *testing.T) {
	const code = types.NoCITarget // MGS1001, so the magusfile area

	cases := []struct {
		name       string
		body       string
		wantStatus types.DoctorCheckStatus
		wantDetail string
	}{
		{
			"a page naming the next step",
			"# MGS1001\n\n## Why\n\nBecause.\n\n## Resolution\n\nDeclare a ci target.\n",
			types.DoctorOK, "",
		},
		{
			"a page that only explains",
			"# MGS1001\n\n## Why\n\nBecause.\n\n## What this is NOT\n\nNot that.\n",
			types.DoctorAdvice, "declares no remediation section",
		},
		{
			"a remediation section with nothing under it",
			"# MGS1001\n\n## Resolution\n\n## See also\n\n- something\n",
			types.DoctorAdvice, "declares no remediation section",
		},
		{
			"verifying the fix is not the fix",
			"# MGS1001\n\n## Confirming the fix\n\nRun magus ls.\n",
			types.DoctorAdvice, "declares no remediation section",
		},
		{
			"a heading inside a fenced block is sample output",
			"# MGS1001\n\n```text\n## Resolution\n```\n",
			types.DoctorAdvice, "declares no remediation section",
		},
		// The spellings already in the tree: the pages say Resolution, Fix, The fix,
		// Fixing it and Resolve it, and all five are the same section.
		{"spelled Fix", "# MGS1001\n\n## Fix\n\nDeclare it.\n", types.DoctorOK, ""},
		{"spelled The fix", "# MGS1001\n\n## The fix\n\nDeclare it.\n", types.DoctorOK, ""},
		{"spelled Fixing it", "# MGS1001\n\n## Fixing it\n\nDeclare it.\n", types.DoctorOK, ""},
		{"spelled Resolve it", "# MGS1001\n\n## Resolve it\n\nDeclare it.\n", types.DoctorOK, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeCodePage(t, root, code, c.body)

			got := checkDiagnosticDocs(root, []types.DiagnosticCode{code}, types.CodeURL)
			require.Equal(t, c.wantStatus, got.Status, got.Message)
			if c.wantDetail == "" {
				assert.Empty(t, got.Details)
				return
			}
			require.Len(t, got.Details, 1)
			assert.Contains(t, got.Details[0], string(code), "a finding must name the code it is about")
			assert.Contains(t, got.Details[0], c.wantDetail)
			assert.Contains(t, got.Details[0], "## Resolution", "a finding without a remedy is a complaint")
		})
	}
}

// TestCheckDiagnosticDocsMissingPage: the code prints a see: URL either way, so a page
// that was never written is a link into a 404 and looks exactly like one that works.
func TestCheckDiagnosticDocsMissingPage(t *testing.T) {
	root := t.TempDir()
	writeCodePage(t, root, types.NoCITarget, "# MGS1001\n\n## Resolution\n\nDeclare it.\n")

	got := checkDiagnosticDocs(root, []types.DiagnosticCode{types.NoCITarget, types.SpellShadowed}, types.CodeURL)
	require.Equal(t, types.DoctorFail, got.Status)
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "MGS1002")
	assert.Contains(t, got.Details[0], "docs/reference/codes/magusfile/MGS1002.md",
		"the finding must name the file to write, not just that one is missing")
}

// TestCheckDiagnosticDocsUnroutableCode covers a code the domain routes nowhere. It is
// unreachable through types.CodeURL, which routes an unrecognized prefix to a default
// area, so it takes a domain of the test's own.
func TestCheckDiagnosticDocsUnroutableCode(t *testing.T) {
	root := t.TempDir()
	writeCodePage(t, root, types.NoCITarget, "# MGS1001\n\n## Resolution\n\nDeclare it.\n")

	stray := types.DiagnosticCode("MGS1002")
	url := func(c types.DiagnosticCode) string {
		if c == stray {
			return ""
		}
		return types.CodeURL(c)
	}

	got := checkDiagnosticDocs(root, []types.DiagnosticCode{types.NoCITarget, stray}, url)
	require.Equal(t, types.DoctorFail, got.Status)
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "no see: target")
	assert.Contains(t, got.Details[0], "types/diagnostic.go")
}

// TestCheckDiagnosticDocsSilentElsewhere: doctor runs in every magus workspace, and the
// MGS pages exist only in this one. A check that failed on their absence would fail for
// every user of magus.
func TestCheckDiagnosticDocsSilentElsewhere(t *testing.T) {
	got := (&runner{root: t.TempDir()}).checkDiagnosticDocs()
	require.Equal(t, types.DoctorOK, got.Status)
	require.Contains(t, got.Message, "skipped")
}

// codesWithoutRemediation lists known pages that explain a failure without naming a next
// step. Empty today; a code may sit here only while its section is being written. Shrink
// this set, never grow it.
var codesWithoutRemediation = []types.DiagnosticCode{}

// TestDiagnosticCatalogPassesItsOwnCheck runs the check against the real catalog and the
// real pages, which is the only run that grades the doctrine claim it exists to enforce.
// A code shipped without a page fails here; a code shipped without a next step shows up
// as an unexpected advice detail.
func TestDiagnosticCatalogPassesItsOwnCheck(t *testing.T) {
	got := (&runner{root: filepath.Join("..", "..")}).checkDiagnosticDocs()
	require.NotEqual(t, types.DoctorFail, got.Status, got.Message)

	for _, d := range got.Details {
		code := types.DiagnosticCode(strings.SplitN(d, ":", 2)[0])
		assert.Truef(t, slices.Contains(codesWithoutRemediation, code),
			"%s has no remediation section; write one rather than adding it to codesWithoutRemediation:\n%s", code, d)
	}
}
