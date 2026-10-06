package scipbuzz

import (
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/scip-code/scip/bindings/go/scip"
)

// Scheme is the SCIP scheme of every global symbol this indexer emits.
const Scheme = "scip-buzz"

// workspacePackage identifies code read from the workspace. Name and version are
// empty (written `.`) so a symbol is the same string in every checkout and every
// project that references it.
func workspacePackage() *scip.Package { return &scip.Package{Manager: "buzz"} }

// hostPackage identifies a module no workspace file provides: a host module such as
// fs or magus, a project/ handle, a remote spell, or a path that does not resolve.
func hostPackage() *scip.Package { return &scip.Package{Manager: "buzz", Name: "host"} }

func formatSymbol(pkg *scip.Package, descriptors ...*scip.Descriptor) string {
	return scip.VerboseSymbolFormatter.FormatSymbol(&scip.Symbol{
		Scheme:      Scheme,
		Package:     pkg,
		Descriptors: descriptors,
	})
}

// declSymbol names a top-level declaration of the workspace file at rel, a
// workspace-relative slash path.
func declSymbol(rel string, kind declKind, name string) string {
	return formatSymbol(workspacePackage(),
		&scip.Descriptor{Name: rel, Suffix: scip.Descriptor_Namespace},
		&scip.Descriptor{Name: name, Suffix: kind.suffix()})
}

// hostSymbol names a member of an external module. The source never says whether a
// host member is a function, a type or a value, so the spelling decides, the same
// way in every index: a capitalized name is a type and anything else a function.
func hostSymbol(module, member string) (string, scip.SymbolInformation_Kind) {
	suffix, kind := scip.Descriptor_Method, scip.SymbolInformation_Function
	if r, _ := utf8.DecodeRuneInString(member); unicode.IsUpper(r) {
		suffix, kind = scip.Descriptor_Type, scip.SymbolInformation_UnspecifiedKind
	}
	return formatSymbol(hostPackage(),
		&scip.Descriptor{Name: module, Suffix: scip.Descriptor_Namespace},
		&scip.Descriptor{Name: member, Suffix: suffix}), kind
}

func localSymbol(n int) string { return "local " + strconv.Itoa(n) }

// declKind is what a top-level declaration introduces.
type declKind int

const (
	kindFun declKind = iota
	kindExtern
	kindFinal
	kindVar
	kindObject
	kindProtocol
	kindEnum
)

func (k declKind) suffix() scip.Descriptor_Suffix {
	switch k {
	case kindFun, kindExtern:
		return scip.Descriptor_Method
	case kindObject, kindProtocol, kindEnum:
		return scip.Descriptor_Type
	default:
		return scip.Descriptor_Term
	}
}

func (k declKind) scipKind() scip.SymbolInformation_Kind {
	switch k {
	case kindFun, kindExtern:
		return scip.SymbolInformation_Function
	case kindFinal:
		return scip.SymbolInformation_Constant
	case kindVar:
		return scip.SymbolInformation_Variable
	case kindObject:
		return scip.SymbolInformation_Object
	case kindProtocol:
		return scip.SymbolInformation_Protocol
	case kindEnum:
		return scip.SymbolInformation_Enum
	default:
		return scip.SymbolInformation_UnspecifiedKind
	}
}

// isType reports whether a name of this kind may appear in a type annotation.
func (k declKind) isType() bool { return k == kindObject || k == kindProtocol || k == kindEnum }
