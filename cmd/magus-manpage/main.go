// Command magus-manpage renders magus's man pages from its command tree.
//
// Two modes, selected by -format:
//
//	roff (default) - groff_man(7) .1 source for `man`, written to -out
//	                 (default: manpage/). A distribution artifact, owned at the
//	                 repo root and shipped for the `man` command.
//	md             - Markdown for the docs site, written to -out
//	                 (default: docs/reference/manpage/). Docs-site content, owned by the docs
//	                 project (docs/magusfile.buzz), which renders these to HTML like
//	                 any other doc.
//
// Usage:
//
//	go run ./cmd/magus-manpage [-format roff] [-out manpage] [-date ""] [-version dev]
//	go run ./cmd/magus-manpage  -format md   [-out docs/reference/manpage]
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/cmdline/generate"
)

func main() {
	format := flag.String("format", "roff", "output format: roff or md")
	out := flag.String("out", "", "output directory (default: manpage for roff, docs/reference/manpage for md)")
	date := flag.String("date", "", "date for .TH header; empty omits it (roff mode)")
	// The committed man pages carry no version: baking one turns every release bump
	// into spurious churn across all pages. The .TH source field stays a neutral
	// "magus" by default; a release build passes -version vX.Y.Z to stamp the copies
	// it ships, which are never committed.
	ver := flag.String("version", "", "version for the .TH source field, e.g. v0.2.0; empty (the default) stays version-neutral for committed pages (roff mode)")
	flag.Parse()

	root := commands.Root
	root.Env = config.EnvVars()
	var pages []generate.Page
	var ext string
	switch *format {
	case "roff":
		if *out == "" {
			*out = "manpage"
		}
		pages, ext = generate.Roff(root, *date, *ver), ".1"
	case "md":
		if *out == "" {
			*out = "docs/reference/manpage"
		}
		pages, ext = generate.Markdown(root, "internal/commands/registry.go"), ".md"
	default:
		fatalf("unknown -format %q; want roff or md", *format)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatalf("mkdir %s: %v", *out, err)
	}
	wrote := make(map[string]bool, len(pages))
	for _, page := range pages {
		if err := os.WriteFile(filepath.Join(*out, page.Name), page.Content, 0o644); err != nil {
			fatalf("%s: %v", page.Name, err)
		}
		wrote[page.Name] = true
	}
	pruned, err := prune(*out, ext, wrote)
	if err != nil {
		fatalf("prune %s: %v", *out, err)
	}
	fmt.Fprintf(os.Stderr, "magus-manpage: wrote %d %s file(s) to %s, pruned %d\n", len(pages), ext, *out, pruned)
}

// prune deletes pages a previous run wrote that this one did not. Both output dirs
// are declared as globs, so without this a command dropped from the registry keeps
// its page forever: magus-churn.1 shipped for a command that never existed.
//
// It only ever considers names THIS generator produces: magus<ext> and
// magus-<command><ext>. -out is a free-form flag, so an unconfined glob would make a
// mistyped path destructive: `-format md -out .` at the repo root would delete
// README.md and CHANGELOG.md. Confined this way a wrong -out deletes nothing.
//
// keep empty means the page set is empty, which is a bug upstream rather than an
// instruction to empty the directory, so it prunes nothing.
func prune(outDir, ext string, keep map[string]bool) (int, error) {
	if len(keep) == 0 {
		return 0, nil
	}
	matches, err := filepath.Glob(filepath.Join(outDir, "magus*"+ext))
	if err != nil {
		return 0, fmt.Errorf("glob %s in %s: %w", ext, outDir, err)
	}
	n := 0
	var errs []error
	for _, path := range matches {
		name := filepath.Base(path)
		if keep[name] || !generatedPage(name, ext) {
			continue
		}
		// A concurrent generate may have removed it already; that is the state we want.
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// generatedPage reports whether name is one this generator could have written, so a
// file that merely shares the prefix (magusfile.md) is never a deletion candidate.
func generatedPage(name, ext string) bool {
	return name == "magus"+ext || strings.HasPrefix(name, "magus-") && strings.HasSuffix(name, ext)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "magus-manpage: "+format+"\n", args...)
	os.Exit(1)
}
