// Package scipbuzz writes a SCIP index for the Buzz files of a project.
//
// It reads Buzz statically: files are parsed with gopherbuzz and never checked or
// run, so indexing cannot execute an imported module. Names are bound by the
// indexer's own scope walk, and identifier positions are recovered from the token
// stream, since the gopherbuzz AST keeps neither.
package scipbuzz

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// Version is this indexer's version, reported in Metadata.ToolInfo and by
// `scip-buzz --version`.
const Version = "0.1.0"

// Options configures Index.
type Options struct {
	// ProjectRoot is the directory whose .buzz files are indexed, and the root that
	// Document.relative_path is relative to. It is required.
	ProjectRoot string
	// WorkspaceRoot is the root symbol paths are relative to, so two projects that
	// reference one file agree on its symbols. Empty means the nearest directory at
	// or above ProjectRoot holding magus.yaml, else ProjectRoot. It must contain
	// ProjectRoot.
	WorkspaceRoot string
	// Warn, when set, receives a line for each file that does not parse and each
	// construct whose position could not be recovered exactly; Index leaves those
	// out rather than emit a range that is wrong.
	Warn func(msg string)
}

// Index reads every .buzz file under the project root and returns its SCIP index.
// Discovery skips dot-directories, testdata, node_modules and vendor, and any
// subdirectory holding its own magusfile.buzz. A file that does not parse is left
// out and reported through Options.Warn; only an unreadable project fails the call.
//
// The result is canonical and names no absolute path: indexing one tree twice, or
// from two checkouts, gives equal bytes. Metadata.ProjectRoot is left empty for
// that reason; documents are relative to the indexed directory.
func Index(ctx context.Context, opts Options) (*scip.Index, error) {
	if opts.ProjectRoot == "" {
		return nil, errors.New("scipbuzz: no project root")
	}
	project, err := filepath.Abs(opts.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("scipbuzz: project root: %w", err)
	}
	workspace := opts.WorkspaceRoot
	if workspace == "" {
		workspace = findWorkspaceRoot(project)
	}
	if workspace, err = filepath.Abs(workspace); err != nil {
		return nil, fmt.Errorf("scipbuzz: workspace root: %w", err)
	}
	ix := &indexer{
		project:   project,
		workspace: workspace,
		onWarn:    opts.Warn,
		files:     map[string]*file{},
		external:  map[string]*scip.SymbolInformation{},
	}
	if _, ok := ix.workspaceRel(project); !ok {
		return nil, fmt.Errorf("scipbuzz: project root %s is outside workspace root %s", project, workspace)
	}
	rels, err := discover(project)
	if err != nil {
		return nil, fmt.Errorf("discover .buzz files: %w", err)
	}

	var docs []*file
	for _, rel := range rels {
		f := ix.load(filepath.Join(project, filepath.FromSlash(rel)))
		if f == nil || f.prog == nil {
			continue
		}
		f.docPath = rel
		docs = append(docs, f)
	}
	idx := &scip.Index{
		Metadata: &scip.Metadata{
			ToolInfo:             &scip.ToolInfo{Name: "scip-buzz", Version: Version},
			TextDocumentEncoding: scip.TextEncoding_UTF8,
		},
	}
	for _, f := range docs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		idx.Documents = append(idx.Documents, ix.document(f))
	}
	defined := map[string]bool{}
	for _, f := range docs {
		for _, d := range f.decls {
			defined[d.symbol] = true
		}
	}
	for sym, info := range ix.external {
		if !defined[sym] {
			idx.ExternalSymbols = append(idx.ExternalSymbols, info)
		}
	}
	idx.ExternalSymbols = scip.CanonicalizeSymbols(idx.ExternalSymbols)
	return idx, nil
}

// Write encodes idx in the SCIP protobuf wire format.
func Write(w io.Writer, idx *scip.Index) error {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(idx)
	if err != nil {
		return fmt.Errorf("scipbuzz: encode index: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("scipbuzz: write index: %w", err)
	}
	return nil
}

// indexer holds what one Index call has read.
type indexer struct {
	project, workspace string
	onWarn             func(msg string)
	// files caches every file read, documents and import targets alike, by
	// absolute path.
	files map[string]*file
	// external holds SymbolInformation for each global symbol a document
	// references; Index keeps those no document defines.
	external map[string]*scip.SymbolInformation
}

func (ix *indexer) warn(format string, args ...any) {
	if ix.onWarn != nil {
		ix.onWarn(fmt.Sprintf(format, args...))
	}
}

// load reads and parses the workspace file at abs, once. It returns nil when the
// file cannot be read or lies outside the workspace; a file that does not parse
// comes back with no program and no declarations.
func (ix *indexer) load(abs string) *file {
	if f, ok := ix.files[abs]; ok {
		return f
	}
	rel, ok := ix.workspaceRel(abs)
	if !ok {
		return nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		ix.warn("%s: %v", rel, err)
		ix.files[abs] = nil
		return nil
	}
	f := &file{rel: rel, abs: abs, decls: map[string]*decl{}}
	ix.files[abs] = f
	if err := f.parse(string(data)); err != nil {
		ix.warn("%s: %v", rel, err)
		return f
	}
	for _, problem := range f.collectDecls() {
		ix.warn("%s: %s", rel, problem)
	}
	return f
}

// noteDecl records SymbolInformation for a referenced declaration, kept as an
// external symbol when no document of this index defines it.
func (ix *indexer) noteDecl(d *decl) {
	if _, ok := ix.external[d.symbol]; !ok {
		ix.external[d.symbol] = d.info()
	}
}

func (ix *indexer) noteHost(symbol, name string, kind scip.SymbolInformation_Kind) {
	if _, ok := ix.external[symbol]; !ok {
		ix.external[symbol] = &scip.SymbolInformation{Symbol: symbol, DisplayName: name, Kind: kind}
	}
}

// document builds the SCIP document for f.
func (ix *indexer) document(f *file) *scip.Document {
	w := newWalker(ix, f)
	w.walkFile()
	doc := &scip.Document{
		Language:         "buzz",
		RelativePath:     f.docPath,
		PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences:      w.occs,
	}
	for _, d := range f.order {
		doc.Symbols = append(doc.Symbols, d.info())
	}
	doc.Symbols = append(doc.Symbols, w.locals...)
	return scip.CanonicalizeDocument(doc)
}
