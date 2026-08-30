package cli

import (
	"bytes"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/config"
)

// Page is one rendered man page carried by the magus binary.
type Page struct {
	Name    string
	Content []byte
}

// RoffPages renders the complete manpage set from the command registry.
func RoffPages(date, version string) []Page {
	pages := make([]Page, 0, 1+len(All))
	pages = append(pages, Page{Name: "magus.1", Content: renderMainRoff(date, version)})
	for _, command := range All {
		pages = append(pages, Page{
			Name:    "magus-" + command.Name + ".1",
			Content: renderCommandRoff(command, date, version),
		})
	}
	return pages
}

func renderMainRoff(date, version string) []byte {
	var buf bytes.Buffer
	w := newWriter(&buf)
	w.TH("magus", "1", date, thSource(version), "magus Manual")
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
	for _, command := range All {
		ref := fmt.Sprintf(`\fBmagus\-%s\fR(1)`, escapeHyphen(command.Name))
		w.TP(w.B(command.Name), escape(command.Short)+`. See `+ref+`.`)
	}
	w.Dedent()
	writeEnvSectionRoff(w)
	writeFilesSectionRoff(w)
	writeSeeAlsoRoff(w, &buf, "")
	return buf.Bytes()
}

func renderCommandRoff(command Command, date, version string) []byte {
	var buf bytes.Buffer
	w := newWriter(&buf)
	pageName := "magus-" + command.Name
	w.TH(pageName, "1", date, thSource(version), "magus Manual")
	w.SH("Name")
	fmt.Fprintf(&buf, "%s \\- %s\n", escapeHyphen(pageName), escape(command.Short))
	w.SH("Synopsis")
	if split := strings.Index(command.Usage, " "); split < 0 {
		fmt.Fprintln(&buf, `.B `+escapeHyphen(command.Usage))
	} else {
		fmt.Fprintln(&buf, `.B `+escapeHyphen(command.Usage[:split]))
		fmt.Fprintln(&buf, `.RI "`+escape(strings.TrimSpace(command.Usage[split:]))+`"`)
	}
	if command.Long != "" {
		w.SH("Description")
		w.Para(command.Long)
	}
	if command.HasFlags() {
		writeFlagsRoff(w, command.Name, command.BindFlags, "Options")
	}
	// Recursive: config nests four levels, so a one-level walk rendered
	// `config set options` and silently dropped `config cache prune options` and
	// `config mcp connector create options`.
	writeChildFlagsRoff(w, command.Name, command.Children)
	if len(command.Children) > 0 {
		w.SH("Subcommands")
		w.Indent()
		for _, child := range command.Children {
			w.TP(w.B(child.Name), escape(child.Short))
		}
		w.Dedent()
	}
	if len(command.Targets) > 0 {
		w.SH("Targets")
		w.Indent()
		for _, target := range command.Targets {
			w.TP(w.B(target.Name), escape(target.Short))
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
	writeSeeAlsoRoff(w, &buf, command.Name)
	return buf.Bytes()
}

// writeChildFlagsRoff emits an options section for every descendant that
// declares flags, naming each by its full path.
func writeChildFlagsRoff(w *writer, path string, children []Command) {
	for _, child := range children {
		sub := path + " " + child.Name
		if child.HasFlags() {
			writeFlagsRoff(w, sub, child.BindFlags, sub+" options")
		}
		writeChildFlagsRoff(w, sub, child.Children)
	}
}

func writeFlagsRoff(w *writer, name string, build func(*flag.FlagSet), heading string) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	build(flags)
	if heading == "Options" {
		w.SH(heading)
	} else {
		w.SS(escapeHyphen(heading))
	}
	w.Indent()
	flags.VisitAll(func(f *flag.Flag) {
		typeName, _ := flag.UnquoteUsage(f)
		w.TP(roffFlagLabel(f.Name, typeName, f.DefValue), escape(f.Usage))
	})
	w.Dedent()
}

func writeExitStatusRoff(w *writer, codes []ExitCode) {
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

func writeEnvSectionRoff(w *writer) {
	w.SH("Environment")
	w.Indent()
	for _, doc := range config.EnvVarDocs() {
		body := escape(doc.Desc)
		if doc.Default != "" {
			body += ` (default: ` + escape(doc.Default) + `)`
		}
		if doc.YAMLKey != "" {
			body += `. Equivalent magus.yaml key: ` + w.B(doc.YAMLKey) + `.`
		}
		w.TP(`\fB`+doc.EnvVar+`\fR`, body)
	}
	w.Dedent()
}

func writeFilesSectionRoff(w *writer) {
	w.SH("Files")
	w.TP(`\fBmagus.yaml\fR, \fB.magus.yaml\fR`, escapeMulti(filesConfig))
	w.TP(`\fB.magus\-cache/\fR`, escapeMulti(filesCache))
}

func writeSeeAlsoRoff(w *writer, buf *bytes.Buffer, current string) {
	w.SH("See Also")
	refs := make([]string, 0, len(All))
	if current != "" {
		refs = append(refs, `\fBmagus\fR(1)`)
	}
	for _, command := range All {
		if command.Name != current {
			refs = append(refs, fmt.Sprintf(`\fBmagus\-%s\fR(1)`, escapeHyphen(command.Name)))
		}
	}
	fmt.Fprintln(buf, strings.Join(refs, ",\n"))
	fmt.Fprintln(buf, `.br`)
}

func thSource(version string) string {
	if version == "" {
		return "magus"
	}
	return "magus " + version
}

func roffFlagLabel(name, typeName, defaultValue string) string {
	prefix := "--"
	if len(name) == 1 {
		prefix = "-"
	}
	label := `\fB` + escapeHyphen(prefix) + escapeHyphen(name) + `\fR`
	if typeName != "" && typeName != "bool" {
		label += ` \fI` + typeName + `\fR`
	}
	if defaultValue != "" && defaultValue != "false" && defaultValue != "0" && defaultValue != "0s" {
		label += ` (default: ` + escape(defaultValue) + `)`
	}
	return label
}

func escapeMulti(text string) string {
	parts := SplitParas(text)
	for i := range parts {
		parts[i] = escape(parts[i])
	}
	return strings.Join(parts, "\n")
}
