package buzz

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/types"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// foreignMethodNames maps a method name other languages spell to the builtin
// that does the job here, per receiver kind. Only names with a one-to-one
// counterpart belong; TestForeignMethodNamesResolve checks every target exists.
var foreignMethodNames = map[vm.ReceiverKind]map[string]string{
	vm.ListReceiver: {
		"push": "append", "add": "append", "length": "len", "size": "len", "count": "len",
		"contains": "indexOf", "includes": "indexOf", "find": "indexOf", "slice": "sub",
		"each": "forEach", "unshift": "insert",
	},
	vm.MapReceiver: {
		"has": "hasKey", "containsKey": "hasKey", "contains": "hasKey", "delete": "remove",
		"length": "len", "count": "len",
	},
	vm.StrReceiver: {
		"toUpperCase": "upper", "toLowerCase": "lower", "toUpper": "upper", "toLower": "lower",
		"length": "len", "size": "len", "strip": "trim", "contains": "indexOf",
		"includes": "indexOf", "find": "indexOf", "substring": "sub", "substr": "sub",
		"slice": "sub", "hasPrefix": "startsWith", "hasSuffix": "endsWith",
	},
}

var receiverNouns = map[vm.ReceiverKind]string{vm.ListReceiver: "lists", vm.MapReceiver: "maps", vm.StrReceiver: "strings"}

// checkBuiltinMethod reports a member of a list, map or str receiver that is not
// one of its builtins. Upstream rejects these at compile time ("List property
// doesn't exist.", Parser.zig dot); the VM answers null for them, so the call
// failed later as "null is not callable" with no position.
func (c *checker) checkBuiltinMethod(v *ast.MemberExpr, kind vm.ReceiverKind, recv types.Type) {
	if vm.HasBuiltinMethod(kind, v.Name) {
		return
	}
	noun := receiverNouns[kind]
	if s := suggestBuiltinMethod(kind, v.Name); s != "" {
		c.errorf(v.Pos, "unknown method %s on %s; %s have %s", v.Name, recv.TypeName(), noun, s)
		return
	}
	c.errorf(v.Pos, "unknown method %s on %s; %s have %s", v.Name, recv.TypeName(), noun, strings.Join(vm.BuiltinMethods(kind), ", "))
}

func (c *checker) noteBuiltinRecv(v *ast.MemberExpr, recv types.Type) {
	if c.builtinRecv == nil {
		c.builtinRecv = map[*ast.MemberExpr]types.Type{}
	}
	c.builtinRecv[v] = recv
}

// suggestBuiltinMethod names the builtin a mistaken name most likely meant: a
// known foreign spelling, then a case-insensitive match, then the closest name
// within two edits. It returns "" when nothing is close.
func suggestBuiltinMethod(kind vm.ReceiverKind, name string) string {
	if s, ok := foreignMethodNames[kind][name]; ok {
		return s
	}
	best, bestDist := "", 3
	for _, m := range vm.BuiltinMethods(kind) {
		if strings.EqualFold(m, name) {
			return m
		}
		if d := editDistance(strings.ToLower(m), strings.ToLower(name)); d < bestDist {
			best, bestDist = m, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b, in bytes.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// checkNamespaceDot reports `mod.member` where mod names an imported module.
// Upstream binds no value under a module's name, only qualified names, so the
// dot form is "`std` is not defined" there; here the module is a map value, so
// the dot form ran anyway and taught a spelling upstream refuses.
//
// TODO: drop the embedded exemption once magus's embedded test sources stop
// spelling `fs.writeFile` and `magus.log.info` (job buzz-dot-migration).
func (c *checker) checkNamespaceDot(v *ast.MemberExpr) {
	if v.Namespaced || c.embedded {
		return
	}
	id, ok := v.Object.(*ast.IdentExpr)
	if !ok {
		return
	}
	e, ok := c.lookup(id.Name)
	if !ok {
		return
	}
	if nt, isObj := e.typ.(*types.ObjectType); !e.module && !(isObj && nt.IsNamespace) {
		return
	}
	c.errorf(v.Pos, "%s is a module, so its members are reached with a backslash: write %s\\%s", id.Name, id.Name, v.Name)
}

// undefinedCallHint is the fix for calling a name Buzz spells as a method, or ""
// when name is not one. Upstream defines no global len; it is a method of str,
// list, map and range.
func undefinedCallHint(name string, args []ast.Node) string {
	if name != "len" || len(args) != 1 {
		return ""
	}
	recv := "value"
	if id, ok := args[0].(*ast.IdentExpr); ok {
		recv = id.Name
	}
	return fmt.Sprintf("; len is a method in Buzz: write %s.len()", recv)
}
