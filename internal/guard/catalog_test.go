package guard

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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

	src := parseRuleSource(t)
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
// advisory kind the catalog lacks. A site is a GuardJudgment's Rule, a ShellVerdict's Kind, a
// field of type denyRuleName, a parameter of that type or of hint.MarkerKind, and a
// conversion to one. Each must name a
// catalogued rule, a workspace rule or nothing; a value computed at run time arrives
// through one of these sites from a constant they cover.
func TestEveryRuleSiteNamesACataloguedRule(t *testing.T) {
	t.Parallel()

	src := parseRuleSource(t)
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
			assertCatalogued(t, catalogued, fmt.Sprintf("%s: %s", s.at, s.constant), value)
		}
	}
}

// assertCatalogued asserts that name, which by declares, has a catalog row saying what it
// fires on and a builtin.Defaults entry.
func assertCatalogued(t *testing.T, catalogued map[string]types.RuleDoc, by, name string) {
	t.Helper()
	doc, ok := catalogued[name]
	if assert.Truef(t, ok, "%s names rule %q, which has no catalog row: declare a ruleDoc var and name it in ruleDocRegistry", by, name) {
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
		assert.Equalf(t, want.String(), got, "%q: the catalog shows a decision other than its default", name)
	}
	for name := range rows {
		_, ok := defaults[name]
		assert.Truef(t, ok, "catalog row %q is missing from builtin.Defaults", name)
	}
}

// TestRuleDocsAreNamedAfterTheirRule pins the one naming every row follows, so a job
// claiming `catalog.go#ruleDocLeaseGate` can derive the name from the rule's id:
// ruleDoc plus the id in PascalCase. It also counts the declarations, which fails a row
// the registry holds that is not a top-level var.
func TestRuleDocsAreNamedAfterTheirRule(t *testing.T) {
	t.Parallel()

	src := parseRuleSource(t)
	f, err := parser.ParseFile(token.NewFileSet(), "catalog.go", nil, 0)
	require.NoError(t, err)

	seen := 0
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			if typ, ok := lit.Type.(*ast.SelectorExpr); !ok || typ.Sel.Name != "RuleDoc" {
				continue
			}
			seen++
			var constant string
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok || kv.Key.(*ast.Ident).Name != "Name" {
					continue
				}
				conv, ok := kv.Value.(*ast.CallExpr)
				require.Truef(t, ok && len(conv.Args) == 1, "%s: Name is string(<rule constant>)", vs.Names[0].Name)
				constant = conv.Args[0].(*ast.Ident).Name
			}
			name, ok := src.values[constant]
			require.Truef(t, ok, "%s: %q is not a rule constant", vs.Names[0].Name, constant)
			assert.Equal(t, "ruleDoc"+pascalCase(name), vs.Names[0].Name, "the var for rule %q", name)
		}
	}
	assert.Len(t, ruleDocRegistry, seen, "every filed row is a top-level ruleDoc var")
}

// pascalCase joins the words of a kebab-case rule id, each capitalized.
func pascalCase(id string) string {
	var b strings.Builder
	for _, word := range strings.Split(id, "-") {
		b.WriteString(strings.ToUpper(word[:1]) + word[1:])
	}
	return b.String()
}

// TestEnrolledAdvisoriesAreMarkerKinds keeps the enrolled flag honest: only a
// hint.MarkerKind constant can hold an advisory to one firing per session.
func TestEnrolledAdvisoriesAreMarkerKinds(t *testing.T) {
	t.Parallel()

	src := parseRuleSource(t)
	enrolled := enrolledAdvisories()
	require.NotEmpty(t, enrolled)
	for _, name := range enrolled {
		_, kind := src.kinds[name]
		assert.Truef(t, kind, "%q is filed as enrolled but no hint.MarkerKind constant declares it", name)
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
	assert.Equal(t, builtin.Defaults()["stage-all"].String(), got.Decision)

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

// parseRuleSource parses every non-test file of the package.
func parseRuleSource(t *testing.T) ruleSource {
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
	// Where a rule is set: GuardJudgment.Rule and ShellVerdict.Kind by name, every struct field
	// of type denyRuleName, and every package function's denyRuleName parameters.
	fields := map[string]map[string]bool{"GuardJudgment": {"Rule": true}, "ShellVerdict": {"Kind": true}}
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
				typName := ""
				switch typ := n.Type.(type) {
				case *ast.Ident:
					typName = typ.Name
				case *ast.SelectorExpr:
					if pkg, ok := typ.X.(*ast.Ident); ok && pkg.Name == "types" && typ.Sel.Name == "GuardJudgment" {
						typName = typ.Sel.Name
					}
				}
				if typName == "" {
					return true
				}
				for _, elt := range n.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := kv.Key.(*ast.Ident); ok && fields[typName][key.Name] {
						src.sites = append(src.sites, classifySite(fset, kv.Value))
					}
				}
			case *ast.AssignStmt:
				if len(n.Lhs) != len(n.Rhs) {
					return true
				}
				for i, lhs := range n.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && assigned[sel.Sel.Name] {
						src.sites = append(src.sites, classifySite(fset, n.Rhs[i]))
					}
				}
			case *ast.CallExpr:
				if len(n.Args) == 1 && isDenyRuleName(n.Fun) {
					src.sites = append(src.sites, classifySite(fset, n))
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
						src.sites = append(src.sites, classifySite(fset, n.Args[i]))
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

// classifySite classifies the value a rule is set to, seen through a conversion to string,
// denyRuleName or hint.MarkerKind.
func classifySite(fset *token.FileSet, value ast.Expr) ruleSite {
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

// A remedy the same guard refuses is the retry loop its rule exists to end.
// Each rule serving one has a method here, named for the rule.

type remedyContractSuite struct{ suite.Suite }

func TestRemedyContract(t *testing.T) { suite.Run(t, new(remedyContractSuite)) }

// servedFleet is every role a next is served to and what judges them.
func servedFleet(t *testing.T) (context.Context, []servedRole, Dependencies) {
	t.Helper()
	ctx, roles := servedRoles(t)
	deps := withRepositoryPolicy(t, strict(loadingWorkspace(t)))
	deps.Spells = testDependencies().Spells
	deps.GraphIDs = diagnosticGraph
	// Lowered so a bound worker is served the raw-tool remedy.
	deps.Builtins[string(denyRuleWorkerCheckOnly)] = builtin.Setting{
		Decision: builtin.Advise,
	}
	return ctx, roles, deps
}

// withRepositoryPolicy layers the root magusfile's rules over deps, as the
// hook loads them.
func withRepositoryPolicy(t *testing.T, deps Dependencies) Dependencies {
	t.Helper()
	rules := workspacePolicy(t)
	deps.ShellRules, deps.ShellDialect = rules.ShellRules()
	deps.SpawnRule = rules.SpawnRule()
	deps.CommandRule = rules.CommandRule()
	deps.WriteRule = rules.WriteRule()
	deps.StopRule = rules.StopRule()
	for name, setting := range rules.Builtins() {
		deps.Builtins[name] = setting
	}
	return deps
}

// remedyCase returns a refused call and the remedy an unbound caller gets.
// want is "" when the remedy depends on what the fixture recorded.
type remedyCase func(t *testing.T, at location) (call Request, want string)

func refused(command, want string) remedyCase {
	return func(*testing.T, location) (Request, string) {
		return Request{Input: command}, want
	}
}

// contract judges each case as every role and grades what each is served.
func (s *remedyContractSuite) contract(rule denyRuleName, cases ...remedyCase) {
	s.Require().NotEmpty(cases)
	ctx, roles, deps := servedFleet(s.T())
	for i, c := range cases {
		for _, role := range roles {
			s.Run(fmt.Sprintf("%d/%s", i, role.name), func() {
				t := s.T()
				call, want := c(t, hookLocation(ctx, Dependencies{}))
				call = servedAs(call, role)
				call.DryRun = true
				v := Judge(ctx, deps, call)
				if role.call.Host == "" {
					require.Equal(t, string(rule), v.Rule, "%q:\n%s", call.Input, v.Reason)
					require.NotEmpty(t, v.NextSteps, "unbound, %q serves its remedy", call.Input)
					if want != "" {
						assert.Equal(t, want, v.NextSteps[0].Run)
					}
				}
				for _, n := range v.NextSteps {
					assert.Equal(t, hint.DenyRemedyPrefix+string(rule), n.ID)
					assertRunsOnTheTree(t, n.Run)
					gradeServed(t, ctx, deps, call, role.name, n.ID, n.Run)
				}
			})
		}
	}
}

// assertRunsOnTheTree fails t unless run parses as shell and each magus
// invocation in it names a command the tree declares.
func assertRunsOnTheTree(t *testing.T, run string) {
	t.Helper()
	assert.Empty(t, placeholderRe.FindString(run), "a remedy carries no placeholder: %q", run)
	cmds, ok := ParseCommands(run)
	require.True(t, ok && len(cmds) > 0, "the remedy does not parse: %q", run)
	for _, c := range cmds {
		if path.Base(filepath.ToSlash(c.Name)) != "magus" {
			continue
		}
		assert.Empty(t, treeMiss(c.Args), "in %q", run)
	}
}

// treeMiss names the magus argv word the tree has no command for. It is ""
// past a leaf or a command taking operands. Only the root's flags take a
// value before the subcommand.
func treeMiss(args []string) string {
	node := commands.Root
	for i := 0; i < len(args); i++ {
		word := args[i]
		if word == "--" {
			break
		}
		if strings.HasPrefix(word, "-") {
			flag := strings.TrimLeft(word, "-")
			if node.Name == commands.Root.Name && !strings.Contains(word, "=") && takesValue(node, flag) {
				i++
			}
			continue
		}
		if len(node.Children) == 0 || len(node.Operands) > 0 {
			return ""
		}
		child, ok := childNamed(node, word)
		if !ok {
			return fmt.Sprintf("magus has no command %q under %q", word, node.Name)
		}
		node = child
	}
	if len(node.Children) > 0 && len(node.Operands) == 0 {
		return fmt.Sprintf("%q stops at %q, which needs a subcommand", strings.Join(args, " "), node.Name)
	}
	return ""
}

func childNamed(node command.Command, name string) (command.Command, bool) {
	for _, c := range node.Children {
		if c.Name == name {
			return c, true
		}
	}
	return command.Command{}, false
}

func takesValue(node command.Command, name string) bool {
	for _, f := range node.Flags {
		if f.Name == name {
			return f.Kind != command.Bool
		}
	}
	return false
}

func (s *remedyContractSuite) TestBusyWait() {
	s.contract(denyRuleBusyWait, func(t *testing.T, at location) (Request, string) {
		recordRun(t, at.workspace, ".", "test", 5*time.Minute)
		return Request{Input: "until ./magus run test .; do sleep 30; done"}, ""
	})
}

func (s *remedyContractSuite) TestChainedRun() {
	s.contract(denyRuleChainedRun,
		refused("magus run lint . && magus run lint docs", "magus run lint . | magus run lint docs"),
		refused("magus run generate:rw . && magus run test .", "magus run generate:rw . | magus run test ."),
	)
}

func (s *remedyContractSuite) TestGrepReader() {
	s.contract(denyRuleGrepReader, func(t *testing.T, at location) (Request, string) {
		src := "package api\n\n// HandleRequest serves one request.\nfunc HandleRequest() {}\n"
		require.NoError(t, os.WriteFile(filepath.Join(at.workspace, "handler.go"), []byte(src), 0o644))
		t.Chdir(at.workspace)
		return Request{Input: "grep -n 'func HandleRequest' -A20 handler.go"}, "sed -n 3,4p handler.go"
	})
}

// TestLeaseClaim grades the claim line for the one caller it is served to: the
// worker whose write it refused.
func (s *remedyContractSuite) TestLeaseClaim() {
	t := s.T()
	ctx, root, row := leaseClaimFixture(t)
	deps := withRepositoryPolicy(t, strict(Dependencies{}))
	worker := Request{Host: "claude-code", Session: "5d1c", Lease: row.ID, Dir: root, DryRun: true}
	write := worker
	write.Input = hookJSON(t, map[string]any{
		"session_id": "5d1c", "hook_event_name": "PreToolUse", "tool_name": "Write", "cwd": root,
		"tool_input": map[string]any{"file_path": filepath.Join(root, "internal/commands/registry.go"), "content": "package x\n"},
	})
	v := Judge(ctx, deps, write)
	require.Equal(t, string(denyRuleLeaseClaim), v.Rule, v.Reason)
	require.Len(t, v.NextSteps, 1)
	assertRunsOnTheTree(t, v.NextSteps[0].Run)
	gradeServed(t, ctx, deps, worker, "lease holder", v.NextSteps[0].ID, v.NextSteps[0].Run)
}

func (s *remedyContractSuite) TestMagusTimeout() {
	s.contract(denyRuleMagusTimeout,
		refused("timeout 600 magus run lint .", "magus run --timeout 10m lint ."),
		refused("timeout 60 magus ls jobs", "magus ls jobs"),
	)
}

func (s *remedyContractSuite) TestOutputPipe() {
	s.contract(denyRuleOutputPipe,
		refused("magus run lint . | tail -20", "magus run lint . -s"),
		refused("magus ls jobs | wc -l", `magus ls jobs -o "template={{len .jobs}}"`),
	)
}

func (s *remedyContractSuite) TestOutputRedirect() {
	s.contract(denyRuleOutputRedirect,
		refused("magus ls jobs -o json > jobs.json", "magus ls jobs -o json --tee jobs.json"),
		refused("magus ls jobs > jobs.txt", "magus ls jobs -o json --tee jobs.txt"),
		refused("magus run lint . > /dev/null 2>&1", "magus run lint . --silent"),
	)
}

func (s *remedyContractSuite) TestProcessPoll() {
	s.contract(denyRuleProcessPoll, refused("pgrep -fl magus", "magus server status"))
}

func (s *remedyContractSuite) TestProcessSignal() {
	s.contract(denyRuleProcessSignal, refused("pkill -f 'magus job fork'", "magus server status"))
}

func (s *remedyContractSuite) TestRawTool() {
	s.contract(denyRuleRawTool, func(t *testing.T, _ location) (Request, string) {
		spellName := register.Spell(t, "guard-remedy-test",
			spells.WithTargets("check"),
			spells.WithCommandRenderer(func(target string, _ []string) (string, []string, bool, error) {
				return "remedy-tool", []string{"check"}, target == "check", nil
			}),
		)
		return Request{Input: "remedy-tool check"}, fmt.Sprintf("magus run %s::check", spellName)
	})
}

func (s *remedyContractSuite) TestSiblingCheckout() {
	sibling := func(verb string) remedyCase {
		return func(t *testing.T, _ location) (Request, string) {
			main, wt := twoCheckouts(t)
			require.NoError(t, os.WriteFile(filepath.Join(wt, "magus"), []byte("#!/bin/sh\n"), 0o755))
			t.Chdir(main)
			// Relative: the fixture's temp dir is no throwaway copy.
			return Request{Input: fmt.Sprintf("cd ../%s && ./magus %s harness/worker", filepath.Base(wt), verb)},
				fmt.Sprintf("%s --root %s %s harness/worker", filepath.Join(wt, "magus"), wt, verb)
		}
	}
	s.contract(denyRuleSiblingCheckout, sibling("describe job"), sibling("job exec"))
}

func (s *remedyContractSuite) TestSleepPoll() {
	s.contract(denyRuleSleepPoll, func(t *testing.T, at location) (Request, string) {
		recordRun(t, at.workspace, ".", "test", 80*time.Second)
		return Request{Input: "sleep 30; ./magus server status; ./magus run test ."}, ""
	})
}

func (s *remedyContractSuite) TestStageAll() {
	s.contract(denyRuleStageAll,
		refused("git add -A", "magus vcs ls-files --include-untracked --include-generated -o name | git add --pathspec-from-file=-"),
		refused("git add -A -- cmd/magus/main.go", "magus vcs ls-files --include-untracked --include-generated cmd/magus/main.go -o name | git add --pathspec-from-file=-"),
	)
}

func (s *remedyContractSuite) TestSymbolSearch() {
	s.contract(denyRuleSymbolSearch, refused("grep -rn MGS2011 docs/", "magus explain diagnostic:MGS2011"))
}

func (s *remedyContractSuite) TestWorktreeRemove() {
	s.contract(denyRuleWorktreeRemove,
		func(t *testing.T, at location) (Request, string) {
			r := newWorktreeRepository(t)
			wt, row := r.workerWorktree(types.StatePass)
			row.ID = "remedy-lock"
			seedRemedyJob(t, at, row)
			writeSpawnedAgent(hint.NewGate(at.cacheDir, FactsKey("claude", "remedy")), "agent-remedy", spawnedAgent{Description: "root/worker remedy-lock"})
			return Request{Input: "git worktree remove " + wt, Dir: r.main, Host: "claude", Session: "remedy"}, "git worktree remove -f -f " + wt
		},
		func(t *testing.T, at location) (Request, string) {
			r := newWorktreeRepository(t)
			wt, row := r.workerWorktree(types.StateExited)
			row.ID = "remedy-wait"
			seedRemedyJob(t, at, row)
			storeTries(t, at, filedTry(row.ID))
			return Request{Input: "git worktree remove " + wt, Dir: r.main}, hint.JobWait.With("remedy-wait")
		},
	)
}

// TestEveryRemedyRuleHasAContract reads the rules that serve a remedy out of
// the source, so a rule that starts serving one fails until a method drives it.
func TestEveryRemedyRuleHasAContract(t *testing.T) {
	t.Parallel()
	serving := remedyRules(t)
	require.NotEmpty(t, serving, "the sweep found no rule serving a remedy")

	methods := map[string]bool{}
	typ := reflect.TypeFor[*remedyContractSuite]()
	for i := range typ.NumMethod() {
		if name := typ.Method(i).Name; strings.HasPrefix(name, "Test") {
			methods[name] = true
		}
	}
	contracted := map[string]bool{}
	for _, e := range ruleDocRegistry {
		method := "Test" + pascalCase(e.doc.Name)
		contracted[method] = true
		if serving[e.doc.Name] {
			assert.Truef(t, methods[method], "%q serves a remedy and remedyContractSuite has no %s", e.doc.Name, method)
		}
	}
	for method := range methods {
		assert.Truef(t, contracted[method], "remedyContractSuite.%s names no catalogued rule", method)
	}
}

// remedyRules names every rule a remedy is built under in the package source.
// That is a rule handed to a NextStep constructor taking a denyRuleName, or
// one appended to hint.DenyRemedyPrefix.
func remedyRules(t *testing.T) map[string]bool {
	t.Helper()
	src := parseRuleSource(t)
	parsed := parsePackageSource(t)
	constructors := remedyConstructors(parsed)
	rule := func(expr ast.Expr) (string, bool) {
		if conv, ok := expr.(*ast.CallExpr); ok && len(conv.Args) == 1 {
			if fun, ok := conv.Fun.(*ast.Ident); ok && fun.Name == "string" {
				expr = conv.Args[0]
			}
		}
		id, ok := expr.(*ast.Ident)
		if !ok {
			return "", false
		}
		name, ok := src.values[id.Name]
		return name, ok
	}
	out := map[string]bool{}
	for _, f := range parsed {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				var name string
				switch fun := n.Fun.(type) {
				case *ast.Ident:
					name = fun.Name
				case *ast.SelectorExpr:
					name = fun.Sel.Name
				}
				for _, i := range constructors[name] {
					if i < len(n.Args) {
						if r, ok := rule(n.Args[i]); ok {
							out[r] = true
						}
					}
				}
			case *ast.BinaryExpr:
				sel, ok := n.X.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "DenyRemedyPrefix" {
					return true
				}
				if r, ok := rule(n.Y); ok {
					out[r] = true
				}
			}
			return true
		})
	}
	return out
}

func parsePackageSource(t *testing.T) []*ast.File {
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
	return parsed
}

// remedyConstructors maps each function building a NextStep under a rule to
// the index of every denyRuleName parameter it takes.
func remedyConstructors(parsed []*ast.File) map[string][]int {
	constructors := map[string][]int{"NextForDenyRemedy": {0}, "NextForDenyRemedyPipeline": {0}}
	for _, f := range parsed {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Type.Results == nil || !mentionsNextStep(fn.Type.Results) {
				continue
			}
			i := 0
			for _, field := range fn.Type.Params.List {
				for range max(1, len(field.Names)) {
					if isDenyRuleName(field.Type) {
						constructors[fn.Name.Name] = append(constructors[fn.Name.Name], i)
					}
					i++
				}
			}
		}
	}
	return constructors
}

// mentionsNextStep reports whether a result list carries a types.NextStep.
func mentionsNextStep(results *ast.FieldList) bool {
	found := false
	ast.Inspect(results, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "NextStep" {
			found = true
		}
		return !found
	})
	return found
}
