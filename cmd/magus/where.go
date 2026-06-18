package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/interactive"
)

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
		return fmt.Errorf("magus where: conflicting pattern flags — use only one of --filter, --glob, --regex, --literal")
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	all := ws.All()
	if len(all) == 0 {
		return fmt.Errorf("no projects in workspace")
	}

	var matchFn func(string) bool
	var pat watch.IgnorePattern
	switch {
	case filterPat != "":
		p, perr := watch.ParsePattern(filterPat)
		if perr != nil {
			return fmt.Errorf("magus where: %w", perr)
		}
		pat = p
	case glob != "":
		pat = watch.IgnorePattern{Type: watch.PatternGlob, Pattern: glob}
	case regex != "":
		pat = watch.IgnorePattern{Type: watch.PatternRegex, Pattern: regex}
	case literal != "":
		pat = watch.IgnorePattern{Type: watch.PatternLiteral, Pattern: literal}
	}
	if pat.Type != "" {
		if err := watch.ValidatePattern(pat); err != nil {
			return fmt.Errorf("magus where: %w", err)
		}
		matchFn = watch.IgnorePatterns(ws.Root(), []watch.IgnorePattern{pat})
	}

	scored := interactive.ScoreProjects(all, filters)
	if len(scored) == 0 {
		files, ferr := interactive.SearchFiles(ctx, ws.Root(), filters, matchFn)
		if ferr != nil {
			return ferr
		}
		if len(files) == 0 {
			fmt.Fprintf(os.Stderr, "magus where: no projects or files match %v\n", filters)
			return errSilent{exitCode: 2}
		}
		if printAll {
			for _, f := range files {
				fmt.Println(filepath.Join(ws.Root(), f.Path))
			}
			return nil
		}
		if len(files) == 1 || (len(filters) > 0 && files[0].Score > files[1].Score) {
			fmt.Println(filepath.Join(ws.Root(), files[0].Path))
			return nil
		}
		fmt.Fprintln(os.Stderr, "magus where: ambiguous file match — candidates:")
		for _, f := range files {
			fmt.Fprintf(os.Stderr, "  %s\n", f.Path)
		}
		return errSilent{exitCode: 2}
	}

	if printAll {
		for _, s := range scored {
			fmt.Println(filepath.Join(ws.Root(), s.P.Path))
		}
		return nil
	}

	// Unique top score (or exactly one result): print and exit.
	if len(scored) == 1 || (len(filters) > 0 && scored[0].Score > scored[1].Score) {
		fmt.Println(filepath.Join(ws.Root(), scored[0].P.Path))
		return nil
	}

	// Ambiguous: list candidates on stderr and exit non-zero.
	fmt.Fprintln(os.Stderr, "magus where: ambiguous — candidates:")
	for _, s := range scored {
		fmt.Fprintf(os.Stderr, "  %s\n", s.P.Path)
	}
	return errSilent{exitCode: 2}
}
