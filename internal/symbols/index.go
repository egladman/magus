package symbols

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// The symbol index is a build artifact, never a source file: it lives under the magus
// cache dir, not the working tree. A spell declares its indexer with
// mgs_getSymbolIndexer (see spells.SymbolIndexer); when magus runs it, it hands the
// indexer the destination via the IndexEnvVar environment variable, so the command
// writes straight into the cache and the tree stays clean. The knowledge graph reads
// the same path back. The run and ingestion agree by both calling IndexPath with the
// same (cacheDir, projectAbsDir).
const (
	// IndexEnvVar names the environment variable magus sets to the index's cache
	// destination when running a declared symbol indexer, which writes its index there.
	IndexEnvVar = "MAGUS_SYMBOL_INDEX"
	// indexFileName is the basename of every project's cached SCIP index.
	indexFileName = "index.scip"
)

// IndexPath returns the absolute path of a project's cached SCIP index:
// <cacheDir>/symbols/<hash>/index.scip, where <hash> is derived from the project's
// absolute directory. Keying on a hash of the abs dir (rather than the workspace path)
// lets the op-run side (which knows only the project dir) and the ingestion side
// (which joins root and the project path) compute an identical location without either
// re-deriving the other's view. projectAbsDir is cleaned first so trivially different
// spellings of the same dir map to one index.
func IndexPath(cacheDir, projectAbsDir string) string {
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
	return filepath.Join(cacheDir, "symbols", hex.EncodeToString(sum[:8]), indexFileName)
}

// FingerprintBodies sets each symbol's BodyDigest from the definition lines under root,
// reading every defining file once. A symbol whose file or lines cannot be read keeps an
// empty digest, which a comparison must read as unknown rather than as unchanged.
//
// It reads the working tree, not the index, so an index built before the file was edited
// fingerprints lines that may no longer hold the symbol. That is the staleness the index
// already carries for Source, and `magus graph build` clears both at once.
func FingerprintBodies(root string, syms []types.KnowledgeSymbol) {
	lines := map[string][][]byte{}
	for i := range syms {
		path, lineStr, ok := strings.Cut(syms[i].Source, ":")
		if !ok {
			continue
		}
		start, err := strconv.Atoi(lineStr)
		if err != nil || start < 1 {
			continue
		}
		file, seen := lines[path]
		if !seen {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if err == nil {
				file = bytes.Split(data, []byte("\n"))
			}
			lines[path] = file
		}
		end := max(start, syms[i].DefEndLine)
		if end > len(file) {
			continue
		}
		sum := sha256.Sum256(bytes.Join(file[start-1:end], []byte("\n")))
		syms[i].BodyDigest = hex.EncodeToString(sum[:8])
	}
}
