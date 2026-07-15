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
		if opts.Template == "" { // bare "-o template": document the fields instead of rendering
			return writeTemplateFields(w, v)
		}
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

// writeTemplate executes a Go text/template body against v. The template sees the
// SAME shape as -o json: field names are the json-tag keys ({{.path}}), NOT the
// PascalCase Go struct fields. We guarantee that by first normalizing v through the
// codec (marshal to JSON, then unmarshal into a plain any), so the template ranges
// over map[string]any / []any exactly as -o json renders it. This makes -o json a
// faithful reference for authoring templates (the kubectl -o go-template model), and
// keeps template field names stable with the json contract rather than with Go
// identifiers. Numbers arrive as float64 (json's default for any); text/template
// prints whole values without a fractional part (float64(26) -> "26").
// Helpers: a curated subset of sprig (strings, lists, dicts, encoding, paths, defaults, semver).
// Excluded: env/expandenv, now/date/uuidv4, crypto, getHostByName, fail.
// No trailing newline is appended (templates control whitespace, matching kubectl -o go-template).
func writeTemplate(w io.Writer, v any, body string) error {
	t, err := template.New("output").Funcs(templateFuncs()).Parse(body)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	shaped, err := jsonShape(v)
	if err != nil {
		return fmt.Errorf("shape template data: %w", err)
	}
	if err := t.Execute(w, shaped); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}
	return nil
}

// jsonShape round-trips v through the codec so the result is the plain-any mirror of
// v's JSON form (map[string]any/[]any/float64/string/bool/nil), keyed by json tags.
// -o template renders against this so its field names match -o json exactly, under
// whichever codec build (encoding/json or json/v2) is active.
func jsonShape(v any) (any, error) {
	b, err := codec.Marshal(v)
	if err != nil {
		return nil, err
	}
	var shaped any
	if err := codec.Unmarshal(b, &shaped); err != nil {
		return nil, err
	}
	return shaped, nil
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
	// Preserve magus's original bindings; magus's list-first join wins over sprig's
	// reversed (sep-first) join. templateJoin (not strings.Join) so it also handles
	// the []any that json-normalization produces for list fields - see writeTemplate.
	f["join"] = templateJoin
	f["upper"] = strings.ToUpper
	f["lower"] = strings.ToLower
	f["trim"] = strings.TrimSpace
	return f
}

// templateJoin joins a list's elements with sep, keeping magus's list-first arg order
// ({{join .list ","}}). Unlike strings.Join it accepts ANY slice/array - []string
// from sprig helpers, and the []any that -o template's json-normalized data yields
// for list fields - stringifying each element. Non-slices stringify whole.
func templateJoin(list any, sep string) string {
	if list == nil { // an absent/null list field joins to empty, like strings.Join(nil, sep)
		return ""
	}
	rv := reflect.ValueOf(list)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return fmt.Sprint(list)
	}
	parts := make([]string, rv.Len())
	for i := range parts {
		parts[i] = fmt.Sprint(rv.Index(i).Interface())
	}
	return strings.Join(parts, sep)
}

// writeTemplateFields prints the fields available to -o template / -o json for v,
// instead of rendering v: what bare "-o template" (no body) produces - the template
// surface documenting itself. It REFLECTS v's type directly (the same approach config
// uses in collectSchema), so it works for ANY output type without a curated set, and
// lists each exported field by its json-tag key (the vocabulary -o json and -o
// template share) with its type. Referenced struct types are listed too, so a nested
// shape (e.g. .projects -> []ProjectEntry) is drillable in the same output. Reflection
// cannot read Go doc comments, so this is field names + types only, no per-field docs.
func writeTemplateFields(w io.Writer, v any) error {
	rt := structType(reflect.TypeOf(v))
	if rt == nil {
		return fmt.Errorf("-o template: %T has no fields to list", v)
	}
	fmt.Fprintln(w, "# fields for -o json / -o template (bare -o template lists these):")
	seen := map[reflect.Type]bool{}
	queue := []reflect.Type{rt}
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		if seen[t] {
			continue
		}
		seen[t] = true
		for _, ref := range writeFieldBlock(w, t) {
			if !seen[ref] {
				queue = append(queue, ref)
			}
		}
	}
	return nil
}

// writeFieldBlock prints one struct type's exported fields as `<json-key>  <type>`,
// json-key column aligned, under a `<TypeName>:` header, and returns the named struct
// types those fields reference (through slice/pointer/map wrappers) so the caller can
// list them too.
func writeFieldBlock(w io.Writer, t reflect.Type) []reflect.Type {
	type field struct{ key, typ string }
	var fields []field
	var refs []reflect.Type
	width := 0
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || f.Anonymous {
			continue
		}
		key := jsonFieldKey(f)
		if key == "" { // json:"-"
			continue
		}
		fields = append(fields, field{key, typeLabel(f.Type)})
		if len(key) > width {
			width = len(key)
		}
		if st := structType(elemType(f.Type)); st != nil {
			refs = append(refs, st)
		}
	}
	fmt.Fprintf(w, "\n%s:\n", t.Name())
	for _, f := range fields {
		fmt.Fprintf(w, "  %-*s  %s\n", width, f.key, f.typ)
	}
	return refs
}

// structType dereferences pointers and returns rt when it is a struct, else nil.
func structType(rt reflect.Type) reflect.Type {
	for rt != nil && rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	if rt == nil || rt.Kind() != reflect.Struct {
		return nil
	}
	return rt
}

// elemType unwraps slice/array/pointer/map(value) wrappers to the element type, so a
// field like []ProjectEntry or *Target surfaces the struct it references.
func elemType(rt reflect.Type) reflect.Type {
	for rt != nil {
		switch rt.Kind() {
		case reflect.Ptr, reflect.Slice, reflect.Array, reflect.Map:
			rt = rt.Elem()
		default:
			return rt
		}
	}
	return rt
}

// jsonFieldKey returns the json object key for a struct field: its json-tag name, the
// Go field name when there is no tag (encoding/json's default), or "" when the field
// is json-excluded (json:"-").
func jsonFieldKey(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name
	}
	switch name, _, _ := strings.Cut(tag, ","); name {
	case "-":
		return ""
	case "":
		return f.Name
	default:
		return name
	}
}

// typeLabel renders a reflect.Type as a readable field type, dropping package
// qualifiers ("types.ProjectEntry" -> "ProjectEntry", "time.Duration" -> "Duration")
// so the listing reads like the json shape rather than Go's fully-qualified names.
func typeLabel(rt reflect.Type) string {
	switch rt.Kind() {
	case reflect.Ptr:
		return "*" + typeLabel(rt.Elem())
	case reflect.Slice, reflect.Array:
		return "[]" + typeLabel(rt.Elem())
	case reflect.Map:
		return "map[" + typeLabel(rt.Key()) + "]" + typeLabel(rt.Elem())
	case reflect.Interface:
		if rt.NumMethod() == 0 {
			return "any"
		}
	}
	if n := rt.Name(); n != "" {
		return n
	}
	return rt.String()
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
	FormatTemplate Format = "template" // -o template=<go-template>

	// Graph-only output formats. Not accepted by other commands.
	FormatDot      Format = "dot"
	FormatMermaid  Format = "mermaid"
	FormatTree     Format = "tree"
	FormatMarkdown Format = "markdown" // describe graph: target catalog + dependency graph as a Markdown doc
	FormatGraphML  Format = "graphml"  // graph export: GraphML XML for external graph viewers
)

// CommonFormats are the formats every structured command accepts. The
// template= form is handled separately by ResolveOutput.
var CommonFormats = []Format{FormatText, FormatJSON, FormatYAML, FormatJSONL, FormatName}

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
		// An empty body ("-o template=") means "list the fields", same as bare
		// "-o template" below - not an error. A non-empty body renders.
		return OutputOptions{Format: FormatTemplate, Template: body}, nil
	}
	if input == "template" {
		// Bare "-o template" (no body): print the output's templatable fields
		// instead of rendering - the self-documentation of the template surface.
		return OutputOptions{Format: FormatTemplate, Template: ""}, nil
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
