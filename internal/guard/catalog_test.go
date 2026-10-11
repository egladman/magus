package guard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryRuleIsCatalogued is what makes the catalog worth reading: a rule that fires
// but is not listed turns `magus describe rules` into a partial answer, which is worse
// than no answer because a reader trusts it.
//
// It reads the denyRuleName constants out of the whole package SOURCE rather than comparing
// against a second hand-written list, which would only move the drift one file over. A new
// rule lands as a new constant in any file; this fails until its row exists, and until
// builtin.Defaults resolves it, since a workspace naming a rule Defaults lacks fails to load.
func TestEveryRuleIsCatalogued(t *testing.T) {
	t.Parallel()

	src := ruleSourceOf(t)
	require.NotEmpty(t, src.denies, "parsed no denyRuleName constants; this gate stopped looking")

	catalogued := map[string]types.RuleDoc{}
	for _, r := range Rules() {
		catalogued[r.Name] = r
	}
	for name, constant := range src.denies {
		assertCatalogued(t, catalogued, constant, name)
	}
	// And the other direction: a row for a rule that no longer exists documents
	// something a reader can never see fire.
	for _, r := range Rules() {
		_, deny := src.denies[r.Name]
		_, kind := src.kinds[r.Name]
		assert.Truef(t, deny || kind, "catalog row %q names no declared rule or advisory", r.Name)
	}
}

// TestEveryRuleSiteNamesACataloguedRule closes the roads around the constants: a verdict
// filed under a name spelled at the site, under a constant of another type, or under an
// advisory kind the catalog lacks. A site is a Verdict's Rule, a ShellVerdict's Kind, a
// field of type denyRuleName, a parameter of that type or of hint.MarkerKind, and a
// conversion to one. Each must name a
// catalogued rule, a workspace rule or nothing; a value computed at run time arrives
// through one of these sites from a constant they cover.
func TestEveryRuleSiteNamesACataloguedRule(t *testing.T) {
	t.Parallel()

	src := ruleSourceOf(t)
	require.NotEmpty(t, src.sites, "found no site setting a rule; this gate stopped looking")
	catalogued := map[string]types.RuleDoc{}
	for _, r := range Rules() {
		catalogued[r.Name] = r
	}
	for _, s := range src.sites {
		value, typed := src.values[s.constant]
		switch {
		case s.literal != nil && *s.literal != "":
			t.Errorf("%s files a verdict under the literal %q: declare it as a denyRuleName constant", s.at, *s.literal)
		case s.constant == "" || src.workspace[s.constant]:
		case !typed:
			t.Errorf("%s files a verdict under %s, a constant of neither denyRuleName nor hint.MarkerKind", s.at, s.constant)
		default:
			assertCatalogued(t, catalogued, s.at+": "+s.constant, value)
		}
	}
}

// assertCatalogued asserts that name, which by declares, has a catalog row saying what it
// fires on and a builtin.Defaults entry.
func assertCatalogued(t *testing.T, catalogued map[string]types.RuleDoc, by, name string) {
	t.Helper()
	doc, ok := catalogued[name]
	if assert.Truef(t, ok, "%s names rule %q, which has no catalog row: add one to denyRuleDocs or advisoryDocs", by, name) {
		assert.NotEmpty(t, doc.Catches, "%q must say what it fires on", name)
	}
	_, ok = builtin.Defaults()[name]
	assert.Truef(t, ok, "%s names rule %q, which builtin.Defaults does not resolve", by, name)
}

// TestCatalogMatchesBuiltinDefaults pins the catalog to the table the rules resolve
// their decision from: a rule listed in one and not the other is documented with the
// wrong decision, or resolves to none.
func TestCatalogMatchesBuiltinDefaults(t *testing.T) {
	t.Parallel()

	defaults := builtin.Defaults()
	rows := map[string]string{}
	for _, r := range Rules() {
		rows[r.Name] = r.Decision
	}
	for name, want := range defaults {
		got, ok := rows[name]
		if !assert.Truef(t, ok, "builtin.Defaults names %q and the catalog has no row for it", name) {
			continue
		}
		assert.Equalf(t, string(want), got, "%q: the catalog shows a decision other than its default", name)
	}
	for name := range rows {
		_, ok := defaults[name]
		assert.Truef(t, ok, "catalog row %q is missing from builtin.Defaults", name)
	}
}

// measurementFigure matches the shapes a measured statistic takes in prose: a percentage,
// or a count grouped by thousands.
var measurementFigure = regexp.MustCompile(`\d+(\.\d+)?%|\b\d{1,3}(,\d{3})+\b`)

// TestCatalogCarriesNoMeasurements keeps numbers magus measured about its own use out of
// text the binary ships: a figure goes stale the day the rules change, and a reason does
// not. The measurements live beside the rule they justify, in the policy that sets it.
func TestCatalogCarriesNoMeasurements(t *testing.T) {
	t.Parallel()
	for _, r := range Rules() {
		for field, text := range map[string]string{"Why": r.Why, "Catches": r.Catches} {
			assert.NotContainsf(t, strings.ToLower(text), "measured", "%q: %s reports a measurement", r.Name, field)
			assert.Emptyf(t, measurementFigure.FindString(text), "%q: %s carries a figure", r.Name, field)
			assert.Equalf(t, strings.TrimSpace(text), text, "%q: %s ends in whitespace, which the rule page renders", r.Name, field)
		}
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
	assert.Equal(t, string(builtin.Defaults()["stage-all"]), got.Decision)

	// Surrounding whitespace is a shell artifact, not a different name.
	_, ok = Rule("  stage-all\n")
	assert.True(t, ok)

	for _, miss := range []string{"stage", "stage-all-files", "STAGE-ALL", ""} {
		_, ok := Rule(miss)
		assert.Falsef(t, ok, "%q must not resolve", miss)
	}
}

// ruleSource is what the package's non-test sources say about rule names.
type ruleSource struct {
	// denies and kinds map each name a denyRuleName or hint.MarkerKind constant declares
	// to that constant.
	denies, kinds map[string]string
	// values maps each of those constants to the name it declares.
	values map[string]string
	// workspace holds the constants naming workspace rules, built on workspaceShellPrefix,
	// which the catalog never lists.
	workspace map[string]bool
	// consts is every package-level constant.
	consts map[string]bool
	sites  []ruleSite
}

// ruleSite is one place a verdict is filed under a rule. literal is set for a string
// spelled at the site, constant for a package constant; neither for a value computed at
// run time.
type ruleSite struct {
	at       string
	literal  *string
	constant string
}

// isDenyRuleName reports whether expr spells the type denyRuleName.
func isDenyRuleName(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "denyRuleName"
}

// isMarkerKind reports whether expr spells the type hint.MarkerKind.
func isMarkerKind(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "hint" && sel.Sel.Name == "MarkerKind"
}

// ruleSourceOf parses every non-test file of the package.
func ruleSourceOf(t *testing.T) ruleSource {
	t.Helper()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err, "parse %s", name)
		parsed = append(parsed, f)
	}
	src := ruleSource{
		denies: map[string]string{}, kinds: map[string]string{}, values: map[string]string{},
		workspace: map[string]bool{}, consts: map[string]bool{},
	}
	// Where a rule is set: Verdict.Rule and ShellVerdict.Kind by name, every struct field
	// of type denyRuleName, and every package function's denyRuleName parameters.
	fields := map[string]map[string]bool{"Verdict": {"Rule": true}, "ShellVerdict": {"Kind": true}}
	params := map[string][]int{}
	for _, f := range parsed {
		for _, decl := range f.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.ValueSpec:
						if decl.Tok == token.CONST {
							src.addConst(t, spec)
						}
					case *ast.TypeSpec:
						st, ok := spec.Type.(*ast.StructType)
						if !ok {
							continue
						}
						for _, field := range st.Fields.List {
							for _, name := range field.Names {
								if isDenyRuleName(field.Type) {
									if fields[spec.Name.Name] == nil {
										fields[spec.Name.Name] = map[string]bool{}
									}
									fields[spec.Name.Name][name.Name] = true
								}
							}
						}
					}
				}
			case *ast.FuncDecl:
				i := 0
				for _, field := range decl.Type.Params.List {
					for range max(1, len(field.Names)) {
						if isDenyRuleName(field.Type) || isMarkerKind(field.Type) {
							params[decl.Name.Name] = append(params[decl.Name.Name], i)
						}
						i++
					}
				}
			}
		}
	}
	// An assignment names its field and not the type holding it, so only names no foreign
	// type could hold count: the two wire fields, and unexported rule fields. denyRule's
	// exported Name is met as the composite literal it is always built with.
	assigned := map[string]bool{"Rule": true, "Kind": true}
	for _, byName := range fields {
		for name := range byName {
			if !ast.IsExported(name) {
				assigned[name] = true
			}
		}
	}
	for _, f := range parsed {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CompositeLit:
				typ, ok := n.Type.(*ast.Ident)
				if !ok {
					return true
				}
				for _, elt := range n.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := kv.Key.(*ast.Ident); ok && fields[typ.Name][key.Name] {
						src.sites = append(src.sites, siteOf(fset, kv.Value))
					}
				}
			case *ast.AssignStmt:
				if len(n.Lhs) != len(n.Rhs) {
					return true
				}
				for i, lhs := range n.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && assigned[sel.Sel.Name] {
						src.sites = append(src.sites, siteOf(fset, n.Rhs[i]))
					}
				}
			case *ast.CallExpr:
				if len(n.Args) == 1 && isDenyRuleName(n.Fun) {
					src.sites = append(src.sites, siteOf(fset, n))
					return true
				}
				name := ""
				switch fun := n.Fun.(type) {
				case *ast.Ident:
					name = fun.Name
				case *ast.SelectorExpr:
					name = fun.Sel.Name
				}
				for _, i := range params[name] {
					if i < len(n.Args) {
						src.sites = append(src.sites, siteOf(fset, n.Args[i]))
					}
				}
			}
			return true
		})
	}
	for i, s := range src.sites {
		if s.constant != "" && !src.consts[s.constant] {
			src.sites[i].constant = "" // a variable or parameter, computed at run time
		}
	}
	return src
}

// addConst records one constant spec.
func (src ruleSource) addConst(t *testing.T, spec *ast.ValueSpec) {
	t.Helper()
	deny, kind := isDenyRuleName(spec.Type), isMarkerKind(spec.Type)
	for i, ident := range spec.Names {
		src.consts[ident.Name] = true
		if i >= len(spec.Values) {
			continue
		}
		if bin, ok := spec.Values[i].(*ast.BinaryExpr); ok {
			if x, ok := bin.X.(*ast.Ident); ok && x.Name == "workspaceShellPrefix" {
				src.workspace[ident.Name] = true
			}
		}
		if !deny && !kind {
			continue
		}
		lit, ok := spec.Values[i].(*ast.BasicLit)
		require.Truef(t, ok && lit.Kind == token.STRING, "%s is a rule name spelled other than as a string literal", ident.Name)
		value, err := strconv.Unquote(lit.Value)
		require.NoError(t, err)
		src.values[ident.Name] = value
		if deny {
			src.denies[value] = ident.Name
		} else {
			src.kinds[value] = ident.Name
		}
	}
}

// siteOf classifies the value a rule is set to, seen through a conversion to string,
// denyRuleName or hint.MarkerKind.
func siteOf(fset *token.FileSet, value ast.Expr) ruleSite {
	s := ruleSite{at: fset.Position(value.Pos()).String()}
	if call, ok := value.(*ast.CallExpr); ok && len(call.Args) == 1 {
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "string" || isDenyRuleName(call.Fun) || isMarkerKind(call.Fun) {
			value = call.Args[0]
		}
	}
	switch v := value.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			text, err := strconv.Unquote(v.Value)
			if err == nil {
				s.literal = &text
			}
		}
	case *ast.Ident:
		s.constant = v.Name
	}
	return s
}
