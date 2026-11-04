package scipbuzz

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/scip-code/scip/bindings/go/scip"
)

// Project reads one project's Buzz a file at a time, for an indexer that stores each
// file's document apart and rebuilds only the ones that moved. Files reads every file
// once; Document builds from that read, so a Project describes the tree as Files found
// it. Not safe for concurrent use.
type Project struct {
	ix    *indexer
	files map[string]*file // by project-relative path
	// problems holds what each load and walk reported, by workspace-relative path.
	problems map[string][]string
}

// File is one .buzz file under the project root.
type File struct {
	// Path is the project-relative slash path, the document's path.
	Path string
	// Imports is what the file's imports resolve to among the workspace's files, by
	// workspace-relative path, sorted. An import of a host module or of a path no
	// workspace file answers to is not listed.
	Imports []Import
	// Err is why the file was not indexed: it could not be read or did not parse.
	Err error
}

// Import is a workspace file an import resolves to.
type Import struct {
	// Path is the workspace-relative slash path.
	Path string
	// Document is the project-relative path when the file is one of this project's
	// documents, else "".
	Document string
}

// NewProject checks opts's roots the way Index does and returns a Project over them.
func NewProject(opts Options) (*Project, error) {
	ix, err := newIndexer(opts)
	if err != nil {
		return nil, err
	}
	p := &Project{ix: ix, files: map[string]*file{}, problems: map[string][]string{}}
	ix.onWarn = p.note
	return p, nil
}

// note files a problem under the workspace-relative path every message opens with.
func (p *Project) note(msg string) {
	rel, _, _ := strings.Cut(msg, ": ")
	p.problems[rel] = append(p.problems[rel], msg)
	if p.ix.userWarn != nil {
		p.ix.userWarn(msg)
	}
}

// Files discovers and parses every .buzz file under the project root, and resolves
// each one's imports. Discovery skips what Index skips.
func (p *Project) Files(ctx context.Context) ([]File, error) {
	rels, err := discover(p.ix.project)
	if err != nil {
		return nil, fmt.Errorf("discover .buzz files: %w", err)
	}
	out := make([]File, 0, len(rels))
	for _, rel := range rels {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		abs := filepath.Join(p.ix.project, filepath.FromSlash(rel))
		f := p.ix.load(abs)
		entry := File{Path: rel}
		if f == nil || f.prog == nil {
			wrel, _ := p.ix.workspaceRel(abs)
			entry.Err = errors.New(strings.Join(p.problems[wrel], "; "))
			out = append(out, entry)
			continue
		}
		f.docPath = rel
		p.files[rel] = f
		out = append(out, entry)
	}
	for i := range out {
		if f := p.files[out[i].Path]; f != nil {
			out[i].Imports = p.imports(f)
		}
	}
	return out, nil
}

// imports resolves f's top-level imports to workspace files.
func (p *Project) imports(f *file) []Import {
	seen := map[string]bool{}
	var out []Import
	for _, st := range f.prog.Stmts {
		imp, ok := st.(*ast.ImportStmt)
		if !ok {
			continue
		}
		m := p.ix.resolve(f, imp.Path)
		if m.file == nil || seen[m.file.rel] {
			continue
		}
		seen[m.file.rel] = true
		out = append(out, Import{Path: m.file.rel, Document: m.file.docPath})
	}
	slices.SortFunc(out, func(a, b Import) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// Exports describes what the workspace file at rel, a workspace-relative slash path,
// lets an importer bind: its `namespace` line and each exported declaration's kind
// and name, in source order. A file Files did not reach, or that does not parse,
// exports nothing.
func (p *Project) Exports(rel string) []string {
	f := p.ix.files[filepath.Join(p.ix.workspace, filepath.FromSlash(rel))]
	if f == nil || f.prog == nil {
		return nil
	}
	var out []string
	if ns := f.namespace(); ns != nil {
		out = append(out, "namespace "+strings.Join(ns, `\`))
	}
	for _, d := range f.order {
		if d.exported {
			out = append(out, fmt.Sprintf("%d %s", d.kind, d.name))
		}
	}
	return out
}

// Document builds the SCIP document of the file at path, a project-relative path Files
// listed with no error, and returns every problem reported about that file: a
// construct whose position could not be recovered and so was left out.
func (p *Project) Document(path string) (*scip.Document, []string, error) {
	f := p.files[path]
	if f == nil {
		return nil, nil, fmt.Errorf("scipbuzz: %s is not a parsed document of this project", path)
	}
	doc := p.ix.document(f)
	return doc, slices.Clone(p.problems[f.rel]), nil
}
