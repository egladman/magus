package knowledge

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/ast"
	"github.com/egladman/gopherbuzz/token"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// BuzzShardName is the singleton shard holding buzz-source nodes (files,
// functions, imports, rationale comments) across the whole workspace.
const BuzzShardName = "@buzz"

// markerRe matches a rationale marker inside a comment block (the Graphify idea
// worth copying: NOTE/WHY/HACK/TODO comments carry the "why" a reader most wants).
// It runs over the lexer's already-stripped comment text (no leading "//"), so a
// "//" inside a string literal can never false-match.
var markerRe = regexp.MustCompile(`^\s*(NOTE|WHY|HACK|TODO|FIXME|XXX)\b:?\s*(.*)`)

// fnLine is a top-level function's name and declaration line, used to bind a
// rationale comment to the function that encloses it.
type fnLine struct {
	name string
	line int
}

// assembleBuzz walks every .buzz source file under root and extracts: a file node
// per file, a function node per declaration (file contains function), intra-file
// call edges (function calls function), import edges (resolved to a file where
// possible, else an inferred edge to the literal), and rationale nodes tied to
// their enclosing function. Deterministic and LLM-free.
func assembleBuzz(root string) Shard {
	s := Shard{Name: BuzzShardName}
	files := findBuzzFiles(root)
	scanned := make(map[string]bool, len(files))
	for _, f := range files {
		scanned[f] = true
	}

	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		content := string(src)
		fID := fileID(rel)
		s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: fID, Kind: types.KindFile, Label: rel, Source: rel})

		prog, err := buzz.ParseEmbedded(content)
		if err != nil || prog == nil {
			continue
		}

		// Top-level function names (for intra-file call resolution) and their lines
		// (for binding rationale comments to the enclosing function).
		sameFile := map[string]bool{}
		var fnLines []fnLine
		for _, stmt := range prog.Stmts {
			if d, ok := stmt.(*ast.FunDecl); ok {
				sameFile[d.Name] = true
				fnLines = append(fnLines, fnLine{d.Name, d.Pos.Line})
			}
		}

		for _, stmt := range prog.Stmts {
			switch d := stmt.(type) {
			case *ast.FunDecl:
				fnID := functionID(rel, d.Name)
				attrs := map[string]string{}
				if d.IsExported {
					attrs["exported"] = "true"
				}
				s.Nodes = append(s.Nodes, types.KnowledgeNode{
					ID: fnID, Kind: types.KindFunction, Label: d.Name, Doc: d.Doc,
					Source: rel + ":" + strconv.Itoa(d.Pos.Line), Attrs: nilIfEmpty(attrs),
				})
				s.Edges = append(s.Edges, extractedEdge(fID, fnID, types.RelationContains, rel))
				s.Edges = append(s.Edges, callEdges(rel, d, sameFile)...)
			case *ast.ImportStmt:
				if target, ok := resolveBuzzImport(d.Path, scanned); ok {
					s.Edges = append(s.Edges, extractedEdge(fID, fileID(target), types.RelationImports, rel))
				} else {
					iID := importID(d.Path)
					s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: iID, Kind: types.KindImport, Label: d.Path})
					s.Edges = append(s.Edges, inferredEdge(fID, iID, types.RelationImports, rel, 0.7))
				}
			}
		}

		s.Nodes, s.Edges = appendRationale(s.Nodes, s.Edges, rel, content, fnLines)
	}
	return s
}

// appendRationale reuses the Buzz lexer (token.Tokenize) rather than re-scanning
// raw lines: every non-trailing comment block is attached to the next token's Doc
// (already "//"-stripped), so markers inside a string literal never match, and a
// marker is bound to the code it precedes (the token's line), which is the
// function it documents. One rationale node per marker line, tied rationale_for to
// the enclosing function.
func appendRationale(nodes []types.KnowledgeNode, edges []types.KnowledgeEdge, rel, content string, fnLines []fnLine) ([]types.KnowledgeNode, []types.KnowledgeEdge) {
	toks, err := token.Tokenize(content)
	if err != nil {
		return nodes, edges
	}
	seen := map[string]bool{}
	for _, tk := range toks {
		if tk.Doc == "" {
			continue
		}
		for _, dl := range strings.Split(tk.Doc, "\n") {
			m := markerRe.FindStringSubmatch(dl)
			if m == nil {
				continue
			}
			prov := rel + ":" + strconv.Itoa(tk.Line)
			rID := rationaleID(rel, tk.Line) + ":" + m[1]
			if seen[rID] {
				continue
			}
			seen[rID] = true
			nodes = append(nodes, types.KnowledgeNode{
				ID: rID, Kind: types.KindRationale, Label: m[1], Doc: strings.TrimSpace(m[2]), Source: prov,
			})
			if fn := enclosingFunction(fnLines, tk.Line); fn != "" {
				edges = append(edges, extractedEdge(rID, functionID(rel, fn), types.RelationRationaleFor, prov))
			}
		}
	}
	return nodes, edges
}

// callEdges returns the intra-file function->function call edges for one function:
// bare calls (IdentExpr callee) to another function defined in the same file,
// deduped, excluding self-recursion. Reuses describe.Inspect for full AST coverage
// so a call nested in an if-condition or loop body is not missed.
func callEdges(rel string, d *ast.FunDecl, sameFile map[string]bool) []types.KnowledgeEdge {
	fnID := functionID(rel, d.Name)
	seen := map[string]bool{}
	var out []types.KnowledgeEdge
	describe.Inspect(d.Body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := ce.Callee.(*ast.IdentExpr)
		if ok && sameFile[id.Name] && id.Name != d.Name && !seen[id.Name] {
			seen[id.Name] = true
			out = append(out, extractedEdge(fnID, functionID(rel, id.Name), types.RelationCalls, rel))
		}
		return true
	})
	return out
}

// enclosingFunction returns the name of the function whose declaration line is the
// greatest <= line (i.e. the function the comment sits inside), or "" when the
// comment precedes the first function.
func enclosingFunction(fnLines []fnLine, line int) string {
	name := ""
	best := -1
	for _, f := range fnLines {
		if f.line <= line && f.line > best {
			best, name = f.line, f.name
		}
	}
	return name
}

// resolveBuzzImport best-effort maps an import path to a scanned .buzz file (rel to
// root). It is a graph-linking heuristic, not an authoritative reimplementation of
// Buzz's resolver: candidates cover the file (<path>.buzz) and directory-module
// (<path>/spell.buzz) forms, resolved root-relative (matching upstream's CWD-
// relative resolution - see the upstream-buzz-import-resolution memory). Built-in
// magus/* imports and anything not matching a scanned file stay unresolved (the
// caller records the literal as an inferred edge).
func resolveBuzzImport(path string, scanned map[string]bool) (string, bool) {
	for _, c := range []string{path + ".buzz", path + "/spell.buzz", path} {
		if strings.HasSuffix(c, ".buzz") && scanned[filepath.ToSlash(filepath.Clean(c))] {
			return filepath.ToSlash(filepath.Clean(c)), true
		}
	}
	return "", false
}

// findBuzzFiles returns every workspace .buzz source path (rel to root), sorted,
// skipping ignore dirs (dot-dirs, vendor, ...) and testdata fixtures (not source).
func findBuzzFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && (project.IsIgnoreDir(d.Name()) || d.Name() == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".buzz") {
			if rel, err := filepath.Rel(root, path); err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	slices.Sort(out)
	return out
}
