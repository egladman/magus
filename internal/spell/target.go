package spell

import (
	_ "embed"
	"strings"
)

// TargetModulePath is the import path of the canonical magus value-types module.
// A spell does `import "magus/target";` to bring Target into scope so its
// mgs_listTargets can be typed as a map of fun(Target, fun(any)) handlers instead
// of `any`. The runtime registers the module as embedded source (see the buzz
// bindings' registerHostModules); the built-in spell generator inlines it so each
// compiled built-in is self-contained.
const TargetModulePath = "magus/target"

// TargetModuleSource is the generated Buzz `object Target` mirror of types.Target
// (see cmd/magus-types-gen) — the canonical work-unit value type. It is consumed
// both at runtime (as part of the magus/target source module) and at built-in
// generation time (inlined into each built-in via SelfContainedBuiltinSource). The
// other value types (TargetQuery/ExecResult/OpResult/TargetResult) are appended at
// runtime registration only (see registerHostModules), since built-ins don't use
// them.
//
//go:generate go run ../../cmd/magus-types-gen -type Target -out buzzlib/target.gen.buzz
//go:embed buzzlib/target.gen.buzz
var TargetModuleSource string

// TargetQuerySource is the generated Buzz `object TargetQuery` mirror of
// types.TargetQuery (see cmd/magus-types-gen). It ships in the magus/target source
// module so a magusfile can annotate with or construct a TargetQuery;
// magus.target.literal/glob/regex return the same field shape.
//
//go:generate go run ../../cmd/magus-types-gen -type TargetQuery -out buzzlib/targetquery.gen.buzz
//go:embed buzzlib/targetquery.gen.buzz
var TargetQuerySource string

// ExecResultSource is the generated Buzz `object ExecResult` mirror of
// types.ExecResult (see cmd/magus-types-gen). It ships alongside the other value
// types in the magus/target module so a magusfile can annotate an os.exec /
// magus.cmd / captured-op result as `> ExecResult`; the runtime value is the
// matching {stdout, stderr, code, ok} record (see run.ExecResult.Record).
//
//go:generate go run ../../cmd/magus-types-gen -type ExecResult -out buzzlib/execresult.gen.buzz
//go:embed buzzlib/execresult.gen.buzz
var ExecResultSource string

// SelfContainedBuiltinSource prepares a spell source for a bare compile into an
// embedded built-in. A built-in may import only the pure-types magus/target
// module: that import is stripped and the module's source prepended, so the
// compiled chunk defines Target itself (imports emit no bytecode, so an imported
// type would be missing when the built-in runs from .bo). It returns ok=false if
// the source imports any other module — such a spell needs host bindings a bare
// compile can't provide and is not a built-in. Shared by the built-in generator
// and the bytecode-parity test so both compile built-ins identically.
func SelfContainedBuiltinSource(src string) (string, bool) {
	var kept []string
	inlineModule := false
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "import ") {
			if importPath(line) != TargetModulePath {
				return "", false // imports a host module → not a built-in
			}
			inlineModule = true
			continue // strip; the module source is prepended below
		}
		kept = append(kept, line)
	}
	body := strings.Join(kept, "\n")
	if inlineModule {
		return TargetModuleSource + "\n" + body, true
	}
	return body, true
}

// IsSelfContained reports whether src is a fork spell — one that imports only the
// pure-types magus/target module (or nothing). Such a spell's op handlers run a
// declared command via the injected run-callback and carry no host side effects,
// so their specs can be extracted once at spec-resolution time (see
// resolveOps). A spell importing host modules (e.g. magus/extra/http) is a
// function-op spell whose handlers must be dispatched at invoke time instead.
func IsSelfContained(src string) bool {
	_, ok := SelfContainedBuiltinSource(src)
	return ok
}

// importPath extracts the quoted module path from an import line, or "" if none.
func importPath(line string) string {
	i := strings.IndexByte(line, '"')
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(line[i+1:], '"')
	if j < 0 {
		return ""
	}
	return line[i+1 : i+1+j]
}
