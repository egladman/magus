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
// decode.ContractFuncs and decode.HarnessFuncs, and every exported function named like a
// provider op with decode.ProviderFuncs, reporting every violation in one MGS1051 error so
// a spell author fixes them in one pass. A return type other than the contract's (`> any`
// included), none at all, the wrong parameters, or an mgs_ name no contract lists refuses
// the spell.
func checkContract(src string) error {
	prog, err := parsecache.Shared().ParseEmbedded(src)
	if err != nil {
		return fmt.Errorf("magus/spell: contract check: %w", err)
	}
	contract := make(map[string]decode.ContractFunc, len(decode.ContractFuncs)+len(decode.HarnessFuncs))
	for _, table := range [][]decode.ContractFunc{decode.ContractFuncs, decode.HarnessFuncs} {
		for _, fn := range table {
			contract[fn.Name] = fn
		}
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
			problems = append(problems, checkSignature(fd, op)...)
			continue
		}
		if !strings.HasPrefix(fd.Name, "mgs_") {
			continue
		}
		fn, known := contract[fd.Name]
		if !known {
			problems = append(problems, fmt.Sprintf("line %d: %s is not a spell contract function", fd.Line, fd.Name))
			continue
		}
		problems = append(problems, checkSignature(fd, fn)...)
	}
	if len(problems) == 0 {
		return nil
	}
	return types.DiagnosticErrorf(types.SpellContractViolated,
		"spell does not match the spell contract (docs/reference/spell-contract.md):\n  %s", strings.Join(problems, "\n  "))
}

// checkSignature holds an export to fn's parameter types and return type. Parameter names
// are the spell's own; a function whose Params is empty takes none.
func checkSignature(fd *ast.FunDecl, fn decode.ContractFunc) []string {
	var wantTypes []string
	if fn.Params != "" {
		for _, param := range strings.Split(fn.Params, ",") {
			_, typ, _ := strings.Cut(param, ":")
			wantTypes = append(wantTypes, squash(typ))
		}
	}
	gotTypes := make([]string, len(fd.ParamAnnots))
	for i, annot := range fd.ParamAnnots {
		gotTypes[i] = squash(annot)
	}
	var problems []string
	switch {
	case fn.Params == "" && len(fd.Params) > 0:
		problems = append(problems, fmt.Sprintf("line %d: %s takes parameters; a contract function takes none", fd.Line, fd.Name))
	case !slices.Equal(gotTypes, wantTypes):
		problems = append(problems, fmt.Sprintf("line %d: %s takes (%s); the contract wants (%s)",
			fd.Line, fd.Name, strings.Join(fd.ParamAnnots, ", "), fn.Params))
	}
	return append(problems, checkReturn(fd, fn.Returns)...)
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
