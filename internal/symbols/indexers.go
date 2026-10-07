package symbols

import (
	"cmp"
	"fmt"
)

// Indexer describes the SCIP indexer a language's spell drives: the tool it forks and
// where to get it. It exists so a failed index run can point the user at an install
// page instead of a bare "command not found": the indexers are separate projects magus
// does not bundle, so "not installed" is a common, recoverable state.
type Indexer struct {
	Language string // canonical language (matches the spell's mgs_getLanguage)
	Tool     string // the indexer binary the scip op forks
	URL      string // install / source page
}

// indexers maps a canonical language to its SCIP indexer. Keep the keys in lockstep
// with the languages the built-in spells declare via mgs_getLanguage.
var indexers = map[string]Indexer{
	"buzz":       {Language: "buzz", Tool: "scip-buzz", URL: "https://github.com/egladman/magus/tree/main/libs/scipbuzz"},
	"go":         {Language: "go", Tool: "scip-go", URL: "https://github.com/sourcegraph/scip-go"},
	"typescript": {Language: "typescript", Tool: "scip-typescript", URL: "https://github.com/sourcegraph/scip-typescript"},
	"python":     {Language: "python", Tool: "scip-python", URL: "https://github.com/sourcegraph/scip-python"},
	"rust":       {Language: "rust", Tool: "rust-analyzer", URL: "https://github.com/rust-lang/rust-analyzer"},
}

// MissingIndexerHint is the fix for an index never built because its indexer, bin as the
// spell declares it, is not installed: it names bin, and where to get it when the
// language has a known indexer. Empty for an empty bin. The caller decides the binary is
// missing, against the PATH a run would use; this does no lookup of its own.
func MissingIndexerHint(language, bin string) string {
	if bin == "" {
		return ""
	}
	hint := fmt.Sprintf("the %s SCIP indexer (%s) is not installed", cmp.Or(language, "declared"), bin)
	if i, ok := indexers[language]; ok {
		hint += "; get it from " + i.URL
	}
	return hint
}

// InstallHint returns a one-line, actionable suffix naming the language's indexer and
// its install URL, for appending to a failed-index error. It is empty for a language
// with no known indexer, so a caller can append it unconditionally.
func InstallHint(language string) string {
	i, ok := indexers[language]
	if !ok {
		return ""
	}
	return fmt.Sprintf("the %s SCIP indexer (%s) may not be installed; get it from %s", i.Language, i.Tool, i.URL)
}
