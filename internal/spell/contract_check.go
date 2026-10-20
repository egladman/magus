package spell

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/egladman/magus/libs/gopherbuzz/ast"

	"github.com/egladman/magus/internal/interp/bindings/gen/decode"
	"github.com/egladman/magus/internal/parsecache"
	"github.com/egladman/magus/types"
)

// checkContract compares every exported top-level mgs_ function in src with
// decode.ContractFuncs, and every exported function named like a provider op with
// decode.ProviderFuncs, reporting every violation in one MGS1051 error so a spell
// author fixes them in one pass. A return type other than the contract's (`> any`
// included), none at all, the wrong parameters, or an mgs_ name the contract lacks
// refuses the spell.
func checkContract(src string) error {
	prog, err := parsecache.Shared().ParseEmbedded(src)
	if err != nil {
		return fmt.Errorf("magus/spell: contract check: %w", err)
	}
	want := make(map[string]string, len(decode.ContractFuncs))
	for _, fn := range decode.ContractFuncs {
		want[fn.Name] = fn.Returns
	}
	// Two families may share an op name; the generator refuses one read two ways, so
	// either row says what the export must declare.
	ops := make(map[string]decode.ContractFunc, len(decode.ProviderFuncs))
	for _, fn := range decode.ProviderFuncs {
		ops[fn.Name] = fn
	}

	var problems []string
	for _, stmt := range prog.Stmts {
		fd, ok := stmt.(*ast.FunDecl)
		if !ok || !fd.IsExported {
			continue
		}
		if op, ok := ops[fd.Name]; ok {
			problems = append(problems, checkProviderOp(fd, op)...)
			continue
		}
		if !strings.HasPrefix(fd.Name, "mgs_") {
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
		problems = append(problems, checkReturn(fd, returns)...)
	}
	if len(problems) == 0 {
		return nil
	}
	return types.DiagnosticErrorf(types.SpellContractViolated,
		"spell does not match the spell contract (docs/reference/spell-contract.md):\n  %s", strings.Join(problems, "\n  "))
}

// checkProviderOp holds a provider op's export to op's parameter types and return type.
// Parameter names are the spell's own.
func checkProviderOp(fd *ast.FunDecl, op decode.ContractFunc) []string {
	var wantTypes []string
	for _, param := range strings.Split(op.Params, ",") {
		_, typ, _ := strings.Cut(param, ":")
		wantTypes = append(wantTypes, squash(typ))
	}
	gotTypes := make([]string, len(fd.ParamAnnots))
	for i, annot := range fd.ParamAnnots {
		gotTypes[i] = squash(annot)
	}
	var problems []string
	if !slices.Equal(gotTypes, wantTypes) {
		problems = append(problems, fmt.Sprintf("line %d: %s takes (%s); the provider contract wants (%s)",
			fd.Line, fd.Name, strings.Join(fd.ParamAnnots, ", "), op.Params))
	}
	return append(problems, checkReturn(fd, op.Returns)...)
}

func checkReturn(fd *ast.FunDecl, returns string) []string {
	switch {
	case fd.RetAnnot == "":
		return []string{fmt.Sprintf("line %d: %s declares no return type; the contract wants > %s", fd.Line, fd.Name, returns)}
	case squash(fd.RetAnnot) != squash(returns):
		return []string{fmt.Sprintf("line %d: %s declares > %s; the contract wants > %s", fd.Line, fd.Name, fd.RetAnnot, returns)}
	}
	return nil
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
