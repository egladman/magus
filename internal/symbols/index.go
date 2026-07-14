package symbols

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// The symbol index is a build artifact, never a source file: it lives under the magus
// cache dir, not the working tree. magus reserves the op name IndexOp; when it runs that
// op it hands the indexer the destination via the IndexEnvVar environment variable, so
// the spell's command writes straight into the cache and the tree stays clean. The
// knowledge graph reads the same path back. Op-run and ingestion agree by both calling
// IndexPath with the same (cacheDir, projectAbsDir).
const (
	// IndexOp is the reserved per-language op name that runs a SCIP indexer. A spell
	// that exposes it is symbol-capable; magus injects IndexEnvVar into its run.
	IndexOp = "scip"
	// IndexEnvVar names the environment variable magus sets to the index's cache
	// destination when running an IndexOp. The spell op writes its index there.
	IndexEnvVar = "MAGUS_SYMBOL_INDEX"
	// indexFileName is the basename of every project's cached SCIP index.
	indexFileName = "index.scip"
)

// IndexPath returns the absolute path of a project's cached SCIP index:
// <cacheDir>/symbols/<hash>/index.scip, where <hash> is derived from the project's
// absolute directory. Keying on a hash of the abs dir (rather than the workspace path)
// lets the op-run side - which knows only the project dir - and the ingestion side -
// which joins root and the project path - compute an identical location without either
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
