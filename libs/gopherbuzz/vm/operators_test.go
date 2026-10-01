package vm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"
)

// TestBuiltinMethodNamesMatchDispatch reads the `switch name` cases of each
// method dispatcher out of operators.go, so a method added to a switch without
// the table (or the reverse) fails here instead of in the checker.
func TestBuiltinMethodNamesMatchDispatch(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "operators.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	dispatchers := map[string]ReceiverKind{"listMethod": ListReceiver, "mapMethod": MapReceiver, "strMethod": StrReceiver}
	seen := map[string]bool{}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		kind, ok := dispatchers[fd.Name.Name]
		if !ok {
			continue
		}
		seen[fd.Name.Name] = true
		got := map[string]bool{}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			if tag, ok := sw.Tag.(*ast.Ident); !ok || tag.Name != "name" {
				return true
			}
			for _, s := range sw.Body.List {
				for _, e := range s.(*ast.CaseClause).List {
					lit, ok := e.(*ast.BasicLit)
					if !ok {
						t.Fatalf("%s: non-literal case %T", fd.Name.Name, e)
					}
					name, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					got[name] = true
				}
			}
			return true
		})
		want := BuiltinMethods(kind)
		if !slices.IsSorted(want) {
			t.Errorf("%s table is not sorted: %v", fd.Name.Name, want)
		}
		for _, name := range want {
			if !got[name] {
				t.Errorf("%s table lists %q but the switch has no such case", fd.Name.Name, name)
			}
		}
		for name := range got {
			if !HasBuiltinMethod(kind, name) {
				t.Errorf("%s switch resolves %q but the table omits it", fd.Name.Name, name)
			}
		}
	}
	for name := range dispatchers {
		if !seen[name] {
			t.Errorf("operators.go no longer declares %s", name)
		}
	}
}

func TestHasBuiltinMethod(t *testing.T) {
	cases := []struct {
		kind ReceiverKind
		name string
		want bool
	}{
		{ListReceiver, "append", true},
		{ListReceiver, "push", false},
		{StrReceiver, "upper", true},
		{StrReceiver, "toUpperCase", false},
		{MapReceiver, "hasKey", true},
		{MapReceiver, "items", false},
	}
	for _, tc := range cases {
		if got := HasBuiltinMethod(tc.kind, tc.name); got != tc.want {
			t.Errorf("HasBuiltinMethod(%d, %q) = %v, want %v", tc.kind, tc.name, got, tc.want)
		}
	}
}
