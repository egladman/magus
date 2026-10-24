package generate

import (
	"bytes"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/cmdline"
)

// Markdown renders root's man pages as Markdown for a docs site: <name>.md and
// <name>-<command>.md, each opening with YAML frontmatter (title, description,
// tags) and naming source as the file it is generated from. The definition lists
// use the PHP Markdown Extra syntax ("term\n: body").
func Markdown(root cmdline.Command, source string) []Page {
	pages := make([]Page, 0, 1+len(root.Children))
	pages = append(pages, Page{Name: root.Name + ".md", Content: renderMainMD(root, source)})
	for _, command := range root.Children {
		pages = append(pages, Page{
			Name:    fmt.Sprintf("%s-%s.md", root.Name, command.Name),
			Content: renderCommandMD(root, command, source),
		})
	}
	return pages
}

func renderMainMD(root cmdline.Command, source string) []byte {
	var m mdBuf
	m.frontmatter(root.Name+" command", root.Description, source, root.Tags)
	m.h1(root.Name)
	m.p(fmt.Sprintf("%s - %s", root.Name, mdEsc(root.Short)))
	m.h2("Synopsis")
	m.p(synopsisMD(root.Usage))
	m.h2("Description")
	for _, para := range splitParas(root.Long) {
		m.p(mdEsc(para))
	}
	if len(root.Flags) > 0 {
		m.h2("Global Flags")
		writeFlagListMD(&m, root)
	}
	m.h2("Subcommands")
	for _, command := range root.Children {
		ref := fmt.Sprintf("[%s(1)](%s-%s.md)", mdB(fmt.Sprintf("%s-%s", root.Name, command.Name)), root.Name, command.Name)
		m.def(mdB(command.Name), fmt.Sprintf("%s. See %s.", mdEsc(command.Short), ref))
	}
	writeEnvSectionMD(&m, root.Env)
	writeFilesSectionMD(&m, root.Files)
	writeSeeAlsoMD(&m, root, "")
	return m.bytes()
}

func renderCommandMD(root, command cmdline.Command, source string) []byte {
	var m mdBuf
	desc := command.Description
	if desc == "" {
		desc = command.Short
	}
	tags := command.Tags
	if len(tags) == 0 {
		tags = []string{"cli", fmt.Sprintf("%s %s", root.Name, command.Name), command.Name}
	}
	m.frontmatter(fmt.Sprintf("%s %s", root.Name, command.Name), desc, source, tags)
	m.h1(fmt.Sprintf("%s-%s", root.Name, command.Name))
	m.p(mdEsc(command.Short))
	m.h2("Synopsis")
	m.p(synopsisMD(command.Usage))
	if command.Long != "" {
		m.h2("Description")
		for _, para := range splitParas(command.Long) {
			m.p(mdEsc(para))
		}
	}
	if len(command.Flags) > 0 {
		m.h2("Options")
		writeFlagListMD(&m, command)
	}
	// Recursive, matching the roff renderer.
	writeChildFlagsMD(&m, command.Name, command.Children)
	if len(command.Children) > 0 {
		m.h2("Subcommands")
		for _, child := range command.Children {
			m.def(mdB(child.Name), mdEsc(child.Short))
		}
	}
	if len(command.Operands) > 0 {
		m.h2("Operands")
		for _, o := range command.Operands {
			m.def(mdB(o.Name), mdEsc(o.Short))
		}
	}
	// Before Examples, matching the roff renderer's man(7) section order.
	writeExitStatusMD(&m, command.ExitStatus)
	if len(command.Examples) > 0 {
		m.h2("Examples")
		for _, ex := range command.Examples {
			if ex.Comment != "" {
				m.p(mdI(ex.Comment))
			}
			m.code(ex.Command)
		}
	}
	writeSeeAlsoMD(&m, root, command.Name)
	return m.bytes()
}

func synopsisMD(usage string) string {
	if sp := strings.Index(usage, " "); sp >= 0 {
		return fmt.Sprintf("%s %s", mdB(usage[:sp]), mdEsc(strings.TrimSpace(usage[sp:])))
	}
	return mdB(usage)
}

// writeChildFlagsMD emits an options block for every descendant declaring flags.
func writeChildFlagsMD(m *mdBuf, path string, children []cmdline.Command) {
	for _, child := range children {
		sub := fmt.Sprintf("%s %s", path, child.Name)
		if len(child.Flags) > 0 {
			m.h3(sub + " options")
			writeFlagListMD(m, child)
		}
		writeChildFlagsMD(m, sub, child.Children)
	}
}

func writeFlagListMD(m *mdBuf, command cmdline.Command) {
	visitFlags(command, func(f *flag.Flag) {
		typeName, usage := flag.UnquoteUsage(f)
		m.def(flagLabelMD(f.Name, typeName, f.DefValue), mdEsc(usage))
	})
}

func writeExitStatusMD(m *mdBuf, codes []cmdline.ExitCode) {
	if len(codes) == 0 {
		return
	}
	m.h2("Exit status")
	for _, code := range codes {
		m.def(mdB(strconv.Itoa(code.Code)), mdEsc(code.Meaning))
	}
}

func writeEnvSectionMD(m *mdBuf, env []cmdline.Env) {
	if len(env) == 0 {
		return
	}
	m.h2("Environment")
	for _, e := range env {
		body := mdEsc(e.Doc)
		if e.Default != "" {
			body += fmt.Sprintf(" (default: %s)", mdEsc(e.Default))
		}
		if e.Key != "" {
			body += fmt.Sprintf(". Config file key: %s.", mdB(e.Key))
		}
		m.def(mdB(e.Name), body)
	}
}

func writeFilesSectionMD(m *mdBuf, files []cmdline.File) {
	if len(files) == 0 {
		return
	}
	m.h2("Files")
	for _, f := range files {
		m.def(mdB(f.Name), mdEsc(strings.Join(splitParas(f.Doc), " ")))
	}
}

func writeSeeAlsoMD(m *mdBuf, root cmdline.Command, current string) {
	m.h2("See Also")
	var refs []string
	if current != "" {
		refs = append(refs, fmt.Sprintf("[%s(1)](%s.md)", mdB(root.Name), root.Name))
	}
	for _, command := range root.Children {
		if command.Name != current {
			refs = append(refs, fmt.Sprintf("[%s(1)](%s-%s.md)", mdB(fmt.Sprintf("%s-%s", root.Name, command.Name)), root.Name, command.Name))
		}
	}
	m.p(strings.Join(refs, ", "))
}

// flagLabelMD builds the definition-list term for a flag.
func flagLabelMD(name, typeName, defValue string) string {
	label := mdB(dashes(name) + name)
	if typeName != "" && typeName != "bool" {
		label += " " + mdI(typeName)
	}
	if defValue != "" && defValue != "false" && defValue != "0" && defValue != "0s" {
		label += fmt.Sprintf(" (default: %s)", mdEsc(defValue))
	}
	return label
}

type mdBuf struct{ b bytes.Buffer }

// frontmatter writes the YAML frontmatter block; tags render as a flow sequence.
func (m *mdBuf) frontmatter(title, description, source string, tags []string) {
	m.b.WriteString("---\n")
	fmt.Fprintf(&m.b, "title: %s\n", yamlScalar(title))
	fmt.Fprintf(&m.b, "generated_from: %s\n", yamlScalar(source))
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

// yamlScalar quotes a value only when YAML would misread it: a colon, a quote, or
// leading or trailing space.
func yamlScalar(s string) string {
	needsQuote := strings.ContainsAny(s, ":\"'") || (len(s) > 0 && (s[0] == ' ' || s[len(s)-1] == ' '))
	if !needsQuote {
		return s
	}
	return fmt.Sprintf("\"%s\"", strings.ReplaceAll(s, "\"", "\\\""))
}

func (m *mdBuf) h1(s string) { fmt.Fprintf(&m.b, "# %s\n\n", s) }
func (m *mdBuf) h2(s string) { fmt.Fprintf(&m.b, "## %s\n\n", s) }
func (m *mdBuf) h3(s string) { fmt.Fprintf(&m.b, "### %s\n\n", s) }
func (m *mdBuf) p(s string)  { m.b.WriteString(s + "\n\n") }

func (m *mdBuf) def(term, body string) { fmt.Fprintf(&m.b, "%s\n: %s\n\n", term, body) }

// code writes a fenced block tagged sh, which markdownlint's MD040 asks for.
func (m *mdBuf) code(lines ...string) {
	m.b.WriteString("```sh\n")
	for _, l := range lines {
		m.b.WriteString(l + "\n")
	}
	m.b.WriteString("```\n\n")
}

func (m *mdBuf) bytes() []byte { return m.b.Bytes() }

// mdEscaper neutralizes the Markdown metacharacters CLI prose carries: raw < > would
// be dropped as HTML, and a backtick, asterisk or backslash would start code or
// emphasis. Underscores stay, since GFM ignores intra-word _.
var mdEscaper = strings.NewReplacer(
	`\`, `\\`,
	"`", "\\`",
	"*", `\*`,
	"<", `\<`,
	">", `\>`,
)

func mdEsc(s string) string { return mdEscaper.Replace(s) }
func mdB(s string) string   { return fmt.Sprintf("**%s**", mdEsc(s)) }
func mdI(s string) string   { return fmt.Sprintf("*%s*", mdEsc(s)) }
