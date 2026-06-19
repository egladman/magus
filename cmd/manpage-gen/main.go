// Command manpage-gen generates magus man pages from the CLI registry.
//
// Two modes, selected by -format:
//
//	roff (default) — groff_man(7) .1 source for `man`, written to -out
//	                 (default: manpage/gen).
//	md             — Markdown for the docs site, written to -out
//	                 (default: docs/manpage/gen). The static-site generator
//	                 (website/magusfile.buzz) renders these to HTML like any other doc.
//
// Both render from the same internal/manpage registry; they are independent
// serializers, not a conversion of one format into the other.
//
// Usage:
//
//	go run ./cmd/manpage-gen [-format roff] [-out manpage/gen] [-date ""] [-version dev]
//	go run ./cmd/manpage-gen  -format md   [-out docs/manpage/gen]
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/config"
	imanpage "github.com/egladman/magus/internal/manpage"
)

func main() {
	format := flag.String("format", "roff", "output format: roff or md")
	out := flag.String("out", "", "output directory (default: manpage/gen for roff, docs/manpage/gen for md)")
	date := flag.String("date", "", "date for .TH header; empty omits it (roff mode)")
	ver := flag.String("version", "dev", "version string for .TH source field (roff mode)")
	flag.Parse()

	switch *format {
	case "roff":
		if *out == "" {
			*out = "manpage/gen"
		}
		genRoff(*out, *date, *ver)
	case "md":
		if *out == "" {
			*out = "docs/manpage/gen"
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
	if err := writeFile(outDir, "magus.1", renderMain(date, ver)); err != nil {
		fatalf("magus.1: %v", err)
	}
	for _, seg := range imanpage.All {
		name := "magus-" + seg.Name + ".1"
		if err := writeFile(outDir, name, renderSegment(seg, date, ver)); err != nil {
			fatalf("%s: %v", name, err)
		}
	}
	total := 1 + len(imanpage.All)
	fmt.Fprintf(os.Stderr, "manpage-gen: wrote %d .1 file(s) to %s\n", total, outDir)
}

func renderMain(date, ver string) []byte {
	var buf bytes.Buffer
	w := imanpage.NewWriter(&buf)

	w.TH("magus", "1", date, "magus "+ver, "magus Manual")

	w.SH("Name")
	fmt.Fprintln(&buf, `magus \- workspace-aware build orchestrator and content-addressed cache`)

	w.SH("Synopsis")
	fmt.Fprintln(&buf, `.B magus`)
	fmt.Fprintln(&buf, `.RI [ flags ]\ <subcommand>\ [ args ]`)

	w.SH("Description")
	w.Para(mainDescription)

	w.SH("Global Flags")
	w.Para(globalFlagsIntro)
	w.TP(`\fB\-\-root\fR \fIpath\fR`, escapeMulti(flagRoot))
	w.TP(`\fB\-\-config\fR \fIpath\fR`, escapeMulti(flagConfig))
	w.TP(`\fB\-\-output\fR \fIfmt\fR, \fB\-o\fR \fIfmt\fR`, escapeMulti(flagOutput))
	w.TP(`\fB\-\-concurrency\fR \fIN\fR`, escapeMulti(flagConcurrency))
	w.TP(`\fB\-v\fR`, escapeMulti(flagVerbose))

	w.SH("Subcommands")
	w.Indent()
	for _, seg := range imanpage.All {
		manRef := fmt.Sprintf(`\fBmagus\-%s\fR(1)`, imanpage.EscapeHyphen(seg.Name))
		body := imanpage.Escape(seg.Short) + `. See ` + manRef + `.`
		w.TP(w.B(seg.Name), body)
	}
	w.Dedent()

	writeEnvSection(w, &buf)
	writeFilesSection(w)
	writeSeeAlso(w, &buf, "")

	return buf.Bytes()
}

func renderSegment(seg imanpage.Segment, date, ver string) []byte {
	var buf bytes.Buffer
	w := imanpage.NewWriter(&buf)

	pageName := "magus-" + seg.Name
	w.TH(pageName, "1", date, "magus "+ver, "magus Manual")

	w.SH("Name")
	fmt.Fprintf(&buf, "%s \\- %s\n", imanpage.EscapeHyphen(pageName), imanpage.Escape(seg.Short))

	w.SH("Synopsis")
	if sp := strings.Index(seg.Usage, " "); sp < 0 {
		fmt.Fprintln(&buf, `.B `+imanpage.EscapeHyphen(seg.Usage))
	} else {
		fmt.Fprintln(&buf, `.B `+imanpage.EscapeHyphen(seg.Usage[:sp]))
		fmt.Fprintln(&buf, `.RI "`+imanpage.Escape(strings.TrimSpace(seg.Usage[sp:]))+`"`)
	}

	if seg.Long != "" {
		w.SH("Description")
		w.Para(seg.Long)
	}

	if seg.BuildFlags != nil {
		fs := flag.NewFlagSet(seg.Name, flag.ContinueOnError)
		seg.BuildFlags(fs)
		w.SH("Options")
		w.Indent()
		fs.VisitAll(func(f *flag.Flag) {
			typeName, _ := flag.UnquoteUsage(f)
			w.TP(flagLabel(w, f.Name, typeName, f.DefValue), imanpage.Escape(f.Usage))
		})
		w.Dedent()
	}

	for _, child := range seg.Children {
		if child.BuildFlags == nil {
			continue
		}
		fs := flag.NewFlagSet(seg.Name+" "+child.Name, flag.ContinueOnError)
		child.BuildFlags(fs)
		w.SS(imanpage.EscapeHyphen(seg.Name) + " " + child.Name + " options")
		w.Indent()
		fs.VisitAll(func(f *flag.Flag) {
			typeName, _ := flag.UnquoteUsage(f)
			w.TP(flagLabel(w, f.Name, typeName, f.DefValue), imanpage.Escape(f.Usage))
		})
		w.Dedent()
	}

	if len(seg.Children) > 0 {
		w.SH("Subcommands")
		w.Indent()
		for _, child := range seg.Children {
			w.TP(w.B(child.Name), imanpage.Escape(child.Short))
		}
		w.Dedent()
	}

	if len(seg.Targets) > 0 {
		w.SH("Targets")
		w.Indent()
		for _, t := range seg.Targets {
			w.TP(w.B(t.Name), imanpage.Escape(t.Short))
		}
		w.Dedent()
	}

	if len(seg.Examples) > 0 {
		w.SH("Examples")
		for _, ex := range seg.Examples {
			if ex.Comment != "" {
				fmt.Fprintf(&buf, "\\fI%s\\fR\n", imanpage.Escape(ex.Comment))
				w.P()
			}
			w.Example(ex.Command)
			w.P()
		}
	}

	writeSeeAlso(w, &buf, seg.Name)
	return buf.Bytes()
}

func writeEnvSection(w *imanpage.Writer, _ *bytes.Buffer) {
	w.SH("Environment")
	w.Indent()
	for _, d := range config.EnvVarDocs() {
		label := `\fB` + d.EnvVar + `\fR`
		body := imanpage.Escape(d.Desc)
		if d.Default != "" {
			body += ` (default: ` + imanpage.Escape(d.Default) + `)`
		}
		if d.YAMLKey != "" {
			body += `. Equivalent magus.yaml key: ` + w.B(d.YAMLKey) + `.`
		}
		w.TP(label, body)
	}
	w.Dedent()
}

func writeFilesSection(w *imanpage.Writer) {
	w.SH("Files")
	w.TP(`\fBmagus.yaml\fR, \fB.magus.yaml\fR`, escapeMulti(filesConfig))
	w.TP(`\fB.magus\-cache/\fR`, escapeMulti(filesCache))
}

func writeSeeAlso(w *imanpage.Writer, buf *bytes.Buffer, currentName string) {
	w.SH("See Also")
	var refs []string
	if currentName != "" {
		refs = append(refs, `\fBmagus\fR(1)`)
	}
	for _, seg := range imanpage.All {
		if seg.Name == currentName {
			continue
		}
		refs = append(refs, fmt.Sprintf(`\fBmagus\-%s\fR(1)`, imanpage.EscapeHyphen(seg.Name)))
	}
	fmt.Fprintln(buf, strings.Join(refs, ",\n"))
	fmt.Fprintln(buf, `.br`)
}

// flagLabel builds the .TP label for a CLI flag.
func flagLabel(w *imanpage.Writer, name, typeName, defValue string) string {
	prefix := "--"
	if len(name) == 1 {
		prefix = "-"
	}
	label := `\fB` + imanpage.EscapeHyphen(prefix) + imanpage.EscapeHyphen(name) + `\fR`
	if typeName != "" && typeName != "bool" {
		label += ` \fI` + typeName + `\fR`
	}
	if defValue != "" && defValue != "false" && defValue != "0" && defValue != "0s" {
		label += ` (default: ` + imanpage.Escape(defValue) + `)`
	}
	_ = w // may be used for future B/I formatting
	return label
}

// escapeMulti escapes a roff body that may span several source lines.
func escapeMulti(s string) string {
	parts := imanpage.SplitParas(s)
	for i := range parts {
		parts[i] = imanpage.Escape(parts[i])
	}
	return strings.Join(parts, "\n")
}

func genMD(outDir string) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("mkdir %s: %v", outDir, err)
	}
	if err := writeFile(outDir, "magus.md", renderMainMD()); err != nil {
		fatalf("magus.md: %v", err)
	}
	for _, seg := range imanpage.All {
		name := "magus-" + seg.Name + ".md"
		if err := writeFile(outDir, name, renderSegmentMD(seg)); err != nil {
			fatalf("%s: %v", name, err)
		}
	}
	total := 1 + len(imanpage.All)
	fmt.Fprintf(os.Stderr, "manpage-gen: wrote %d .md file(s) to %s\n", total, outDir)
}

func renderMainMD() []byte {
	var m mdBuf
	m.h1("magus")
	m.p("magus - workspace-aware build orchestrator and content-addressed cache")

	m.h2("Synopsis")
	m.p(mdB("magus") + " [flags] " + mdEsc("<subcommand>") + " [args]")

	m.h2("Description")
	for _, para := range imanpage.SplitParas(mainDescription) {
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
	for _, seg := range imanpage.All {
		ref := fmt.Sprintf("[%s(1)](magus-%s.md)", mdB("magus-"+seg.Name), seg.Name)
		m.def(mdB(seg.Name), mdEsc(seg.Short)+". See "+ref+".")
	}

	writeEnvSectionMD(&m)
	writeFilesSectionMD(&m)
	writeSeeAlsoMD(&m, "")

	return m.bytes()
}

func renderSegmentMD(seg imanpage.Segment) []byte {
	var m mdBuf
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
		for _, para := range imanpage.SplitParas(seg.Long) {
			m.p(mdEsc(para))
		}
	}

	if seg.BuildFlags != nil {
		fs := flag.NewFlagSet(seg.Name, flag.ContinueOnError)
		seg.BuildFlags(fs)
		m.h2("Options")
		fs.VisitAll(func(f *flag.Flag) {
			typeName, _ := flag.UnquoteUsage(f)
			m.def(flagLabelMD(f.Name, typeName, f.DefValue), mdEsc(f.Usage))
		})
	}

	for _, child := range seg.Children {
		if child.BuildFlags == nil {
			continue
		}
		fs := flag.NewFlagSet(seg.Name+" "+child.Name, flag.ContinueOnError)
		child.BuildFlags(fs)
		m.h3(seg.Name + " " + child.Name + " options")
		fs.VisitAll(func(f *flag.Flag) {
			typeName, _ := flag.UnquoteUsage(f)
			m.def(flagLabelMD(f.Name, typeName, f.DefValue), mdEsc(f.Usage))
		})
	}

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
	m.def(mdB(".magus-cache/"), mdEsc(filesCache))
}

func writeSeeAlsoMD(m *mdBuf, currentName string) {
	m.h2("See Also")
	var refs []string
	if currentName != "" {
		refs = append(refs, fmt.Sprintf("[%s(1)](magus.md)", mdB("magus")))
	}
	for _, seg := range imanpage.All {
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

func (m *mdBuf) h1(s string) { fmt.Fprintf(&m.b, "# %s\n\n", s) }
func (m *mdBuf) h2(s string) { fmt.Fprintf(&m.b, "## %s\n\n", s) }
func (m *mdBuf) h3(s string) { fmt.Fprintf(&m.b, "### %s\n\n", s) }
func (m *mdBuf) p(s string)  { m.b.WriteString(s + "\n\n") }

// def writes a PHP-Markdown definition-list item (term + ": " body), which the
// DefinitionList extension renders to <dt>/<dd>.
func (m *mdBuf) def(term, body string) { fmt.Fprintf(&m.b, "%s\n: %s\n\n", term, body) }

func (m *mdBuf) code(lines ...string) {
	m.b.WriteString("```\n")
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
	flagOutput      = `Output format: text (default), json, yaml, name, wide, or template=<go-template>. Honoured by subcommands that emit structured data.`
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
	fmt.Fprintf(os.Stderr, "manpage-gen: "+format+"\n", args...)
	os.Exit(1)
}
