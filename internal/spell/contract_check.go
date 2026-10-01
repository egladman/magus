package spell

import (
	"fmt"
	"strings"
	"unicode"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"

	"github.com/egladman/magus/internal/interp/bindings/gen/decode"
	"github.com/egladman/magus/types"
)

// ResolveOption configures Resolve.
type ResolveOption func(*resolveOptions)

type resolveOptions struct {
	contractSrc string
	checked     bool
}

// WithContractCheck has Resolve check src, the spell's own source, against the spell
// contract before calling any mgs_ function, and refuse the spell with MGS1051 when an
// exported mgs_ function declares a return type other than the contract's (`> any`
// included), declares none, takes parameters, or is not a contract function at all.
//
// Off unless passed. Only tests pass it until every shipped spell conforms: typescript,
// podman and experimental/nx return `> any` from mgs_listTargets, and the benchmark
// nextjs and jsmod spells export mgs_getVersionProbe, which the contract dropped.
func WithContractCheck(src string) ResolveOption {
	return func(o *resolveOptions) {
		o.contractSrc = src
		o.checked = true
	}
}

// checkContract compares every exported top-level mgs_ function in src with
// decode.ContractFuncs, reporting every violation in one MGS1051 error so a spell
// author fixes them in one pass.
func checkContract(src string) error {
	prog, err := buzz.ParseEmbedded(src)
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
