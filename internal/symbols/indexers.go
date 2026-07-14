package symbols

import "fmt"

// Indexer describes the SCIP indexer a language's spell drives: the tool it forks and
// where to get it. It exists so a failed index run can point the user at an install
// page instead of a bare "command not found" - the indexers are separate projects magus
// does not bundle, so "not installed" is a common, recoverable state.
type Indexer struct {
	Language string // canonical language (matches the spell's mgs_getLanguage)
	Tool     string // the indexer binary the scip op forks
	URL      string // install / source page
}

// indexers maps a canonical language to its SCIP indexer. Keep the keys in lockstep
// with the languages the built-in spells declare via mgs_getLanguage.
var indexers = map[string]Indexer{
	"go":         {Language: "go", Tool: "scip-go", URL: "https://github.com/sourcegraph/scip-go"},
	"typescript": {Language: "typescript", Tool: "scip-typescript", URL: "https://github.com/sourcegraph/scip-typescript"},
	"python":     {Language: "python", Tool: "scip-python", URL: "https://github.com/sourcegraph/scip-python"},
	"rust":       {Language: "rust", Tool: "rust-analyzer", URL: "https://github.com/rust-lang/rust-analyzer"},
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
