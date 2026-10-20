package spell

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/egladman/magus/libs/gopherbuzz/ast"

	"github.com/egladman/magus/internal/interp/bindings/gen/decode"
	"github.com/egladman/magus/internal/parsecache"
	"github.com/egladman/magus/types"
)

// checkContract compares every exported top-level mgs_ function in src with
// decode.ContractFuncs, reporting every violation in one MGS1051 error so a spell
// author fixes them in one pass. A return type other than the contract's (`> any`
// included), none at all, parameters, or a name the contract lacks refuses the spell.
func checkContract(src string) error {
	prog, err := parsecache.Shared().ParseEmbedded(src)
	if err != nil {
		return fmt.Errorf("magus/spell: contract check: %w", err)
	}
	want := make(map[string]string, len(decode.ContractFuncs))
	for _, fn := range decode.ContractFuncs {
		want[fn.Name] = fn.Returns
	}

	var problems []string
	for _, stmt := range prog.Stmts {
		fd, ok := stmt.(*ast.FunDecl)
		if !ok || !fd.IsExported || !strings.HasPrefix(fd.Name, "mgs_") {
			continue
		}
		returns, known := want[fd.Name]
		switch {
		case !known:
			problems = append(problems, fmt.Sprintf("line %d: %s is not a spell contract function", fd.Line, fd.Name))
			continue
		case len(fd.Params) > 0:
			problems = append(problems, fmt.Sprintf("line %d: %s takes parameters; a contract function takes none", fd.Line, fd.Name))
		}
		switch {
		case fd.RetAnnot == "":
			problems = append(problems, fmt.Sprintf("line %d: %s declares no return type; the contract wants > %s", fd.Line, fd.Name, returns))
		case squash(fd.RetAnnot) != squash(returns):
			problems = append(problems, fmt.Sprintf("line %d: %s declares > %s; the contract wants > %s", fd.Line, fd.Name, fd.RetAnnot, returns))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return types.DiagnosticErrorf(types.SpellContractViolated,
		"spell does not match the spell contract (docs/reference/spell-contract.md):\n  %s", strings.Join(problems, "\n  "))
}

// squash drops whitespace, which a return annotation may carry anywhere without changing
// the type: `{str: Tool}` and `{str:Tool}` are one annotation.
func squash(annot string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, annot)
}
