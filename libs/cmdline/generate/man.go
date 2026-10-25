package generate

import (
	"bytes"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/cmdline"
)

// Page is one rendered page and the file name it belongs under.
type Page struct {
	Name    string
	Content []byte
}

// Roff renders section 1 man pages for root: <name>.1 for the program and
// <name>-<command>.1 for each top-level command. An empty date omits it from the
// title line, and an empty version leaves the source field as the program name.
func Roff(root cmdline.Command, date, version string) []Page {
	pages := make([]Page, 0, 1+len(root.Children))
	pages = append(pages, Page{Name: root.Name + ".1", Content: renderMainRoff(root, date, version)})
	for _, command := range root.Children {
		pages = append(pages, Page{
			Name:    fmt.Sprintf("%s-%s.1", root.Name, command.Name),
			Content: renderCommandRoff(root, command, date, version),
		})
	}
	return pages
}

func renderMainRoff(root cmdline.Command, date, version string) []byte {
	var buf bytes.Buffer
	w := newWriter(&buf)
	w.TH(root.Name, "1", date, thSource(root.Name, version), root.Name+" Manual")
	w.SH("Name")
	fmt.Fprintf(&buf, "%s \\- %s\n", escapeHyphen(root.Name), escape(root.Short))
	writeSynopsisRoff(&buf, root.Usage)
	w.SH("Description")
	w.Para(root.Long)
	if len(root.Flags) > 0 {
		w.SH("Global Flags")
		writeFlagListRoff(w, root)
	}
	w.SH("Subcommands")
	w.Indent()
	for _, command := range root.Children {
		ref := fmt.Sprintf(`\fB%s\-%s\fR(1)`, escapeHyphen(root.Name), escapeHyphen(command.Name))
		w.TP(w.B(command.Name), fmt.Sprintf(`%s. See %s.`, escape(command.Short), ref))
	}
	w.Dedent()
	writeEnvSectionRoff(w, root.Env)
	writeFilesSectionRoff(w, root.Files)
	writeSeeAlsoRoff(w, &buf, root, "")
	return buf.Bytes()
}

func renderCommandRoff(root, command cmdline.Command, date, version string) []byte {
	var buf bytes.Buffer
	w := newWriter(&buf)
	pageName := fmt.Sprintf("%s-%s", root.Name, command.Name)
	w.TH(pageName, "1", date, thSource(root.Name, version), root.Name+" Manual")
	w.SH("Name")
	fmt.Fprintf(&buf, "%s \\- %s\n", escapeHyphen(pageName), escape(command.Short))
	writeSynopsisRoff(&buf, command.Usage)
	if command.Long != "" {
		w.SH("Description")
		w.Para(command.Long)
	}
	if len(command.Flags) > 0 {
		w.SH("Options")
		writeFlagListRoff(w, command)
	}
	// Recursive: a tree nests several levels, and a one-level walk silently dropped
	// every flag below the second.
	writeChildFlagsRoff(w, command.Name, command.Children)
	if len(command.Children) > 0 {
		w.SH("Subcommands")
		w.Indent()
		for _, child := range command.Children {
			w.TP(w.B(child.Name), escape(child.Short))
		}
		w.Dedent()
	}
	if len(command.Operands) > 0 {
		w.SH("Operands")
		w.Indent()
		for _, operand := range command.Operands {
			w.TP(w.B(operand.Name), escape(operand.Short))
		}
		w.Dedent()
	}
	// Before EXAMPLES, per man(7) section order.
	writeExitStatusRoff(w, command.ExitStatus)
	if len(command.Examples) > 0 {
		w.SH("Examples")
		for _, example := range command.Examples {
			if example.Comment != "" {
				fmt.Fprintf(&buf, "\\fI%s\\fR\n", escape(example.Comment))
				w.P()
			}
			w.Example(example.Command)
			w.P()
		}
	}
	writeSeeAlsoRoff(w, &buf, root, command.Name)
	return buf.Bytes()
}

func writeSynopsisRoff(buf *bytes.Buffer, usage string) {
	fmt.Fprintln(buf, ".SH SYNOPSIS")
	if split := strings.Index(usage, " "); split < 0 {
		fmt.Fprintln(buf, `.B `+escapeHyphen(usage))
	} else {
		fmt.Fprintln(buf, `.B `+escapeHyphen(usage[:split]))
		fmt.Fprintln(buf, fmt.Sprintf(`.RI "%s"`, escape(strings.TrimSpace(usage[split:]))))
	}
}

// writeChildFlagsRoff emits an options section for every descendant that
// declares flags, naming each by its full path.
func writeChildFlagsRoff(w *writer, path string, children []cmdline.Command) {
	for _, child := range children {
		sub := fmt.Sprintf("%s %s", path, child.Name)
		if len(child.Flags) > 0 {
			w.SS(escapeHyphen(sub + " options"))
			writeFlagListRoff(w, child)
		}
		writeChildFlagsRoff(w, sub, child.Children)
	}
}

func writeFlagListRoff(w *writer, command cmdline.Command) {
	w.Indent()
	visitFlags(command, func(f *flag.Flag) {
		typeName, usage := flag.UnquoteUsage(f)
		w.TP(roffFlagLabel(f.Name, typeName, f.DefValue), escape(usage))
	})
	w.Dedent()
}

// visitFlags binds command's flags on a throwaway FlagSet and visits them sorted.
func visitFlags(command cmdline.Command, visit func(*flag.Flag)) {
	fs := flag.NewFlagSet(command.Name, flag.ContinueOnError)
	command.BindFlags(fs)
	fs.VisitAll(visit)
}

func writeExitStatusRoff(w *writer, codes []cmdline.ExitCode) {
	if len(codes) == 0 {
		return
	}
	w.SH("Exit status")
	w.Indent()
	for _, code := range codes {
		w.TP(w.B(strconv.Itoa(code.Code)), escape(code.Meaning))
	}
	w.Dedent()
}

func writeEnvSectionRoff(w *writer, env []cmdline.Env) {
	if len(env) == 0 {
		return
	}
	w.SH("Environment")
	w.Indent()
	for _, e := range env {
		body := escape(e.Doc)
		if e.Default != "" {
			body += fmt.Sprintf(` (default: %s)`, escape(e.Default))
		}
		if e.Key != "" {
			body += fmt.Sprintf(`. Config file key: %s.`, w.B(e.Key))
		}
		w.TP(fmt.Sprintf(`\fB%s\fR`, e.Name), body)
	}
	w.Dedent()
}

func writeFilesSectionRoff(w *writer, files []cmdline.File) {
	if len(files) == 0 {
		return
	}
	w.SH("Files")
	for _, f := range files {
		w.TP(w.B(f.Name), escapeMulti(f.Doc))
	}
}

func writeSeeAlsoRoff(w *writer, buf *bytes.Buffer, root cmdline.Command, current string) {
	w.SH("See Also")
	refs := make([]string, 0, 1+len(root.Children))
	if current != "" {
		refs = append(refs, fmt.Sprintf(`\fB%s\fR(1)`, escapeHyphen(root.Name)))
	}
	for _, command := range root.Children {
		if command.Name != current {
			refs = append(refs, fmt.Sprintf(`\fB%s\-%s\fR(1)`, escapeHyphen(root.Name), escapeHyphen(command.Name)))
		}
	}
	fmt.Fprintln(buf, strings.Join(refs, ",\n"))
	fmt.Fprintln(buf, `.br`)
}

func thSource(name, version string) string {
	if version == "" {
		return name
	}
	return fmt.Sprintf("%s %s", name, version)
}

func roffFlagLabel(name, typeName, defaultValue string) string {
	label := fmt.Sprintf(`\fB%s\fR`, escapeHyphen(dashes(name)+name))
	if typeName != "" && typeName != "bool" {
		label += fmt.Sprintf(` \fI%s\fR`, typeName)
	}
	if defaultValue != "" && defaultValue != "false" && defaultValue != "0" && defaultValue != "0s" {
		label += fmt.Sprintf(` (default: %s)`, escape(defaultValue))
	}
	return label
}

func escapeMulti(text string) string {
	parts := splitParas(text)
	for i := range parts {
		parts[i] = escape(parts[i])
	}
	return strings.Join(parts, "\n")
}
