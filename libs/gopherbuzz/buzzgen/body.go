package buzzgen

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"strconv"
	"strings"
)

const scalarVM = "github.com/egladman/magus/libs/gopherbuzz/vm"

// ScalarFunction lowers a Go function on one VM value into a Buzz function.
// Its caller supplies the value independently of the object's layout.
func (p *Program) ScalarFunction(pkgPath, goFunction, buzzFunction, resultType string) (string, error) {
	pkg, err := p.Package(pkgPath)
	if err != nil {
		return "", err
	}
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != goFunction {
				continue
			}
			sig := pkg.TypesInfo.Defs[fn.Name].Type().(*types.Signature)
			isVM := func(t types.Type) bool {
				n, ok := t.(*types.Named)
				return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == scalarVM && n.Obj().Name() == "Value"
			}
			if sig.Params().Len() != 1 || sig.Results().Len() != 1 || !isVM(sig.Params().At(0).Type()) || !isVM(sig.Results().At(0).Type()) || fn.Body == nil {
				return "", fmt.Errorf("%s: expected func(vm.Value) vm.Value", pkg.Fset.Position(fn.Pos()))
			}
			isParam := func(x ast.Expr) bool {
				id, ok := x.(*ast.Ident)
				return ok && pkg.TypesInfo.ObjectOf(id) == sig.Params().At(0)
			}
			member := func(x ast.Expr) string {
				s, ok := x.(*ast.SelectorExpr)
				if ok {
					obj := pkg.TypesInfo.ObjectOf(s.Sel)
					if obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == scalarVM {
						return obj.Name()
					}
				}
				return ""
			}
			var out strings.Builder
			fmt.Fprintf(&out, "fun %s(data: any) > %s {\n", buzzFunction, resultType)
			var emit func(*ast.BlockStmt) error
			emit = func(block *ast.BlockStmt) error {
				for _, stmt := range block.List {
					switch s := stmt.(type) {
					case *ast.IfStmt:
						call, ok := s.Cond.(*ast.CallExpr)
						if !ok || s.Init != nil || s.Else != nil || len(call.Args) != 0 {
							return fmt.Errorf("%s: unsupported condition", pkg.Fset.Position(s.Pos()))
						}
						sel, ok := call.Fun.(*ast.SelectorExpr)
						typ := map[string]string{"IsInt":"int","IsBool":"bool","IsString":"str","IsFloat":"double"}[member(call.Fun)]
						if !ok || !isParam(sel.X) || typ == "" {
							return fmt.Errorf("%s: unsupported predicate", pkg.Fset.Position(call.Pos()))
						}
						fmt.Fprintf(&out, "if (data is %s) {\n", typ)
						if err := emit(s.Body); err != nil {
							return err
						}
						out.WriteString("}\n")
					case *ast.ReturnStmt:
						var val string
						if len(s.Results) == 1 {
							x := s.Results[0]
							val = map[string]string{"Null":"null","False":"false","True":"true"}[member(x)]
							if isParam(x) {
								val = "data"
							}
							if c, ok := x.(*ast.CallExpr); ok && len(c.Args)==1 {
								v := pkg.TypesInfo.Types[c.Args[0]].Value
								if v != nil {
									switch member(c.Fun) {
									case "StringValue":
										str := constant.StringVal(v)
										if strings.Trim(str,"abcdefghijklmnopqrstuvwxyz ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")=="" {
											val = strconv.Quote(str)
										}
									case "IntValue":
										n, exact := constant.Int64Val(v)
										if exact && n >= -2147483648 && n <= 2147483647 {
											val = strconv.FormatInt(n,10)
										}
									case "FloatValue":
										f,_ := constant.Float64Val(v)
										val = strconv.FormatFloat(f,'f',-1,64)
										if !strings.Contains(val,".") {
											val += ".0"
										}
									}
								}
							}
						}
						if val == "" {
							return fmt.Errorf("%s: unsupported return expression", pkg.Fset.Position(s.Pos()))
						}
						fmt.Fprintf(&out,"return %s;\n",val)
					default:
						return fmt.Errorf("%s: unsupported statement %T", pkg.Fset.Position(stmt.Pos()),stmt)
					}
				}
				return nil
			}
			if err := emit(fn.Body); err != nil {
				return "", err
			}
			return out.String()+"}\n", nil
		}
	}
	return "", fmt.Errorf("buzzgen: %s declares no function %s",pkgPath,goFunction)
}
