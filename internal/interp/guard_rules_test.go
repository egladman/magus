package interp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/parsecache"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
)

// reduce filters src as a guard-rules load would, with files standing in for
// the file search: an import path absent from it resolves to no file.
func reduce(t *testing.T, src string, files map[string]string) *ast.Program {
	t.Helper()
	prog, err := parsecache.Shared().ParseEmbedded(src)
	require.NoError(t, err)
	guardRulesFilter(prog, func(p string, _ ...string) (string, []byte, bool) {
		body, ok := files[p]
		return p, []byte(body), ok
	})
	return prog
}

func stmtKinds(prog *ast.Program) []string {
	var out []string
	for _, stmt := range prog.Stmts {
		switch s := stmt.(type) {
		case *ast.ImportStmt:
			out = append(out, "import "+s.Path)
		case *ast.FunDecl:
			out = append(out, "fun "+s.Name)
		case *ast.ExprStmt:
			out = append(out, "expr")
		case *ast.DeclStmt:
			out = append(out, "decl "+s.Name)
		case *ast.EnumDecl:
			out = append(out, "enum "+s.Name)
		case *ast.ObjectDecl:
			out = append(out, "object "+s.Name)
		default:
			out = append(out, "other")
		}
	}
	return out
}

func TestGuardRulesFilterDropsSpellImport(t *testing.T) {
	prog := reduce(t, `
import "magus";
import "spells/harness/cursor" as harness;
import "./rules" as rules;
magus\harness.provider(harness);
magus\guard.command(rules\judge);
`, nil)
	assert.Equal(t, []string{"import magus", "import ./rules", "expr"}, stmtKinds(prog))
}

func TestGuardRulesFilterKeepsHelperAndCall(t *testing.T) {
	prog := reduce(t, `
import "magus";
import "./rules" as rules;
import "./unused" as unused;
fun register() {
    magus\guard.command(rules\judge);
}
register();
`, map[string]string{"./unused": "export fun other() > void {}\n"})
	assert.Equal(t, []string{"import magus", "import ./rules", "fun register", "expr"}, stmtKinds(prog))
}

func TestGuardRulesFilterLeavesUnrelatedProgram(t *testing.T) {
	prog := reduce(t, `
import "magus";
import "spells/harness/cursor" as harness;
magus\harness.provider(harness);
`, nil)
	assert.Equal(t, []string{"import magus", "import spells/harness/cursor", "expr"}, stmtKinds(prog))
}

func TestGuardRulesFilterKeepsFlatImport(t *testing.T) {
	prog := reduce(t, `
import "magus";
import "./rules";
import "spells/harness/cursor";
magus\guard.command(judge);
`, nil)
	assert.Equal(t, []string{"import magus", "import ./rules", "expr"}, stmtKinds(prog))
}

// A type named only in an annotation is still a name the kept code needs.
func TestGuardRulesFilterKeepsAnnotationTypes(t *testing.T) {
	files := map[string]string{
		"./types": "export object Req { cmd: str }\n",
		"./other": "export fun other() > void {}\n",
	}
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{
			name: "enum in a declaration",
			src: `
import "magus";
enum Mode { strict, lax }
final mode: Mode = .strict;
fun judge(cmd: str) > str { return "{mode}"; }
magus\guard.command(judge);
`,
			want: []string{"import magus", "enum Mode", "decl mode", "fun judge", "expr"},
		},
		{
			name: "aliased type in a parameter",
			src: `
import "magus";
import "./types" as t;
import "./other" as other;
fun judge(r: t\Req) > str { return r.cmd; }
magus\guard.command(judge);
`,
			want: []string{"import magus", "import ./types", "fun judge", "expr"},
		},
		{
			name: "object in a return type",
			src: `
import "magus";
object Verdict { text: str }
fun judge(cmd: str) > Verdict? { return null; }
magus\guard.command(judge);
`,
			want: []string{"import magus", "object Verdict", "fun judge", "expr"},
		},
		{
			name: "catch clause and as",
			src: `
import "magus";
object Oops { why: str }
object Shape { n: int }
fun judge(cmd: str) > str {
    try { return "{cmd as Shape}"; } catch (e: Oops) { return e.why; }
}
magus\guard.command(judge);
`,
			want: []string{"import magus", "object Oops", "object Shape", "fun judge", "expr"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, stmtKinds(reduce(t, tc.src, files)))
		})
	}
}

// An import nothing references is kept when loading it could register a rule.
func TestGuardRulesFilterKeepsImportsThatRegister(t *testing.T) {
	files := map[string]string{
		"./extra-rules": "import \"magus\";\nmagus\\guard.shell({\"name\": \"x\"});\n",
		"./aliased":     "import \"magus\" as m;\nfun add() > void { m\\guard.write(nothing); }\nadd();\n",
		"./wrapper":     "import \"./extra-rules\";\n",
		"./verdicts":    "import \"magus\";\nexport fun no() > any { return magus\\guard.deny(\"no\"); }\n",
		"./broken":      "fun (\n",
		"./plain":       "export fun other() > void {}\n",
	}
	prog := reduce(t, `
import "magus";
import "./rules" as p;
import "./extra-rules";
import "./aliased" as aliased;
import "./wrapper";
import "./verdicts";
import "./broken";
import "./plain";
import "./missing";
import "project/libs/x";
magus\guard.command(p\judge);
`, files)
	assert.Equal(t, []string{
		"import magus",
		"import ./rules",
		"import ./extra-rules",
		"import ./aliased",
		"import ./wrapper",
		"import ./broken",
		"import ./missing",
		"expr",
	}, stmtKinds(prog))
}

// A registration through an alias of magus\guard, or of magus, still counts.
func TestGuardRulesFilterFollowsAliases(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{
			name: "variable",
			src: `
import "magus";
import "./rules" as p;
var g = magus\guard;
g.shell({"name": "x"});
magus\guard.command(p\judge);
`,
			want: []string{"import magus", "import ./rules", "decl g", "expr", "expr"},
		},
		{
			name: "import alias",
			src: `
import "magus" as m;
import "./rules" as p;
m\guard.shell({"name": "x"});
m\guard.command(p\judge);
`,
			want: []string{"import magus", "import ./rules", "expr", "expr"},
		},
		{
			name: "selective import",
			src: `
import guard from "magus";
import "./rules" as p;
import "spells/x" as x;
guard.command(p\judge);
`,
			want: []string{"import magus", "import ./rules", "expr"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, stmtKinds(reduce(t, tc.src, nil)))
		})
	}
}

func TestImportNamesStripsTheBuzzScheme(t *testing.T) {
	assert.Equal(t, []string{"std"}, importNames(&ast.ImportStmt{Path: "buzz:std"}))
	assert.Equal(t, []string{"guard"}, importNames(&ast.ImportStmt{Path: "./policy/guard"}))
}
