package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/internal/codec"
)

// outputDst returns the writer for structured output; mirrors to --tee file when set.
func outputDst() (io.Writer, func() error, error) {
	if global.tee == "" {
		return os.Stdout, func() error { return nil }, nil
	}
	f, err := os.OpenFile(global.tee, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("--tee: %w", err)
	}
	return io.MultiWriter(os.Stdout, f), f.Close, nil
}

// emitFormatted renders v with opts to the structured-output destination (stdout,
// mirrored to --tee) and closes it. It owns the outputDst/cleanup pair so every
// command's `-o json|yaml|jsonl|template` arm is one call instead of repeating the
// same dst/defer/writeFormatted dance.
func emitFormatted(opts OutputOptions, v any) error {
	w, cleanup, err := outputDst()
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()
	return writeFormatted(w, opts, v)
}

func writeJSON(w io.Writer, v any) error {
	b, err := codec.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}

func writeYAML(w io.Writer, v any) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return enc.Close()
}

// writeJSONL writes each element of the single slice field of v as a JSON line; falls back to one line.
func writeJSONL(w io.Writer, v any) error {
	enc := codec.NewEncoder(w)
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		rt := rv.Type()
		sliceIdx := -1
		for i := range rt.NumField() {
			if rt.Field(i).Type.Kind() == reflect.Slice {
				if sliceIdx >= 0 {
					sliceIdx = -1 // more than one slice field — fall back
					break
				}
				sliceIdx = i
			}
		}
		if sliceIdx >= 0 {
			sliceVal := rv.Field(sliceIdx)
			for i := range sliceVal.Len() {
				if err := enc.Encode(sliceVal.Index(i).Interface()); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return enc.Encode(v)
}

// writeFormatted dispatches v to json/yaml/jsonl/template based on opts.Format.
func writeFormatted(w io.Writer, opts OutputOptions, v any) error {
	switch opts.Format {
	case outputJSON:
		return writeJSON(w, v)
	case outputYAML:
		return writeYAML(w, v)
	case outputJSONL:
		return writeJSONL(w, v)
	case outputTemplate:
		return writeTemplate(w, v, opts.Template)
	default:
		return fmt.Errorf("writeFormatted: unsupported format %q", opts.Format)
	}
}

// flagWasSet reports whether flag name was explicitly set (not just defaulted) on fs.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// writeTemplate executes a Go text/template body against v. The template sees the same shape as -o json.
// Helpers: a curated subset of sprig (strings, lists, dicts, encoding, paths, defaults, semver).
// Excluded: env/expandenv, now/date/uuidv4, crypto, getHostByName, fail.
// No trailing newline is appended (templates control whitespace, matching kubectl -o go-template).
func writeTemplate(w io.Writer, v any, body string) error {
	t, err := template.New("output").Funcs(templateFuncs()).Parse(body)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	if err := t.Execute(w, v); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}
	return nil
}

// excludedSprigFuncs lists sprig entries absent from magus templates (non-hermetic or dangerous).
// HermeticTxtFuncMap already drops env/expandenv/getHostByName; this list adds the rest.
var excludedSprigFuncs = []string{
	// non-deterministic
	"now", "date", "dateInZone", "dateModify", "mustDateModify",
	"toDate", "unixEpoch", "htmlDate", "htmlDateInZone",
	"uuidv4",
	// crypto — no business in a build-tool template
	"genPrivateKey", "genCA", "genSelfSignedCert", "genSignedCert",
	"derivePassword", "htpasswd", "bcrypt",
	// confusing in -o context
	"fail",
}

// templateFuncs returns the FuncMap exposed to -o template= bodies.
func templateFuncs() template.FuncMap {
	f := sprig.HermeticTxtFuncMap()
	for _, name := range excludedSprigFuncs {
		delete(f, name)
	}
	// Preserve magus's original bindings; magus strings.Join wins over sprig's reversed join.
	f["join"] = strings.Join
	f["upper"] = strings.ToUpper
	f["lower"] = strings.ToLower
	f["trim"] = strings.TrimSpace
	return f
}

// Format identifies how a command renders structured output (-o/--output).
// This is a CLI-presentation concern, so it lives in package main rather than in
// the domain types package.
type Format string

const (
	FormatText     Format = "text"
	FormatJSON     Format = "json"
	FormatYAML     Format = "yaml"
	FormatJSONL    Format = "jsonl" // one JSON object per line; streams elements of the primary collection field
	FormatName     Format = "name"
	FormatWide     Format = "wide"
	FormatTemplate Format = "template" // -o template=<go-template>

	// Graph-only output formats. Not accepted by other commands.
	FormatDot      Format = "dot"
	FormatMermaid  Format = "mermaid"
	FormatTree     Format = "tree"
	FormatMarkdown Format = "markdown" // describe graph: target catalog + dependency graph as a Markdown doc
)

// CommonFormats are the formats every structured command accepts. The
// template= form is handled separately by ResolveOutput.
var CommonFormats = []Format{FormatText, FormatJSON, FormatYAML, FormatJSONL, FormatName, FormatWide}

// OutputOptions is the parsed -o value; Template is set when Format is FormatTemplate.
// Obtain one from ResolveOutput — the zero value (empty Format) is not a valid
// opts and matches no renderer.
type OutputOptions struct {
	Format   Format
	Template string
}

// ResolveOutput parses an -o/--output value into an OutputOptions. An empty input
// resolves to FormatText; a "template=<body>" input resolves to FormatTemplate
// carrying the body. Any extra formats (e.g. the graph-only FormatDot/Mermaid/
// Tree) are matched verbatim and take precedence over the built-in set, letting
// a command opt into formats beyond CommonFormats. Unknown values are an error.
func ResolveOutput(input string, extra ...Format) (OutputOptions, error) {
	if input == "" {
		return OutputOptions{Format: FormatText}, nil
	}
	for _, v := range extra {
		if string(v) == input {
			return OutputOptions{Format: v}, nil
		}
	}
	if body, ok := strings.CutPrefix(input, "template="); ok {
		if body == "" {
			return OutputOptions{}, fmt.Errorf("output template body must be non-empty (e.g. -o template='{{.Path}}')")
		}
		return OutputOptions{Format: FormatTemplate, Template: body}, nil
	}
	for _, v := range CommonFormats {
		if string(v) == input {
			return OutputOptions{Format: v}, nil
		}
	}
	return OutputOptions{}, fmt.Errorf("unknown output format %q (choose: %s, template=<go-template>)", input, JoinFormats(CommonFormats, ", "))
}

// JoinFormats renders fs as a sep-joined string for help and error text.
func JoinFormats(fs []Format, sep string) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = string(f)
	}
	return strings.Join(parts, sep)
}
