package generate

import (
	"encoding"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"time"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/configuration"
)

// FromStruct lists one setting per leaf of the struct defaults, each with
// its value in defaults as Default. source is the file declaring the
// struct, read for doc comments; empty reads none.
//
// A plain scalar gets a variable named by envPrefix, and a flag unless it
// is a list or a *bool. A leaf under `cli:"-"` gets neither.
func FromStruct(defaults any, envPrefix, source string) ([]configuration.Setting, error) {
	v := reflect.ValueOf(defaults)
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("from struct: %T is not a struct", defaults)
	}
	var docs map[string]*ast.CommentGroup
	if source != "" {
		var err error
		if docs, err = fieldDocs(source, v.Type().Name()); err != nil {
			return nil, err
		}
	}
	w := structWalk{envPrefix: envPrefix, docs: docs}
	w.walk(v.Type(), v, leafPath{})
	return w.out, nil
}

// EnvName joins parts with "_" behind prefix and "_", turns "-" to "_" and
// uppercases the result. An empty prefix adds nothing.
func EnvName(prefix string, parts ...string) string {
	body := strings.Join(parts, "_")
	if prefix != "" {
		body = fmt.Sprintf("%s_%s", prefix, body)
	}
	return strings.ToUpper(strings.ReplaceAll(body, "-", "_"))
}

// FlagName joins parts with "-" and turns "_" to "-".
func FlagName(parts ...string) string {
	return strings.ReplaceAll(strings.Join(parts, "-"), "_", "-")
}

var (
	durationType        = reflect.TypeFor[time.Duration]()
	stringListType      = reflect.TypeFor[[]string]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// leafPath is where a field sits and what its ancestors decided for it.
type leafPath struct {
	yaml, goPath []string
	fileOnly     bool // under `cli:"-"` or a slice element
	template     bool // under a slice element: defaults holds no value
}

func (p leafPath) child(yamlKey, goName string, fileOnly bool) leafPath {
	return leafPath{
		yaml:     append(append([]string{}, p.yaml...), yamlKey),
		goPath:   append(append([]string{}, p.goPath...), goName),
		fileOnly: p.fileOnly || fileOnly,
		template: p.template,
	}
}

type structWalk struct {
	envPrefix string
	docs      map[string]*ast.CommentGroup
	out       []configuration.Setting
}

func (w *structWalk) walk(t reflect.Type, v reflect.Value, at leafPath) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous || !f.IsExported() {
			continue
		}
		key := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if key == "-" {
			continue
		}
		if key == "" {
			key = strings.ToLower(f.Name)
		}
		cli := cliOptions(f.Tag.Get("cli"))
		path := at.child(key, f.Name, cli.optOut)
		var fv reflect.Value
		if !at.template {
			fv = v.Field(i)
		}

		kind, plain := settingKind(f.Type)
		switch {
		case kind != "":
			w.leaf(path, f, fv, cli, kind, plain)
		case f.Type.Kind() == reflect.Struct:
			w.walk(f.Type, fv, path)
		case f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() == reflect.Struct && hasNameKey(f.Type.Elem()):
			element := path.child(configuration.NameSegment, "[]", true)
			element.template = true
			w.walk(f.Type.Elem(), reflect.Value{}, element)
		}
	}
}

func (w *structWalk) leaf(at leafPath, f reflect.StructField, v reflect.Value, cli cliOpts, kind command.Kind, plain bool) {
	doc := w.docs[strings.Join(at.goPath, ".")]
	s := configuration.Setting{
		GoPath:   strings.Join(at.goPath, "."),
		YamlPath: strings.Join(at.yaml, "."),
		Kind:     kind,
		Usage:    sanitizeUsage(firstDocLine(doc)),
		Doc:      docParagraph(doc, f.Name),
	}
	if v.IsValid() {
		s.Default = defaultText(v)
	}
	if plain && !at.fileOnly {
		names := at.yaml
		if cli.name != "" {
			names = []string{cli.name}
		}
		s.EnvVar = EnvName(w.envPrefix, names...)
		if s.Usage == "" {
			s.Usage = s.EnvVar
		} else {
			s.Usage = fmt.Sprintf("%s: %s", s.EnvVar, s.Usage)
		}
		if kind != command.StringList && kind != command.OptionalBool {
			s.Flag = configuration.FlagNames{Long: FlagName(names...), Short: cli.short}
		}
		s.Floor = cli.floor && kind == command.Text
	}
	w.out = append(w.out, s)
}

// settingKind returns the Kind of type t, or "" for a struct or slice to
// walk into. plain is false for a named type that reads no text: no flag
// or variable can carry it.
func settingKind(t reflect.Type) (kind command.Kind, plain bool) {
	ptr := t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface
	if !ptr && reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return command.Text, true
	}
	unnamed := t.PkgPath() == ""
	switch {
	case t == durationType:
		return command.Duration, true
	case t.Kind() == reflect.String:
		return command.String, unnamed
	case t.Kind() == reflect.Int:
		return command.Int, unnamed
	case t.Kind() == reflect.Bool:
		return command.Bool, unnamed
	case t.Kind() == reflect.Float64:
		return command.Float, unnamed
	case t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Bool:
		return command.OptionalBool, t.Elem().PkgPath() == ""
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.String:
		return command.StringList, t == stringListType
	case t.Kind() == reflect.Map && t.Key().Kind() == reflect.String && t.Elem().Kind() == reflect.String:
		return command.StringMap, false
	}
	return "", false
}

// hasNameKey reports whether struct t has a field keyed name, the key a
// slice element is addressed by.
func hasNameKey(t reflect.Type) bool {
	for i := range t.NumField() {
		if strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0] == "name" {
			return true
		}
	}
	return false
}

// defaultText renders v for a reader, or "" for a zero value.
func defaultText(v reflect.Value) string {
	if v.IsZero() {
		return ""
	}
	if d, ok := v.Interface().(time.Duration); ok {
		// 6h0m0s reads as 6h: drop each trailing zero unit.
		s := d.String()
		if trimmed, ok := strings.CutSuffix(s, "m0s"); ok {
			s = trimmed + "m"
		}
		if trimmed, ok := strings.CutSuffix(s, "h0m"); ok {
			s = trimmed + "h"
		}
		return s
	}
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() == reflect.Slice {
		parts := make([]string, v.Len())
		for i := range parts {
			parts[i] = fmt.Sprint(v.Index(i).Interface())
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprint(v.Interface())
}

// cliOpts is what a `cli:"…"` struct tag says about a field.
type cliOpts struct {
	optOut bool   // "-": no flag or variable here or below
	short  string // "short=c"
	name   string // "name=sandbox": replaces the path in both names
	floor  bool   // "floor": see configuration.Setting.Floor
}

func cliOptions(tag string) cliOpts {
	var o cliOpts
	if tag == "" {
		return o
	}
	for _, part := range strings.Split(tag, ",") {
		switch k, v, _ := strings.Cut(part, "="); k {
		case "-":
			o.optOut = true
		case "floor":
			o.floor = true
		case "short":
			o.short = v
		case "name":
			o.name = v
		}
	}
	return o
}

// fieldDocs maps the Go path of each field reachable from struct root,
// through structs source declares, to its doc or trailing comment.
func fieldDocs(source, root string) (map[string]*ast.CommentGroup, error) {
	f, err := parser.ParseFile(token.NewFileSet(), source, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("from struct: parse %s: %w", source, err)
	}
	structs := map[string]*ast.StructType{}
	ast.Inspect(f, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok {
			if st, ok := ts.Type.(*ast.StructType); ok {
				structs[ts.Name.Name] = st
			}
		}
		return true
	})
	st, ok := structs[root]
	if !ok {
		return nil, fmt.Errorf("from struct: %s declares no struct %s", source, root)
	}
	docs := map[string]*ast.CommentGroup{}
	collectDocs(st, structs, nil, docs)
	return docs, nil
}

func collectDocs(st *ast.StructType, structs map[string]*ast.StructType, at []string, docs map[string]*ast.CommentGroup) {
	for _, field := range st.Fields.List {
		for _, name := range field.Names {
			path := append(append([]string{}, at...), name.Name)
			doc := field.Doc
			if doc == nil {
				doc = field.Comment
			}
			docs[strings.Join(path, ".")] = doc
			typeName, slice := localTypeName(field.Type)
			if slice {
				path = append(path, "[]")
			}
			if nested, ok := structs[typeName]; ok {
				collectDocs(nested, structs, path, docs)
			}
		}
	}
}

// localTypeName names the type an identifier, a pointer to one or a slice
// of one spells, and reports whether it is a slice.
func localTypeName(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name, false
	case *ast.StarExpr:
		name, _ := localTypeName(t.X)
		return name, false
	case *ast.ArrayType:
		name, _ := localTypeName(t.Elt)
		return name, true
	}
	return "", false
}

// docParagraph returns the first paragraph of cg as one clause: no leading
// identifier, no final period. A man page appends the default to it.
func docParagraph(cg *ast.CommentGroup, ident string) string {
	text := strings.TrimSuffix(strings.Join(firstParagraph(cg), " "), ".")
	if rest, ok := strings.CutPrefix(text, ident+" "); ok {
		text = rest
		// "Level is the minimum log level" reads "The minimum log level".
		for _, verb := range []string{"is ", "are "} {
			text = strings.TrimPrefix(text, verb)
		}
	}
	if text == "" {
		return ""
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// firstDocLine returns a doc comment's opening sentence. gofmt wraps a
// sentence across lines, so the paragraph is joined before it is cut.
func firstDocLine(cg *ast.CommentGroup) string {
	return firstSentence(strings.Join(firstParagraph(cg), " "))
}

func firstParagraph(cg *ast.CommentGroup) []string {
	if cg == nil {
		return nil
	}
	var prose []string
	for _, c := range cg.List {
		line := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		if line == "" {
			if len(prose) > 0 {
				break
			}
			continue
		}
		prose = append(prose, line)
	}
	return prose
}

// firstSentence cuts s after the first period that a space or the end
// follows, so "e.g." or "--cache-remote-insecure" ends no sentence.
func firstSentence(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && (i == len(s)-1 || s[i+1] == ' ') {
			return s[:i+1]
		}
	}
	return s
}

// sanitizeUsage makes s a safe one-line Go string of at most 120 bytes.
func sanitizeUsage(s string) string {
	s = strings.ReplaceAll(s, `"`, `'`)
	s = strings.ReplaceAll(s, "`", "'")
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}
