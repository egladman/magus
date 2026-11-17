package fim

import (
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzast "github.com/egladman/magus/libs/gopherbuzz/ast"
	buzztoken "github.com/egladman/magus/libs/gopherbuzz/token"
)

// Func is a Buzz function with a body: its name and the byte range inside its braces.
type Func struct {
	Name string
	Body Range
}

// TestBlock is a Buzz `test "name" { ... }` block: its name, the byte range inside its
// braces, and the identifiers its body uses.
type TestBlock struct {
	Name   string
	Body   Range
	Idents map[string]bool
}

// BuzzUnits returns a Buzz file's functions with bodies and its test blocks, from the
// gopherbuzz parser. ok is false when the file does not lex, balance or parse; the
// caller decides what an unreadable file is worth.
func BuzzUnits(src string) (funcs []Func, tests []TestBlock, ok bool) {
	b, ok := lexBuzz(src)
	if !ok {
		return nil, nil, false
	}
	prog, err := buzz.ParseEmbedded(src)
	if err != nil || prog == nil {
		return nil, nil, false
	}
	inside := func(blk *buzzast.BlockStmt) (Range, int, bool) {
		if blk == nil {
			return Range{}, 0, false
		}
		open := b.at(blk.Pos)
		if open < 0 || b.toks[open].Kind != buzztoken.LBrace {
			return Range{}, 0, false
		}
		return Range{b.starts[open] + 1, b.starts[b.match[open]]}, open, true
	}
	visit := func(n buzzast.Node) bool {
		switch n := n.(type) {
		case *buzzast.FunDecl:
			if r, _, ok := inside(n.Body); ok && !n.IsExtern {
				funcs = append(funcs, Func{Name: n.Name, Body: r})
			}
		case *buzzast.TestDecl:
			if r, open, ok := inside(n.Body); ok {
				ids := map[string]bool{}
				for i := open + 1; i < b.match[open]; i++ {
					if b.toks[i].Kind == buzztoken.Ident {
						ids[b.toks[i].Val] = true
					}
				}
				tests = append(tests, TestBlock{Name: n.Name, Body: r, Idents: ids})
			}
		}
		return true
	}
	for _, top := range prog.Stmts {
		buzzast.Inspect(top, visit)
	}
	return funcs, tests, true
}
