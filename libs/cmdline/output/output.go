package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Format is an output format a command line asks for: the -o value.
type Format string

const (
	Text     Format = "text"
	JSON     Format = "json"
	YAML     Format = "yaml"
	JSONL    Format = "jsonl"
	Name     Format = "name"
	Template Format = "template" // -o template=<body>; a bare -o template lists the fields
)

// Common are the formats every structured command accepts, besides Template.
var Common = []Format{Text, JSON, YAML, JSONL, Name}

// Options is a parsed -o value.
type Options struct {
	Format   Format
	Template string // the body after "template="; empty lists the fields instead
}

// Parse reads an -o value. Empty is Text, and "template=<body>" is Template with its
// body. An extra format matches verbatim and wins over Common, so a command can offer
// formats of its own. Anything else is an error naming the choices.
func Parse(s string, extra ...Format) (Options, error) {
	if s == "" {
		return Options{Format: Text}, nil
	}
	for _, f := range extra {
		if string(f) == s {
			return Options{Format: f}, nil
		}
	}
	if body, ok := strings.CutPrefix(s, "template="); ok {
		return Options{Format: Template, Template: body}, nil
	}
	if s == string(Template) || slices.Contains(Common, Format(s)) {
		return Options{Format: Format(s)}, nil
	}
	return Options{}, fmt.Errorf("unknown output format %q (choose: %s, template=<go-template>)", s, Join(Common, ", "))
}

// Join renders formats as a sep-joined string for help and error text.
func Join(formats []Format, sep string) string {
	parts := make([]string, len(formats))
	for i, f := range formats {
		parts[i] = string(f)
	}
	return strings.Join(parts, sep)
}

// Codec is the JSON encoding JSON, JSONL and Template render through, so a program
// keeps one wire form for every format.
type Codec struct {
	Marshal   func(v any) ([]byte, error)
	Unmarshal func(data []byte, v any) error
}

// Writer renders values in the structured formats. Text and Name are the command's
// own to render: which field is a record's identity is the command's to say.
type Writer struct {
	Codec Codec
	Funcs template.FuncMap // the functions a template body may call
}

// Write renders v to w in o's format. A Writer with no Codec, and a format Write does
// not render, are errors.
func (wr Writer) Write(w io.Writer, o Options, v any) error {
	if wr.Codec.Marshal == nil || wr.Codec.Unmarshal == nil {
		return errors.New("output: the Writer has no Codec")
	}
	switch o.Format {
	case JSON:
		return wr.writeJSON(w, v)
	case YAML:
		return writeYAML(w, v)
	case JSONL:
		return wr.writeJSONL(w, v)
	case Template:
		if o.Template == "" {
			return Fields(w, v)
		}
		return wr.writeTemplate(w, v, o.Template)
	}
	return fmt.Errorf("output: Write does not render %q, the command renders it", o.Format)
}

func (wr Writer) writeJSON(w io.Writer, v any) error {
	b, err := wr.Codec.Marshal(v)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	_, err = w.Write(buf.Bytes())
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

// primaryTag marks the collection -o jsonl streams when a type carries several.
const primaryTag = "jsonl"

// writeJSONL streams v's primary collection as one JSON value per line.
//
// Which collection that is must be DECLARED (`jsonl:"primary"`) once a type carries
// more than one. Electing "the only slice field" silently degraded to printing the
// whole object as one line the day a type grew a second list, so an undeclared
// ambiguity is an error naming the candidates instead.
func (wr Writer) writeJSONL(w io.Writer, v any) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		return wr.encodeEach(w, rv)
	case reflect.Struct:
		path, err := primaryField(rv.Type())
		if err != nil {
			return err
		}
		if len(path) > 0 {
			return wr.encodeEach(w, rv.FieldByIndex(path))
		}
	}
	return wr.encodeLine(w, v)
}

func (wr Writer) encodeEach(w io.Writer, list reflect.Value) error {
	for i := range list.Len() {
		if err := wr.encodeLine(w, list.Index(i).Interface()); err != nil {
			return err
		}
	}
	return nil
}

func (wr Writer) encodeLine(w io.Writer, v any) error {
	b, err := wr.Codec.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// primaryField returns the index path of rt's streamable collection, or nil when rt
// has none. It errors when the choice is ambiguous: more than one field marked
// primary, or several collections with none marked.
//
// It descends into embedded structs because json flattens them: a wrapper that adds
// one field beside an embedded result puts that result's collections at the top level.
func primaryField(rt reflect.Type) ([]int, error) {
	var tagged []int
	var taggedKey string
	var candidates [][]int
	var keys []string

	var walk func(reflect.Type, []int) error
	walk = func(t reflect.Type, prefix []int) error {
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			at := append(append([]int(nil), prefix...), i)
			if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Tag.Get("json") == "" {
				if err := walk(f.Type, at); err != nil {
					return err
				}
				continue
			}
			if jsonFieldKey(f) == "" {
				continue
			}
			if f.Type.Kind() != reflect.Slice && f.Type.Kind() != reflect.Array {
				continue
			}
			if f.Tag.Get(primaryTag) == "primary" {
				if tagged != nil {
					return fmt.Errorf("-o jsonl: %s marks both %q and %q as jsonl:\"primary\", exactly one collection streams",
						typeLabel(rt), taggedKey, jsonFieldKey(f))
				}
				tagged, taggedKey = at, jsonFieldKey(f)
			}
			candidates = append(candidates, at)
			keys = append(keys, jsonFieldKey(f))
		}
		return nil
	}
	if err := walk(rt, nil); err != nil {
		return nil, err
	}

	if tagged != nil {
		return tagged, nil
	}
	switch len(candidates) {
	case 0:
		return nil, nil
	case 1:
		return candidates[0], nil
	}
	return nil, fmt.Errorf("-o jsonl: %s carries %d collections (%s) and none is marked jsonl:\"primary\", so there is no single stream to emit, use -o json for the whole record",
		typeLabel(rt), len(keys), strings.Join(keys, ", "))
}

// writeTemplate executes a text/template body against v's -o json form: field names
// are the json keys ({{.cache_key}}), not the Go field names, because -o json is the
// output a reader authors a template against. v round-trips through the Codec, so
// numbers arrive as float64. A missing key is an error rather than "<no value>".
func (wr Writer) writeTemplate(w io.Writer, v any, body string) error {
	t, err := template.New("output").Funcs(wr.Funcs).Option("missingkey=error").Parse(body)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	b, err := wr.Codec.Marshal(v)
	if err != nil {
		return fmt.Errorf("shape template data: %w", err)
	}
	var shaped any
	if err := wr.Codec.Unmarshal(b, &shaped); err != nil {
		return fmt.Errorf("shape template data: %w", err)
	}
	if err := t.Execute(w, shaped); err != nil {
		return fmt.Errorf("execute template: template fields are the -o json names, not the Go field names (.name, not .Name): "+
			"%sto list every field this command exposes, rerun it with a bare -o template (no '=' and no body): %w", recordFields(shaped), err)
	}
	return nil
}

const (
	// maxFieldDepth is how deep recordFields names fields: the record's own and two levels
	// below, which reaches `.job.state` and `.gates[].output_ref`.
	maxFieldDepth = 3
	// maxFieldPaths is how many field paths one error names before it counts the rest.
	maxFieldPaths = 40
)

// recordFields names the field paths the template ran against, as a clause ending in ": ".
// It is empty when the record is not an object.
func recordFields(shaped any) string {
	record, ok := shaped.(map[string]any)
	if !ok || len(record) == 0 {
		return ""
	}
	paths := fieldPaths("", record, 1)
	listed := strings.Join(paths, ", ")
	if len(paths) > maxFieldPaths {
		listed = fmt.Sprintf("%s and %d more", strings.Join(paths[:maxFieldPaths], ", "), len(paths)-maxFieldPaths)
	}
	return fmt.Sprintf("this record's fields are %s (`[]` is each element of a list): ", listed)
}

// fieldPaths is every field path under v at depth and below, sorted within each object and
// each parent before its children. A list's path ends in `[]`, followed by the fields any of
// its object elements carries.
func fieldPaths(prefix string, v any, depth int) []string {
	var paths []string
	switch node := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(node)) {
			p := prefix + "." + k
			paths = append(paths, p)
			if depth < maxFieldDepth {
				paths = append(paths, fieldPaths(p, node[k], depth+1)...)
			}
		}
	case []any:
		merged := map[string]any{}
		for _, el := range node {
			if m, ok := el.(map[string]any); ok {
				for k, x := range m {
					if merged[k] == nil {
						merged[k] = x
					}
				}
			}
		}
		if len(merged) > 0 {
			paths = fieldPaths(prefix+"[]", merged, depth)
		}
	}
	return paths
}

// Fields writes the fields of v's type that -o json and -o template name, by json
// key with their types, then each struct type they reference: what a bare -o template
// prints. A list's element type is listed when v is a list. A type with no fields is
// an error.
func Fields(w io.Writer, v any) error {
	rt := structType(reflect.TypeOf(v))
	if rt == nil {
		rt = structType(elemType(reflect.TypeOf(v)))
	}
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

// writeFieldBlock prints one struct type's exported fields as `<json-key>  <type>`
// under a `<TypeName>:` header, and returns the struct types those fields reference.
func writeFieldBlock(w io.Writer, t reflect.Type) []reflect.Type {
	type field struct{ key, typ string }
	var fields []field
	var refs []reflect.Type
	width := 0
	// VisibleFields reaches an embedded struct's fields, which json promotes to the top level.
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() || f.Anonymous || strings.HasSuffix(f.Tag.Get("json"), ",unknown") {
			continue
		}
		key := jsonFieldKey(f)
		if key == "" {
			continue
		}
		fields = append(fields, field{key, typeLabel(f.Type)})
		width = max(width, len(key))
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

func structType(rt reflect.Type) reflect.Type {
	for rt != nil && rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt == nil || rt.Kind() != reflect.Struct {
		return nil
	}
	return rt
}

// elemType unwraps slice, array, pointer and map wrappers to the element type.
func elemType(rt reflect.Type) reflect.Type {
	for rt != nil {
		switch rt.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			rt = rt.Elem()
		default:
			return rt
		}
	}
	return rt
}

// jsonFieldKey returns a field's json object key: its tag name, the Go name when it
// has no tag, or "" when it is excluded (json:"-").
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

// typeLabel renders a type without package qualifiers ("ProjectEntry", "Duration"),
// so the listing reads like the json shape.
func typeLabel(rt reflect.Type) string {
	switch rt.Kind() {
	case reflect.Pointer:
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
