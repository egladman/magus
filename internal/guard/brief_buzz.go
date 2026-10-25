package guard

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/types"
)

// briefBuzzPrefix is what a buzz-format brief is checked under: the literal is the value of
// one declaration on the import's line, so its own line 1 is the program's line 1.
const briefBuzzPrefix = `import "magus"; final brief = `

// judgeBriefBuzz refuses a buzz-format brief that is not one magus\Brief literal, alone or
// as the only buzz fence, that type-checks against decls, the generated magus declarations.
// format names the format and where it came from, as the verdict says it.
func judgeBriefBuzz(ctx context.Context, text, decls, format string) ShellVerdict {
	deny := func(problems []string) ShellVerdict {
		return ShellVerdict{
			Deny: fmt.Sprintf("this brief is %s, and it is not one `magus\\Brief{...}` literal that type-checks:\n  %s", format, strings.Join(problems, "\n  ")),
			Why: "A buzz-format brief is checked before its worker sees it, so a misnamed field, a wrong type or a missing " +
				"required field is a refusal at spawn rather than a worker acting on half a brief. Write the brief as one " +
				"`magus\\Brief{ job = \"<id>\", goal = \"...\" }` literal, bare or in a single buzz fence.",
			Rule: denyRule{Name: denyRuleBriefCommand, Arg: string(types.BriefFormatBuzz)},
		}
	}
	if !strings.Contains(decls, "export object Brief {") {
		return deny([]string{"this magus build declares no magus\\Brief: regenerate the magus declarations (types-generate) and rebuild"})
	}
	literal := briefBuzzLiteral(text)
	sess := buzz.NewSession(ctx, buzz.WithoutFileImports(), buzz.WithoutFFI())
	defer func() { _ = sess.Close() }()
	sess.SetModuleDecls("magus", decls)
	src := fmt.Sprintf(`%s%s;
`, briefBuzzPrefix, literal)
	var problems []string
	for _, d := range sess.Diagnostics(src) {
		if d.Severity != buzz.SeverityError {
			continue
		}
		code := ""
		if d.Code != "" {
			code = fmt.Sprintf("[%s] ", string(d.Code))
		}
		problems = append(problems, fmt.Sprintf("line %d: %s%s", max(d.Line, 1), code, d.Msg))
	}
	if len(problems) == 0 {
		problems = briefMissingFields(literal)
	}
	if len(problems) > 0 {
		return deny(problems)
	}
	return ShellVerdict{}
}

// briefBuzzLiteral is the brief's one buzz fence, or the whole brief when it has none or
// several; the check then refuses what is not a single literal.
func briefBuzzLiteral(text string) string {
	var blocks []string
	for _, m := range briefFenceRe.FindAllStringSubmatch(text, -1) {
		if strings.EqualFold(m[1], "buzz") {
			blocks = append(blocks, m[2])
		}
	}
	if len(blocks) == 1 {
		return strings.TrimSpace(blocks[0])
	}
	return strings.TrimSpace(text)
}

// briefMissingFields names each required field a magus\Brief literal, or one nested in it,
// leaves out. TODO: delete once gopherbuzz refuses a literal that omits a field with no
// default and the mirror generator emits required fields.
func briefMissingFields(literal string) []string {
	prog, err := buzz.Parse(fmt.Sprintf(`%s%s;
`, briefBuzzPrefix, literal))
	if err != nil {
		return nil
	}
	required := briefRequiredFields
	var missing []string
	for _, stmt := range prog.Stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			lit, ok := n.(*ast.ObjectLit)
			if !ok || lit.Namespace != "magus" {
				return true
			}
			for _, field := range required[lit.TypeName] {
				if !slices.Contains(lit.Keys, field) {
					missing = append(missing, fmt.Sprintf("line %d: magus\\%s needs %s", max(lit.Line, 1), lit.TypeName, field))
				}
			}
			return true
		})
	}
	return missing
}

// briefRequiredFields are the fields of each Brief mirror whose types field carries no
// omitempty; TestBriefRequiredFieldsMatchTheTypes holds the two together.
var briefRequiredFields = map[string][]string{
	"Brief":         {"job", "goal"},
	"BriefDecision": {"what"},
	"BriefRef":      {"kind", "id"},
}

// magusDecls is the generated magus module declarations the binary embeds.
func magusDecls() string {
	src, _ := spell.ModuleDecls("magus")
	return src
}
