package symbols

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// The symbol index is a build artifact, never a source file: it lives under the magus
// cache dir, not the working tree. A spell declares its indexer with
// mgs_getSymbolIndexer (see spells.SymbolIndexer); when magus runs it, it hands the
// indexer the destination via the IndexEnvVar environment variable, so the command
// writes straight into the cache and the tree stays clean. The knowledge graph reads
// the same path back. The run and ingestion agree by both calling IndexPath with the
// same (cacheDir, projectAbsDir, op).
const (
	// IndexEnvVar names the environment variable magus sets to the index's cache
	// destination when running a declared symbol indexer, which writes its index there.
	IndexEnvVar = "MAGUS_SYMBOL_INDEX"
	// WorkspaceRootEnvVar names the variable magus sets to its own workspace root when
	// running a declared symbol indexer, so an indexer that writes workspace-relative
	// paths (scip-buzz) roots them where magus resolves them rather than finding a root
	// of its own.
	WorkspaceRootEnvVar = "MAGUS_WORKSPACE_ROOT"
	// indexFileName is the basename of the index the default indexer op writes.
	indexFileName = "index.scip"
)

// IndexPath returns the absolute path of the SCIP index op writes for a project:
// <cacheDir>/symbols/<hash>/<op>.scip, where <hash> is derived from the project's
// absolute directory and op is the indexer op (spells.SymbolIndexer.OpName), so each
// indexing op bound to one project has a file of its own. Keying on a hash of the abs dir
// (rather than the workspace path) lets the op-run side (which knows only the project
// dir) and the ingestion side (which joins root and the project path) compute an
// identical location without either re-deriving the other's view. projectAbsDir is
// cleaned first so trivially different spellings of the same dir map to one index.
func IndexPath(cacheDir, projectAbsDir, op string) string {
	// Canonicalize through EvalSymlinks so the op-run side (which learns the dir from the
	// run context) and the ingestion side (which joins root and project.Path) hash the
	// SAME bytes even when one spelling reaches here via a symlink (e.g. macOS /var ->
	// /private/var); a mismatch would silently write the index where ingestion never
	// looks. Best-effort: a not-yet-created dir falls back to a plain Clean.
	dir := filepath.Clean(projectAbsDir)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	sum := sha256.Sum256([]byte(dir))
	name := op + ".scip"
	if op == spells.DefaultSymbolIndexOp {
		// compat(until: the cache key schema next changes, which re-runs every index
		// anyway): the default op's index keeps the file it had before a project could
		// hold two, because every cached run stamps that path and a new one would re-run
		// the indexer.
		name = indexFileName
	}
	return filepath.Join(cacheDir, "symbols", hex.EncodeToString(sum[:8]), name)
}

// FingerprintBodies sets each symbol's BodyDigest from the definition lines under root,
// reading every defining file once, and sizes that file into SourceLines and SourceBytes,
// and reads its generated-code header into SourceGenerated, from the same read. A symbol whose file or lines cannot be read keeps an empty digest,
// which a comparison must read as unknown rather than as unchanged. A symbol with a body
// span (a DefEndLine) also gets BodyShape, BodyTokens and BranchIdents over the same
// lines, left empty when its language has no lexer. A Go namespace symbol gets the
// PackageName its file declares, and each reference to it the Alias its import line
// writes, read from the referencing file.
//
// It reads the working tree, not the index, so an index built before the file was edited
// fingerprints lines that may no longer hold the symbol. That is the staleness the index
// already carries for Source, and `magus graph build` clears both at once.
func FingerprintBodies(root string, syms []types.KnowledgeSymbol) {
	type sourceFile struct {
		lines        [][]byte
		nLines, size int
		generated    bool
	}
	files := map[string]sourceFile{}
	read := func(path string) sourceFile {
		file, seen := files[path]
		if !seen {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if err == nil {
				lines := types.SplitSourceLines(data)
				file = sourceFile{lines: lines, nLines: types.CountLines(data), size: len(data), generated: types.GeneratedSource(lines)}
			}
			files[path] = file
		}
		return file
	}
	for i := range syms {
		if syms[i].Key != "" && syms[i].Namespace == syms[i].Key {
			readImportAliases(&syms[i], func(path string) [][]byte { return read(path).lines })
		}
		path, lineStr, ok := strings.Cut(syms[i].Source, ":")
		if !ok {
			continue
		}
		start, err := strconv.Atoi(lineStr)
		if err != nil || start < 1 {
			continue
		}
		file := read(path)
		if file.lines == nil {
			continue
		}
		syms[i].SourceLines, syms[i].SourceBytes, syms[i].SourceGenerated = file.nLines, file.size, file.generated
		if syms[i].Key != "" && syms[i].Namespace == syms[i].Key && syms[i].Language == "go" {
			syms[i].PackageName = packageClause(file.lines)
		}
		if digest, ok := types.BodyDigest(file.lines, start, max(start, syms[i].DefEndLine)); ok {
			syms[i].BodyDigest = digest
		}
		if syms[i].DefEndLine < start {
			continue
		}
		if shape, tokens, ok := types.BodyShape(file.lines, start, syms[i].DefEndLine, syms[i].Language); ok {
			syms[i].BodyShape, syms[i].BodyTokens = shape, tokens
		}
		syms[i].BranchIdents = types.BranchIdents(file.lines, start, syms[i].DefEndLine, syms[i].Language)
	}
}

// readImportAliases sets each reference's Alias on a namespace symbol: the alias the first
// of its recorded lines that is an import spec writes, read from the referencing file.
func readImportAliases(sym *types.KnowledgeSymbol, lines func(path string) [][]byte) {
	if sym.Language != "go" {
		return
	}
	for i, ref := range sym.Refs {
		src := lines(ref.Path)
		for _, line := range ref.Lines {
			if alias := types.ImportAlias(src, line, sym.Language); alias != "" {
				sym.Refs[i].Alias = alias
				break
			}
		}
	}
}

// packageClause is the name the first `package` clause in lines declares, or "" for none.
func packageClause(lines [][]byte) string {
	for _, line := range lines {
		if rest, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("package ")); ok {
			if fields := bytes.Fields(rest); len(fields) > 0 {
				return string(fields[0])
			}
		}
	}
	return ""
}
