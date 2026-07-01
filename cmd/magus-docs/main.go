// Command magus-docs generates Markdown documentation for every module
// registered in the host package. Run it manually to refresh the committed docs:
//
//	go run ./cmd/magus-docs -out ./docs/modules
//
// The output under docs/modules is committed; re-run this tool after
// changing a host module's bindings to keep the docs in sync.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/host"
	"github.com/egladman/magus/std"

	// Blank imports so all module init() functions run, populating std.All().
	_ "github.com/egladman/magus/std"
)

// repoBlob is the GitHub source base for inline type links. It is pinned to the
// default branch rather than a commit hash on purpose: the committed docs embed
// these links, so a raw HEAD hash would rewrite on every commit and trip the
// `generate` drift gate (regenerated docs would never match the tree). There are
// no release tags to pin to yet; when there are, this can point at the tag.
const repoBlob = "https://github.com/egladman/magus/blob/main"

// callbackURL is the source link for the Callback type; repoRoot is the module
// root. Both are resolved in init() rather than main() so the docs the tests
// generate via renderModule match the ones `go run` writes: `go test` runs with
// the package directory as its working directory and never calls main(), so
// anything resolved there (or read via a repo-relative path) would silently
// differ under test and trip the drift gate.
var callbackURL string
var repoRoot string

func init() {
	repoRoot = findRepoRoot()
	callbackURL = sourceURL("std/module.go", "type Callback ")
}

// findRepoRoot walks up from the working directory to the directory holding go.mod
// (the module root), so source paths resolve whether the process runs from the
// repo root (go run) or a package directory (go test).
func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// moduleCategories groups the modules for the index page so the reference reads
// as sections (each an h2, so the table of contents has real entries) instead of
// one flat table. Any module not listed here is collected under "Other" so a
// newly added module is never silently dropped from the index.
var moduleCategories = []struct {
	Title   string
	Modules []string
}{
	{"Files and paths", []string{"fs", "path", "archive"}},
	{"Process and environment", []string{"os", "env", "platform"}},
	{"Text and formatting", []string{"strings", "fmt", "markdown"}},
	{"Serialization and encoding", []string{"json", "yaml", "encoding"}},
	{"Cryptography", []string{"crypto"}},
	{"Networking", []string{"http"}},
	{"Time and versioning", []string{"time", "semver", "vcs"}},
	{"Magus internals", []string{"magus", "charm"}},
}

func main() {
	outDir := flag.String("out", "docs/modules", "output directory for module docs")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "magus-docs: mkdir %s: %v\n", *outDir, err)
		os.Exit(1)
	}

	modules := std.All()
	slices.SortFunc(modules, func(a, b std.Module) int {
		return strings.Compare(a.Name, b.Name)
	})

	for _, m := range modules {
		if err := writeModule(*outDir, m); err != nil {
			fmt.Fprintf(os.Stderr, "magus-docs: %s: %v\n", m.Name, err)
			os.Exit(1)
		}
	}

	if err := writeIndex(*outDir, modules); err != nil {
		fmt.Fprintf(os.Stderr, "magus-docs: index: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "magus-docs: wrote %d module docs to %s\n", len(modules), *outDir)
}

// sourceURL builds a repoBlob link to the first line of path whose trimmed text
// starts with prefix (the definition of a host type), so a type link points at
// the definition the way godoc does. The line is resolved from the working tree
// so it stays correct if the definition moves; on any read miss it links to the
// file without a line anchor.
func sourceURL(path, prefix string) string {
	url := repoBlob + "/" + path
	data, err := os.ReadFile(filepath.Join(repoRoot, path))
	if err != nil {
		return url
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return fmt.Sprintf("%s#L%d", url, i+1)
		}
	}
	return url
}

// typeMarkdown renders a type for a table cell: a backticked code span, linked to
// its source definition when it is a host-defined type. Primitive types (string,
// int, []string, ...) are language builtins and stay unlinked, the way godoc only
// links named types.
func typeMarkdown(t std.TypeTag) string {
	name := t.GoType()
	if t == std.TypeFunc { // GoType() == "Callback"
		return fmt.Sprintf("[`%s`](%s)", name, callbackURL)
	}
	return "`" + name + "`"
}

// methodSourceLink renders a " · [source](…)" suffix linking a method to its
// Impl's definition on GitHub (godoc-style), or "" when unresolvable. It rides
// the Signature line rather than the heading: a link in the heading gets folded
// into the auto-generated slug, which would break the in-page #method anchors.
func methodSourceLink(meth std.Method) string {
	src, line := host.MethodSource(meth, repoRoot)
	if src == "" {
		return ""
	}
	url := repoBlob + "/" + src
	if line > 0 {
		url += fmt.Sprintf("#L%d", line)
	}
	return fmt.Sprintf(" · [source](%s)", url)
}

func writeModule(dir string, m std.Module) error {
	return os.WriteFile(filepath.Join(dir, m.Name+".md"), []byte(renderModule(m)), 0o644)
}

// stdlibNote is one collected footnote: a method that is also in Buzz's stdlib.
type stdlibNote struct {
	label  string // page-unique footnote label ([^label])
	method string // camelCase Buzz method name, for the footnote text
	equiv  string // the Buzz stdlib call that covers the same need
}

func renderModule(m std.Module) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", m.Name)
	if m.Doc != "" {
		fmt.Fprintf(&b, "%s\n\n", m.Doc)
	}

	// Naming convention note: each module is imported under its bare name, with
	// methods in camelCase.
	fmt.Fprintf(&b, "> **Naming convention:** import the module under its bare name "+
		"(`import \"%s\"`) and call methods in `camelCase` (`%s.someMethod`).\n\n",
		m.Name, m.Name)

	if len(m.Fields) > 0 {
		fmt.Fprintf(&b, "## Fields\n\n")
		fmt.Fprintln(&b, "| Field | Type | Description |")
		fmt.Fprintln(&b, "|-------|------|-------------|")
		for _, f := range m.Fields {
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", f.Name, typeMarkdown(f.Type), f.Doc)
		}
		b.WriteByte('\n')
	}

	// notes accumulates the Buzz-stdlib footnotes; each method that duplicates a
	// stdlib call gets a "*" marker on its name linking to an entry rendered after
	// all the methods (see the footnote block below).
	var notes []stdlibNote

	if len(m.Methods) > 0 {
		fmt.Fprintf(&b, "## Methods\n\n")
		for _, meth := range m.Methods {
			// Heading stays plain text so its auto-generated slug (the #method
			// anchor) stays clean; the source link and stdlib footnote both ride
			// the Signature line instead.
			fmt.Fprintf(&b, "### %s\n\n", meth.Name)

			if meth.Doc != "" {
				fmt.Fprintf(&b, "%s\n\n", meth.Doc)
			}

			// A footnote reference after the signature marks methods that are also
			// in Buzz's own stdlib; the Footnote extension renders it as a linked
			// superscript and gathers the notes at the foot of the page.
			sig := fmt.Sprintf("**Signature:** `%s`", host.BuzzSignature(m, meth))
			if equiv, dup := host.BuzzStdlibEquiv(m.Name, meth.Name); dup {
				label := "buzz-stdlib-" + m.Name + "-" + meth.Name
				sig += fmt.Sprintf("[^%s]", label)
				notes = append(notes, stdlibNote{label, buzzMethodName(m, meth), equiv})
			}
			sig += methodSourceLink(meth)
			fmt.Fprintf(&b, "%s\n\n", sig)
			if len(meth.Args) > 0 {
				fmt.Fprintln(&b, "| Parameter | Type | Optional | Description |")
				fmt.Fprintln(&b, "|-----------|------|----------|-------------|")
				for _, a := range meth.Args {
					opt := ""
					if a.Optional {
						opt = "yes"
					}
					fmt.Fprintf(&b, "| `%s` | %s | %s | |\n", a.Name, typeMarkdown(a.Type), opt)
				}
				fmt.Fprintln(&b)
			}
			if len(meth.Returns) > 0 {
				rets := make([]string, len(meth.Returns))
				for i, r := range meth.Returns {
					typ := r.Type.GoType()
					if r.Type == std.TypeFunc {
						typ = fmt.Sprintf("[%s](%s)", typ, callbackURL)
					}
					if r.Name != "" {
						rets[i] = r.Name + " " + typ
					} else {
						rets[i] = typ
					}
				}
				fmt.Fprintf(&b, "**Returns:** %s\n\n", strings.Join(rets, ", "))
			}
		}
	}

	// Footnote definitions for the methods that are also in Buzz's stdlib. The
	// Footnote extension moves these into an anchored section at the foot of the
	// page and links each method's marker to its note. The magus form is kept
	// because it is sandbox-aware where Buzz's bare stdlib is not.
	for _, n := range notes {
		fmt.Fprintf(&b, "[^%s]: `%s` is also in Buzz's standard library (`%s`); "+
			"the magus form is sandbox-aware.\n", n.label, n.method, n.equiv)
	}

	return b.String()
}

// buzzMethodName renders the module-qualified camelCase call (e.g. "fs.delete")
// for a footnote, matching the form used in signatures.
func buzzMethodName(m std.Module, meth std.Method) string {
	name := host.CamelCase(meth.Name)
	if meth.BuzzName != "" {
		name = meth.BuzzName
	}
	return m.Name + "." + name
}

func writeIndex(dir string, modules []std.Module) error {
	return os.WriteFile(filepath.Join(dir, "index.md"), []byte(renderIndex(modules)), 0o644)
}

func renderIndex(modules []std.Module) string {
	byName := make(map[string]std.Module, len(modules))
	for _, m := range modules {
		byName[m.Name] = m
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Magusfile Module Reference\n\n")
	fmt.Fprintf(&b, "These are the runtime utility modules. Import each under its bare name — `import \"fs\"`, then `fs.glob(...)` — with `camelCase` methods. magus layers these host methods onto Buzz's own stdlib, so a single `import \"fs\"` (or `os`, `crypto`) carries both surfaces, and the magus forms are sandbox-aware where Buzz's bare stdlib is not. Methods that are also in Buzz's own standard library are marked with an asterisk (`*`) and a footnote on their page; either form works.\n\n")

	// Render each category as its own section so the page has real headings (and
	// thus a useful table of contents). Track what's been placed so nothing is
	// dropped.
	placed := make(map[string]bool)
	emit := func(title string, names []string) {
		var rows []std.Module
		for _, name := range names {
			if m, ok := byName[name]; ok {
				rows = append(rows, m)
				placed[name] = true
			}
		}
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(&b, "## %s\n\n", title)
		fmt.Fprintln(&b, "| Module | Description |")
		fmt.Fprintln(&b, "|--------|-------------|")
		for _, m := range rows {
			fmt.Fprintf(&b, "| [`%s`](%s.md) | %s |\n", m.Name, m.Name, m.Doc)
		}
		b.WriteByte('\n')
	}

	for _, c := range moduleCategories {
		emit(c.Title, c.Modules)
	}

	// Any module not assigned to a category lands here, so adding a module without
	// categorizing it shows up in the docs (and the drift gate) instead of vanishing.
	var leftover []string
	for _, m := range modules {
		if !placed[m.Name] {
			leftover = append(leftover, m.Name)
		}
	}
	emit("Other", leftover)

	fmt.Fprintf(&b, "## See also\n\n")
	fmt.Fprintf(&b, "- [Targets](../targets.md): the runnable units whose magusfiles call these modules.\n")
	fmt.Fprintf(&b, "- [Spells](../spells.md): language and toolchain adapters that compose these modules into operations.\n")
	fmt.Fprintf(&b, "- [Charms](../charms.md): the execution modifiers the `charm` module constructs.\n")
	fmt.Fprintf(&b, "- [Playground](../playground.html): exercise these modules live in the browser.\n")
	return b.String()
}
