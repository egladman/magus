package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

// whereMatch is one resolved location. Rel is carried alongside the absolute Path
// because a caller that resolved a name usually needs it back in workspace-relative
// form to hand to another magus command, and re-deriving it means knowing the root.
type whereMatch struct {
	Path string `json:"path"  yaml:"path"`
	Rel  string `json:"rel"   yaml:"rel"`
	Kind string `json:"kind"  yaml:"kind"` // "project" or "file"
}

// whereOutput is the structured view of a where resolution.
type whereOutput struct {
	Workspace string       `json:"workspace" yaml:"workspace"`
	Count     int          `json:"count"     yaml:"count"`
	Matches   []whereMatch `json:"matches"   yaml:"matches"`
}

// emitWhere renders resolved matches. The TEXT form stays deliberately bare - one
// absolute path per line and nothing else - because its whole purpose is to be
// substituted straight into another command: cd "$(magus where api)". Only the
// structured formats carry the labels.
//
// where previously ignored -o entirely: every arm printed a bare path, so `-o json`
// was accepted and silently produced text. It looked covered because
// where_formats.txtar asserted stdout '\{|/', an alternation that matches a path just
// as happily as an opening brace, so the test could not fail either way.
func emitWhere(wsRoot string, matches []whereMatch) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, whereOutput{
			Workspace: wsRoot, Count: len(matches), Matches: matches,
		})
	}
	for _, m := range matches {
		fmt.Println(m.Path)
	}
	return nil
}

// normalizeFilters canonicalises the path-SHAPED filters to the workspace-relative slash
// form both matchers compare against, so a filter a human tab-completed ("./cmd/x") or
// copied out of an editor (an absolute path) reaches what a bare "cmd/x" reaches. It is
// the same normalization `magus query` applies to a path-shaped term.
//
// The result is a copy: the filters the user typed are what the no-match error reports.
func normalizeFilters(filters []string, wsRoot string) []string {
	out := slices.Clone(filters)
	for i, f := range out {
		if norm, ok := file.NormalizeWorkspacePath(f, wsRoot); ok {
			out[i] = norm
		}
	}
	return out
}

// whereCmd fuzzy-matches a project and prints its absolute path. On ambiguity, lists candidates and exits 2.
func whereCmd(ctx context.Context, root string, args []string) error {
	var printAll bool
	var filterPat, glob, regex, literal string
	filters, err := cmdParse("where", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&printAll, "all", false, "Print all matching paths to stdout; do not error on ambiguity")
		fs.BoolVar(&printAll, "A", false, "Short for --all")
		fs.StringVar(&filterPat, "filter", "", "Restrict file search by pattern. Form: type=<glob|regex|literal>,pattern=<value>")
		fs.StringVar(&glob, "glob", "", "Restrict file search to paths matching a doublestar glob (shorthand for --filter type=glob,...)")
		fs.StringVar(&regex, "regex", "", "Restrict file search to paths matching a Go regexp (shorthand for --filter type=regex,...)")
		fs.StringVar(&literal, "literal", "", "Restrict file search to paths containing this exact segment (shorthand for --filter type=literal,...)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus where [flags] [filter...]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the absolute path of a project to stdout.")
			fmt.Fprintln(os.Stderr, "Filters are AND-combined substrings; leaf-anchored longest match wins.")
			fmt.Fprintln(os.Stderr, "Prints the path and exits 0 on a unique match; exits 2 on ambiguity.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "When no project matches, falls back to fuzzy file search across the")
			fmt.Fprintln(os.Stderr, "workspace (well-known build/vendor dirs are skipped).")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Examples:")
			fmt.Fprintln(os.Stderr, "  cd \"$(magus where api)\"")
			fmt.Fprintln(os.Stderr, "  code \"$(magus where dash)\"")
			fmt.Fprintln(os.Stderr, "  magus where api gateway                               # AND-filter: must match both tokens")
			fmt.Fprintln(os.Stderr, "  vim \"$(magus where readme.md)\"")
			fmt.Fprintln(os.Stderr, "  magus where --all server | fzf                        # pipe ambiguous matches to fzf")
			fmt.Fprintln(os.Stderr, "  magus where --glob '**/*.go'                          # only Go files")
			fmt.Fprintln(os.Stderr, "  magus where --literal Dockerfile                      # exact filename segment")
			fmt.Fprintln(os.Stderr, "  magus where --regex '_test\\.go$'                     # test files")
			fmt.Fprintln(os.Stderr, "  magus where --filter type=glob,pattern='**/*.go'      # equivalent long form")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	// Exactly one pattern flag may be set.
	patternCount := 0
	for _, v := range []string{filterPat, glob, regex, literal} {
		if v != "" {
			patternCount++
		}
	}
	if patternCount > 1 {
		return fmt.Errorf("magus where: conflicting pattern flags; use only one of --filter, --glob, --regex, --literal")
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	all := ws.All()
	if len(all) == 0 {
		return fmt.Errorf("magus where: no projects in workspace (a project is a directory with a magusfile.buzz declaring magus\\project); run `%s` to bootstrap one", hint.Init)
	}

	var matchFn func(string) bool
	var pat types.IgnorePattern
	switch {
	case filterPat != "":
		p, perr := watch.ParsePattern(filterPat)
		if perr != nil {
			return fmt.Errorf("magus where: %w", perr)
		}
		pat = p
	case glob != "":
		pat = types.IgnorePattern{Type: types.PatternGlob, Pattern: glob}
	case regex != "":
		pat = types.IgnorePattern{Type: types.PatternRegex, Pattern: regex}
	case literal != "":
		pat = types.IgnorePattern{Type: types.PatternLiteral, Pattern: literal}
	}
	if pat.Type != "" {
		if err := watch.ValidatePattern(pat); err != nil {
			return fmt.Errorf("magus where: %w", err)
		}
		matchFn = watch.IgnorePatterns(ws.Root(), []types.IgnorePattern{pat})
	}

	match := normalizeFilters(filters, ws.Root())

	scored := interactive.ScoreProjects(all, match)
	if len(scored) == 0 {
		files, ferr := interactive.SearchFiles(ctx, ws.Root(), match, matchFn)
		if ferr != nil {
			return ferr
		}
		if len(files) == 0 {
			fmt.Fprintf(os.Stderr, "magus where: no projects or files match %v\n", filters)
			return errSilent{exitCode: 2}
		}
		if printAll {
			matches := make([]whereMatch, 0, len(files))
			for _, f := range files {
				matches = append(matches, whereMatch{
					Path: filepath.Join(ws.Root(), f.Path), Rel: f.Path, Kind: "file",
				})
			}
			return emitWhere(ws.Root(), matches)
		}
		if len(files) == 1 || (len(filters) > 0 && files[0].Score > files[1].Score) {
			return emitWhere(ws.Root(), []whereMatch{{
				Path: filepath.Join(ws.Root(), files[0].Path), Rel: files[0].Path, Kind: "file",
			}})
		}
		fmt.Fprintln(os.Stderr, "magus where: ambiguous file match - candidates:")
		for _, f := range files {
			fmt.Fprintf(os.Stderr, "  %s\n", f.Path)
		}
		return errSilent{exitCode: 2}
	}

	if printAll {
		matches := make([]whereMatch, 0, len(scored))
		for _, s := range scored {
			matches = append(matches, whereMatch{
				Path: filepath.Join(ws.Root(), s.P.Path), Rel: s.P.Path, Kind: "project",
			})
		}
		return emitWhere(ws.Root(), matches)
	}

	// Unique top score (or exactly one result): print and exit.
	if len(scored) == 1 || (len(filters) > 0 && scored[0].Score > scored[1].Score) {
		return emitWhere(ws.Root(), []whereMatch{{
			Path: filepath.Join(ws.Root(), scored[0].P.Path), Rel: scored[0].P.Path, Kind: "project",
		}})
	}

	// Ambiguous: list candidates on stderr and exit non-zero.
	fmt.Fprintln(os.Stderr, "magus where: ambiguous - candidates:")
	for _, s := range scored {
		fmt.Fprintf(os.Stderr, "  %s\n", s.P.Path)
	}
	return errSilent{exitCode: 2}
}
