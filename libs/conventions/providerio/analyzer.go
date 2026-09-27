// Package providerio reports Go source that reaches toward a CI or VCS provider
// (GitHub, GitLab, ...) from a package this rule governs: net/http client
// construction, and any import naming a provider client SDK. A CI/VCS provider is
// reached only by a Buzz op magus invokes (a provider spell, a queue provider
// script, a tools/*.buzz driver); Go opens no provider socket, so it invokes the
// Buzz op through bindings and reads the record back instead.
//
// Governed directories carry the risk: a package that already talks to a provider,
// or one a "quick fetch" could most easily grow onto. A file inside them that
// legitimately reaches the world for a reason OTHER than a provider - magus's own
// remote cache, its OCI registry, selfupdate, its own local server - is named in
// the written allowlist, each entry carrying why.
package providerio

import (
	"errors"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

// httpNames are the net/http selectors that build or hold a client: constructing
// one, issuing a request through the package-level helpers, or naming the
// package's defaults. Bare http.Request, http.ResponseWriter, http.Handler and
// http.Server are not here - a governed package may legitimately serve, and
// serving is not what this rule is about.
var httpNames = map[string]bool{
	"Client":                true,
	"Transport":             true,
	"Get":                   true,
	"Head":                  true,
	"Post":                  true,
	"PostForm":              true,
	"NewRequest":            true,
	"NewRequestWithContext": true,
	"DefaultClient":         true,
	"DefaultTransport":      true,
}

const httpMessage = "constructs an HTTP client or request: a CI or VCS provider is reached only by a Buzz op " +
	"magus invokes, never Go; if this is one of magus's OWN remote services (the cache, the OCI registry, " +
	"selfupdate, ...), add a providerio allow entry in .golangci.yml naming why"

const importMessage = "imports %q, shaped like a CI/VCS provider client library: provider calls belong in Buzz, " +
	"not Go; if this is not provider I/O, add a providerio allow entry in .golangci.yml naming why"

// AllowEntry exempts one file, module-relative, from this rule. Reason is
// required: an allowlist with no reason is a place violations go to hide.
type AllowEntry struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the import path [Options.Dirs] and [AllowEntry.File] are relative
	// to.
	Module string `json:"module"`

	// Dirs are the module-relative directories this rule governs, matched by
	// prefix at a path segment boundary: "internal/queue" governs
	// internal/queue/cache.go and internal/queue/provider/host.go alike.
	Dirs []string `json:"dirs"`

	// Allow exempts individual files inside [Options.Dirs] that reach the world
	// for a reason other than a provider.
	Allow []AllowEntry `json:"allow"`

	// ProviderImports are import-path substrings, matched anywhere in the
	// module (not only inside Dirs), that name a CI/VCS provider client SDK.
	// Empty until one is added to go.mod; the rule still governs the day one is.
	ProviderImports []string `json:"provider-imports"`
}

// New returns the analyzer configured by opts, erroring on an unreasoned or
// missing allow entry.
func New(opts Options) (*analysis.Analyzer, error) {
	if len(opts.Dirs) == 0 {
		return nil, errors.New("providerio: dirs is required: with none, the rule governs nothing")
	}
	allow := make(map[string]string, len(opts.Allow))
	for _, a := range opts.Allow {
		if a.File == "" || a.Reason == "" {
			return nil, fmt.Errorf("providerio: allow entry %+v needs both file and reason", a)
		}
		allow[a.File] = a.Reason
	}
	if err := source.InModule("providerio", opts.Module, func(root string) error {
		for _, a := range opts.Allow {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(a.File))); err != nil {
				return fmt.Errorf("providerio: allow file %q: %w; the code it exempted moved, fix the setting", a.File, err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: "providerio",
		Doc:  "report Go source outside an allowlist that constructs an HTTP client or imports a CI/VCS provider SDK",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts, allow) },
	}, nil
}

func run(pass *analysis.Pass, opts Options, allow map[string]string) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	for _, f := range files {
		if source.IsTest(pass, f) {
			continue
		}
		rel, ok := source.Rel(pass, opts.Module, f)
		if !ok {
			rel, _ = source.Rel(pass, "", f)
		}
		if _, exempt := allow[rel]; exempt {
			continue
		}
		checkImports(pass, f, opts.ProviderImports)
		if !governed(rel, opts.Dirs) {
			continue
		}
		checkHTTP(pass, f)
	}
	return nil
}

// governed reports whether rel sits inside one of dirs, at a path segment
// boundary rather than a bare string prefix: "internal/queue" governs
// "internal/queue/cache.go" but not "internal/queueing/x.go".
func governed(rel string, dirs []string) bool {
	for _, d := range dirs {
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

// checkImports reports an import whose path names a configured provider SDK
// substring, wherever the file sits: an accidental provider dependency is worth
// catching everywhere it could land, not only in the governed directories.
func checkImports(pass *analysis.Pass, f *ast.File, providerImports []string) {
	if len(providerImports) == 0 {
		return
	}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		for _, sub := range providerImports {
			if strings.Contains(path, sub) {
				pass.Reportf(imp.Pos(), importMessage, path)
				break
			}
		}
	}
}

// checkHTTP reports every net/http selector in [httpNames], resolved by the
// import's local name (its alias if one is given) rather than by type
// information: a package that imports net/http under any other name is not
// this repository's practice, and matching by identifier keeps the rule
// readable at the site it fires.
func checkHTTP(pass *analysis.Pass, f *ast.File) {
	alias, ok := httpLocalName(f)
	if !ok {
		return
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if ok && id.Name == alias && httpNames[sel.Sel.Name] {
			pass.Reportf(sel.Pos(), "%s", httpMessage)
		}
		return true
	})
}

// httpLocalName returns the name net/http is bound to in f, or false when f does
// not import it (or imports it blank or dot, which this rule does not follow).
func httpLocalName(f *ast.File) (string, bool) {
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "net/http" {
			continue
		}
		if imp.Name == nil {
			return "http", true
		}
		if imp.Name.Name == "_" || imp.Name.Name == "." {
			return "", false
		}
		return imp.Name.Name, true
	}
	return "", false
}
