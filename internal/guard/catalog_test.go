package guard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryRuleIsCatalogued is what makes the catalog worth reading: a rule that fires
// but is not listed turns `magus describe rules` into a partial answer, which is worse
// than no answer because a reader trusts it.
//
// It reads the denyRuleName constants out of the SOURCE rather than comparing against a
// second hand-written list, which would only move the drift one file over. A new rule
// lands as a new constant; this fails until its row exists.
func TestEveryRuleIsCatalogued(t *testing.T) {
	t.Parallel()

	declared := denyRuleNamesFromSource(t)
	require.NotEmpty(t, declared, "parsed no denyRuleName constants; the block moved and this gate stopped looking")

	catalogued := map[string]RuleDoc{}
	for _, r := range Rules() {
		catalogued[r.Name] = r
	}

	for _, name := range declared {
		doc, ok := catalogued[name]
		assert.Truef(t, ok, "deny rule %q has no catalog row: add one to denyRuleDocs", name)
		if ok {
			assert.Equal(t, "deny", doc.Decision, "%q is a deny rule", name)
			assert.NotEmpty(t, doc.Catches, "%q must say what it fires on", name)
		}
	}
	for _, name := range advisoryNames() {
		doc, ok := catalogued[name]
		assert.Truef(t, ok, "advisory %q has no catalog row: add one to advisoryDocs", name)
		if ok {
			assert.Equal(t, "advise", doc.Decision, "%q is an advisory", name)
			assert.NotEmpty(t, doc.Catches, "%q must say what it fires on", name)
		}
	}

	// And the other direction: a row for a rule that no longer exists documents
	// something a reader can never see fire.
	known := map[string]bool{}
	for _, name := range declared {
		known[name] = true
	}
	for _, name := range advisoryNames() {
		known[name] = true
	}
	for _, r := range Rules() {
		assert.Truef(t, known[r.Name], "catalog row %q names no declared rule or advisory", r.Name)
	}
}

// TestCatalogCatchesReadAsOneLine keeps the list scannable. A Catches that grew into the
// verdict's remediation prose would make thirty-eight rows unreadable, which is the
// failure this catalog exists to fix rather than reproduce.
func TestCatalogCatchesReadAsOneLine(t *testing.T) {
	t.Parallel()
	for _, r := range Rules() {
		assert.NotContainsf(t, r.Catches, "\n", "%q: Catches is one line", r.Name)
		assert.LessOrEqualf(t, len(r.Catches), 100, "%q: Catches is %d bytes, which is a paragraph", r.Name, len(r.Catches))
		assert.Falsef(t, strings.HasSuffix(r.Catches, "."), "%q: Catches is a phrase, not a sentence", r.Name)
	}
}

// TestCatalogRowsDescribeTheirOwnRule pins that no two rows share Catches or Why text: a row
// pasted from its neighbor documents the wrong rule under the right name.
func TestCatalogRowsDescribeTheirOwnRule(t *testing.T) {
	t.Parallel()
	catches, why := map[string]string{}, map[string]string{}
	for _, r := range Rules() {
		if prior, ok := catches[r.Catches]; ok {
			t.Errorf("%q and %q share Catches %q", prior, r.Name, r.Catches)
		}
		catches[r.Catches] = r.Name
		if r.Why == "" {
			continue
		}
		if prior, ok := why[r.Why]; ok {
			t.Errorf("%q and %q share Why text", prior, r.Name)
		}
		why[r.Why] = r.Name
	}
}

// TestRuleLookupIsExact pins that a near-miss reports absent. Resolving one would hand a
// reader a different rule's terms under the name they asked about.
func TestRuleLookupIsExact(t *testing.T) {
	t.Parallel()

	got, ok := Rule("stage-all")
	require.True(t, ok)
	assert.Equal(t, "deny", got.Decision)

	// Surrounding whitespace is a shell artifact, not a different name.
	_, ok = Rule("  stage-all\n")
	assert.True(t, ok)

	for _, miss := range []string{"stage", "stage-all-files", "STAGE-ALL", ""} {
		_, ok := Rule(miss)
		assert.Falsef(t, ok, "%q must not resolve", miss)
	}
}

// denyRuleNamesFromSource reads the denyRuleName constant values out of shell.go and
// lease.go, where the lease-scoped rules declare theirs. advisory.go is left out: its
// denyRuleName values are advisories, which advisoryNames covers.
func denyRuleNamesFromSource(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, src := range []string{"shell.go", "lease.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), src, nil, 0)
		require.NoError(t, err, "parse %s", src)
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			ident, ok := spec.Type.(*ast.Ident)
			if !ok || ident.Name != "denyRuleName" {
				return true
			}
			for _, v := range spec.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				names = append(names, value)
			}
			return true
		})
	}
	return names
}
