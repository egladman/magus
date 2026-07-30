package manpage

import (
	"bytes"
	"flag"
	"fmt"
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
	w := NewWriter(&buf)
	w.TH("magus", "1", date, thSource(version), "magus Manual")
	w.SH("Name")
	fmt.Fprintln(&buf, `magus \- workspace-aware build orchestrator and content-addressed cache`)
	w.SH("Synopsis")
	fmt.Fprintln(&buf, `.B magus`)
	fmt.Fprintln(&buf, `.RI [ flags ]\ <subcommand>\ [ args ]`)
	w.SH("Description")
	w.Para(MainDescription)
	w.SH("Global Flags")
	w.Para(GlobalFlagsIntro)
	w.TP(`\fB\-\-root\fR \fIpath\fR`, escapeMulti(FlagRoot))
	w.TP(`\fB\-\-config\fR \fIpath\fR`, escapeMulti(FlagConfig))
	w.TP(`\fB\-\-output\fR \fIfmt\fR, \fB\-o\fR \fIfmt\fR`, escapeMulti(FlagOutput))
	w.TP(`\fB\-\-concurrency\fR \fIN\fR`, escapeMulti(FlagConcurrency))
	w.TP(`\fB\-v\fR`, escapeMulti(FlagVerbose))
	w.SH("Subcommands")
	w.Indent()
	for _, command := range All {
		ref := fmt.Sprintf(`\fBmagus\-%s\fR(1)`, EscapeHyphen(command.Name))
		w.TP(w.B(command.Name), Escape(command.Short)+`. See `+ref+`.`)
	}
	w.Dedent()
	writeEnvSectionRoff(w)
	writeFilesSectionRoff(w)
	writeSeeAlsoRoff(w, &buf, "")
	return buf.Bytes()
}

func renderCommandRoff(command Command, date, version string) []byte {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	pageName := "magus-" + command.Name
	w.TH(pageName, "1", date, thSource(version), "magus Manual")
	w.SH("Name")
	fmt.Fprintf(&buf, "%s \\- %s\n", EscapeHyphen(pageName), Escape(command.Short))
	w.SH("Synopsis")
	if split := strings.Index(command.Usage, " "); split < 0 {
		fmt.Fprintln(&buf, `.B `+EscapeHyphen(command.Usage))
	} else {
		fmt.Fprintln(&buf, `.B `+EscapeHyphen(command.Usage[:split]))
		fmt.Fprintln(&buf, `.RI "`+Escape(strings.TrimSpace(command.Usage[split:]))+`"`)
	}
	if command.Long != "" {
		w.SH("Description")
		w.Para(command.Long)
	}
	if command.BuildFlags != nil {
		writeFlagsRoff(w, command.Name, command.BuildFlags, "Options")
	}
	for _, child := range command.Children {
		if child.BuildFlags != nil {
			writeFlagsRoff(w, command.Name+" "+child.Name, child.BuildFlags, command.Name+" "+child.Name+" options")
		}
	}
	if len(command.Children) > 0 {
		w.SH("Subcommands")
		w.Indent()
		for _, child := range command.Children {
			w.TP(w.B(child.Name), Escape(child.Short))
		}
		w.Dedent()
	}
	if len(command.Targets) > 0 {
		w.SH("Targets")
		w.Indent()
		for _, target := range command.Targets {
			w.TP(w.B(target.Name), Escape(target.Short))
		}
		w.Dedent()
	}
	if len(command.Examples) > 0 {
		w.SH("Examples")
		for _, example := range command.Examples {
			if example.Comment != "" {
				fmt.Fprintf(&buf, "\\fI%s\\fR\n", Escape(example.Comment))
				w.P()
			}
			w.Example(example.Command)
			w.P()
		}
	}
	writeSeeAlsoRoff(w, &buf, command.Name)
	return buf.Bytes()
}

func writeFlagsRoff(w *Writer, name string, build func(*flag.FlagSet), heading string) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	build(flags)
	if heading == "Options" {
		w.SH(heading)
	} else {
		w.SS(EscapeHyphen(heading))
	}
	w.Indent()
	flags.VisitAll(func(f *flag.Flag) {
		typeName, _ := flag.UnquoteUsage(f)
		w.TP(roffFlagLabel(f.Name, typeName, f.DefValue), Escape(f.Usage))
	})
	w.Dedent()
}

func writeEnvSectionRoff(w *Writer) {
	w.SH("Environment")
	w.Indent()
	for _, doc := range config.EnvVarDocs() {
		body := Escape(doc.Desc)
		if doc.Default != "" {
			body += ` (default: ` + Escape(doc.Default) + `)`
		}
		if doc.YAMLKey != "" {
			body += `. Equivalent magus.yaml key: ` + w.B(doc.YAMLKey) + `.`
		}
		w.TP(`\fB`+doc.EnvVar+`\fR`, body)
	}
	w.Dedent()
}

func writeFilesSectionRoff(w *Writer) {
	w.SH("Files")
	w.TP(`\fBmagus.yaml\fR, \fB.magus.yaml\fR`, escapeMulti(FilesConfig))
	w.TP(`\fB.magus\-cache/\fR`, escapeMulti(FilesCache))
}

func writeSeeAlsoRoff(w *Writer, buf *bytes.Buffer, current string) {
	w.SH("See Also")
	refs := make([]string, 0, len(All))
	if current != "" {
		refs = append(refs, `\fBmagus\fR(1)`)
	}
	for _, command := range All {
		if command.Name != current {
			refs = append(refs, fmt.Sprintf(`\fBmagus\-%s\fR(1)`, EscapeHyphen(command.Name)))
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
	label := `\fB` + EscapeHyphen(prefix) + EscapeHyphen(name) + `\fR`
	if typeName != "" && typeName != "bool" {
		label += ` \fI` + typeName + `\fR`
	}
	if defaultValue != "" && defaultValue != "false" && defaultValue != "0" && defaultValue != "0s" {
		label += ` (default: ` + Escape(defaultValue) + `)`
	}
	return label
}

func escapeMulti(text string) string {
	parts := SplitParas(text)
	for i := range parts {
		parts[i] = Escape(parts[i])
	}
	return strings.Join(parts, "\n")
}
