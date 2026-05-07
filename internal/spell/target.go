package spell

import (
	_ "embed"
	"strings"
)

// TargetModulePath is the import path of the canonical magus value-types module.
// A spell does `import "magus/target";` to bring Target/Charm into scope
// so its mgs_listTargets can be typed as a map of fun(Target, fun(any)) handlers
// instead of `any`. The runtime registers the module as embedded source (see the
// buzz bindings' registerHostModules); the built-in spell generator inlines it so
// each compiled built-in is self-contained.
const TargetModulePath = "magus/target"

// TargetModuleSource is the Buzz source of the magus/target module. It is the
// single source of truth for the Target/Charm/Strategy types, consumed both at
// runtime (as a source module) and at built-in generation time (inlined into each
// built-in).
//
//go:embed buzzlib/target.bzz
var TargetModuleSource string

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
