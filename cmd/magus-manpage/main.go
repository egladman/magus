// Command magus-manpage generates magus man pages from the CLI registry.
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
// Both render from the same internal/clispec registry; they are independent
// serializers, not a conversion of one format into the other.
//
// Usage:
//
//	go run ./cmd/magus-manpage [-format roff] [-out manpage] [-date ""] [-version dev]
//	go run ./cmd/magus-manpage  -format md   [-out docs/reference/manpage]
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	iclispec "github.com/egladman/magus/internal/clispec"
	"github.com/egladman/magus/internal/config"
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

	switch *format {
	case "roff":
		if *out == "" {
			*out = "manpage"
		}
		genRoff(*out, *date, *ver)
	case "md":
		if *out == "" {
			*out = "docs/reference/manpage"
		}
		genMD(*out)
	default:
		fatalf("unknown -format %q; want roff or md", *format)
	}
}

func genRoff(outDir, date, ver string) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("mkdir %s: %v", outDir, err)
	}
	pages := iclispec.RoffPages(date, ver)
	wrote := make(map[string]bool, len(pages))
	for _, page := range pages {
		if err := writeFile(outDir, page.Name, page.Content); err != nil {
			fatalf("%s: %v", page.Name, err)
		}
		wrote[page.Name] = true
	}
	pruned, err := prune(outDir, ".1", wrote)
	if err != nil {
		fatalf("prune %s: %v", outDir, err)
	}
	fmt.Fprintf(os.Stderr, "magus-manpage: wrote %d .1 file(s) to %s, pruned %d\n", len(pages), outDir, pruned)
}

func genMD(outDir string) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("mkdir %s: %v", outDir, err)
	}
	if err := writeFile(outDir, "magus.md", renderMainMD()); err != nil {
		fatalf("magus.md: %v", err)
	}
	wrote := map[string]bool{"magus.md": true}
	for _, seg := range iclispec.All {
		name := "magus-" + seg.Name + ".md"
		if err := writeFile(outDir, name, renderCommandMD(seg)); err != nil {
			fatalf("%s: %v", name, err)
		}
		wrote[name] = true
	}
	pruned, err := prune(outDir, ".md", wrote)
	if err != nil {
		fatalf("prune %s: %v", outDir, err)
	}
	fmt.Fprintf(os.Stderr, "magus-manpage: wrote %d .md file(s) to %s, pruned %d\n", len(wrote), outDir, pruned)
}

// prune deletes pages a previous run wrote that this one did not. Both output dirs
// are declared as globs, so without this a command dropped from the registry keeps
// its page forever: magus-churn.1 shipped for a command that never existed.
//
// It only ever considers names THIS generator produces - magus<ext> and
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

func renderMainMD() []byte {
	var m mdBuf
	m.frontmatter(
		"magus command",
		"Standalone build orchestrator and content-addressed cache for polyglot monorepos, with workspace-aware subcommands for build, test, lint, and inspect.",
		[]string{"cli", "magus", "build", "monorepo", "orchestrator", "cache", "workspace"},
	)
	m.h1("magus")
	m.p("magus - workspace-aware build orchestrator and content-addressed cache")

	m.h2("Synopsis")
	m.p(mdB("magus") + " [flags] " + mdEsc("<subcommand>") + " [args]")

	m.h2("Description")
	for _, para := range iclispec.SplitParas(mainDescription) {
		m.p(mdEsc(para))
	}

	m.h2("Global Flags")
	m.p(mdEsc(globalFlagsIntro))
	m.def(mdB("--root")+" "+mdI("path"), mdEsc(flagRoot))
	m.def(mdB("--config")+" "+mdI("path"), mdEsc(flagConfig))
	m.def(mdB("--output")+" "+mdI("fmt")+", "+mdB("-o")+" "+mdI("fmt"), mdEsc(flagOutput))
	m.def(mdB("--concurrency")+" "+mdI("N"), mdEsc(flagConcurrency))
	m.def(mdB("-v"), mdEsc(flagVerbose))

	m.h2("Subcommands")
	for _, seg := range iclispec.All {
		ref := fmt.Sprintf("[%s(1)](magus-%s.md)", mdB("magus-"+seg.Name), seg.Name)
		m.def(mdB(seg.Name), mdEsc(seg.Short)+". See "+ref+".")
	}

	writeEnvSectionMD(&m)
	writeFilesSectionMD(&m)
	writeSeeAlsoMD(&m, "")

	return m.bytes()
}

// writeChildFlagsMD emits an options block for every descendant declaring flags.
func writeChildFlagsMD(m *mdBuf, path string, children []iclispec.Command) {
	for _, child := range children {
		sub := path + " " + child.Name
		if child.HasFlags() {
			fs := flag.NewFlagSet(sub, flag.ContinueOnError)
			child.BindFlags(fs)
			m.h3(sub + " options")
			fs.VisitAll(func(f *flag.Flag) {
				typeName, _ := flag.UnquoteUsage(f)
				m.def(flagLabelMD(f.Name, typeName, f.DefValue), mdEsc(f.Usage))
			})
		}
		writeChildFlagsMD(m, sub, child.Children)
	}
}

func renderCommandMD(seg iclispec.Command) []byte {
	var m mdBuf
	desc := seg.Description
	if desc == "" {
		desc = seg.Short
	}
	tags := seg.Tags
	if len(tags) == 0 {
		tags = []string{"cli", "magus " + seg.Name, seg.Name}
	}
	m.frontmatter("magus "+seg.Name, desc, tags)
	pageName := "magus-" + seg.Name
	m.h1(pageName)
	m.p(mdEsc(seg.Short))

	m.h2("Synopsis")
	if sp := strings.Index(seg.Usage, " "); sp < 0 {
		m.p(mdB(seg.Usage))
	} else {
		m.p(mdB(seg.Usage[:sp]) + " " + mdEsc(strings.TrimSpace(seg.Usage[sp:])))
	}

	if seg.Long != "" {
		m.h2("Description")
		for _, para := range iclispec.SplitParas(seg.Long) {
			m.p(mdEsc(para))
		}
	}

	if seg.HasFlags() {
		fs := flag.NewFlagSet(seg.Name, flag.ContinueOnError)
		seg.BindFlags(fs)
		m.h2("Options")
		fs.VisitAll(func(f *flag.Flag) {
			typeName, _ := flag.UnquoteUsage(f)
			m.def(flagLabelMD(f.Name, typeName, f.DefValue), mdEsc(f.Usage))
		})
	}

	// Recursive, matching the roff renderer: config nests four levels deep.
	writeChildFlagsMD(&m, seg.Name, seg.Children)

	if len(seg.Children) > 0 {
		m.h2("Subcommands")
		for _, child := range seg.Children {
			m.def(mdB(child.Name), mdEsc(child.Short))
		}
	}

	if len(seg.Targets) > 0 {
		m.h2("Targets")
		for _, t := range seg.Targets {
			m.def(mdB(t.Name), mdEsc(t.Short))
		}
	}

	if len(seg.Examples) > 0 {
		m.h2("Examples")
		for _, ex := range seg.Examples {
			if ex.Comment != "" {
				m.p(mdI(ex.Comment))
			}
			m.code(ex.Command)
		}
	}

	writeSeeAlsoMD(&m, seg.Name)
	return m.bytes()
}

func writeEnvSectionMD(m *mdBuf) {
	m.h2("Environment")
	for _, d := range config.EnvVarDocs() {
		body := mdEsc(d.Desc)
		if d.Default != "" {
			body += " (default: " + mdEsc(d.Default) + ")"
		}
		if d.YAMLKey != "" {
			body += ". Equivalent magus.yaml key: " + mdB(d.YAMLKey) + "."
		}
		m.def(mdB(d.EnvVar), body)
	}
}

func writeFilesSectionMD(m *mdBuf) {
	m.h2("Files")
	m.def(mdB("magus.yaml")+", "+mdB(".magus.yaml"), mdEsc(filesConfig))
	m.def(mdB(".magus/"), mdEsc(filesCache))
}

func writeSeeAlsoMD(m *mdBuf, currentName string) {
	m.h2("See Also")
	var refs []string
	if currentName != "" {
		refs = append(refs, fmt.Sprintf("[%s(1)](magus.md)", mdB("magus")))
	}
	for _, seg := range iclispec.All {
		if seg.Name == currentName {
			continue
		}
		refs = append(refs, fmt.Sprintf("[%s(1)](magus-%s.md)", mdB("magus-"+seg.Name), seg.Name))
	}
	m.p(strings.Join(refs, ", "))
}

// flagLabelMD builds the Markdown definition-list term for a CLI flag.
func flagLabelMD(name, typeName, defValue string) string {
	prefix := "--"
	if len(name) == 1 {
		prefix = "-"
	}
	label := mdB(prefix + name)
	if typeName != "" && typeName != "bool" {
		label += " " + mdI(typeName)
	}
	if defValue != "" && defValue != "false" && defValue != "0" && defValue != "0s" {
		label += " (default: " + mdEsc(defValue) + ")"
	}
	return label
}

// mdBuf accumulates Markdown. Each block method leaves a trailing blank line so
// the next block (heading, paragraph, definition item) is parsed independently.
type mdBuf struct{ b bytes.Buffer }

// frontmatter writes the site's YAML frontmatter block. Values are quoted only
// when they contain a colon (YAML would otherwise parse them as a nested
// mapping). Tags render as a flow sequence.
func (m *mdBuf) frontmatter(title, description string, tags []string) {
	m.b.WriteString("---\n")
	fmt.Fprintf(&m.b, "title: %s\n", yamlScalar(title))
	fmt.Fprintf(&m.b, "description: %s\n", yamlScalar(description))
	m.b.WriteString("tags: [")
	for i, t := range tags {
		if i > 0 {
			m.b.WriteString(", ")
		}
		m.b.WriteString(yamlScalar(t))
	}
	m.b.WriteString("]\n---\n\n")
}

// yamlScalar quotes a value only when needed (colon, leading/trailing space,
// or a quote character). Bare scalars stay unquoted to match the rest of the
// site's frontmatter style.
func yamlScalar(s string) string {
	needsQuote := strings.ContainsAny(s, ":\"'") || (len(s) > 0 && (s[0] == ' ' || s[len(s)-1] == ' '))
	if !needsQuote {
		return s
	}
	return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\""
}

func (m *mdBuf) h1(s string) { fmt.Fprintf(&m.b, "# %s\n\n", s) }
func (m *mdBuf) h2(s string) { fmt.Fprintf(&m.b, "## %s\n\n", s) }
func (m *mdBuf) h3(s string) { fmt.Fprintf(&m.b, "### %s\n\n", s) }
func (m *mdBuf) p(s string)  { m.b.WriteString(s + "\n\n") }

// def writes a PHP-Markdown definition-list item (term + ": " body), which the
// DefinitionList extension renders to <dt>/<dd>.
func (m *mdBuf) def(term, body string) { fmt.Fprintf(&m.b, "%s\n: %s\n\n", term, body) }

func (m *mdBuf) code(lines ...string) {
	// Example blocks are shell command lines; tag them so MD040 (fenced-code
	// language) is satisfied and renderers can highlight them.
	m.b.WriteString("```sh\n")
	for _, l := range lines {
		m.b.WriteString(l + "\n")
	}
	m.b.WriteString("```\n\n")
}

func (m *mdBuf) bytes() []byte { return m.b.Bytes() }

// mdEscaper neutralizes the Markdown metacharacters that appear in CLI prose and
// flag text. Raw < > would be dropped as HTML; backtick/asterisk/backslash would
// trigger code/emphasis. Underscores are left alone (GFM ignores intra-word _).
var mdEscaper = strings.NewReplacer(
	`\`, `\\`,
	"`", "\\`",
	"*", `\*`,
	"<", `\<`,
	">", `\>`,
)

func mdEsc(s string) string { return mdEscaper.Replace(s) }
func mdB(s string) string   { return "**" + mdEsc(s) + "**" }
func mdI(s string) string   { return "*" + mdEsc(s) + "*" }

// Shared prose, rendered by both the roff and Markdown serializers.
const (
	mainDescription = `magus is a standalone build orchestrator and content-addressed cache for
multi-language monorepos, and an evolution of Mage. It provides workspace-aware
subcommands for building, testing, linting, and inspecting projects without
requiring Mage to be installed.

magus reads optional configuration from magus.yaml (XDG, workspace root, or
CWD) and MAGUS_* environment variables. All configuration can be overridden
with CLI flags.`

	globalFlagsIntro = `Global flags are accepted by every subcommand and may appear before or
after the subcommand word. Last-write-wins, matching kubectl conventions.`

	flagRoot        = `Workspace root. Default: walk up from cwd until go.mod is found. Must precede the subcommand.`
	flagConfig      = `Config file path. Default: search for magus.yaml in CWD, workspace root, and $XDG_CONFIG_HOME/magus/. Must precede the subcommand.`
	flagOutput      = `Output format: text (default), json, yaml, name, jsonl, or template[=<go-template>]. Honored by subcommands that emit structured data. A template body renders a Go text/template over the same value -o json emits (field names are the json keys); a bare -o template with no body lists that output's fields instead of rendering - the json keys usable in -o json and -o template, with each field's type and doc.`
	flagConcurrency = `Maximum number of concurrent build steps. 0 means use the configured value (or MAGUS_CONCURRENCY, or min(NumCPU,8)).`
	flagVerbose     = `Increase log verbosity. Repeat for more detail (-v, -vv, -vvv).`

	filesConfig = `Configuration file. Searched in CWD, workspace root, and
$XDG_CONFIG_HOME/magus/ in ascending priority order. Both plain and
dot-prefixed names are accepted; having both in the same directory is an error.`
	filesCache = `Content-addressed build cache in the workspace root. Override with
MAGUS_CACHE_DIR.`
)

func writeFile(dir, name string, content []byte) error {
	return os.WriteFile(filepath.Join(dir, name), content, 0o644)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "magus-manpage: "+format+"\n", args...)
	os.Exit(1)
}
