// Subcommand `jobschema` emits the published JSON Schema for each job record from
// the Go struct that decodes it: internal/job/gen/job.schema.json from types.Declaration,
// result.schema.json from types.JobResult.
//
// The schema and the struct were two hand-written copies of one contract, and the test
// between them could only report that they had already diverged. Deriving the schema
// means a field cannot reach the wire undescribed: the json name, the requiredness, the
// closed state set and the lease id charset all come from what the decoder enforces, and
// the prose comes from the field's own doc comment, so there is no second place to write
// any of it down.
//
// It reads the structs through go/ast rather than reflection, which is what keeps it out
// of internal/job's import graph: that package go:embeds the files written here, so a
// generator importing it could not build whenever its own output was missing.

package main

import (
	"errors"
	"maps"
	"reflect"
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/internal/generate/godecl"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// jobRecord is one published record: the struct a caller's JSON decodes into, and the
// file its schema is written to.
type jobRecord struct {
	Struct string
	// Type is the record's Go type, the root the schema's sources are
	// found from. Nil for a record read from a given file.
	Type    reflect.Type
	Title   string
	File    string
	Version string // the constant holding this record's api_version
}

var jobRecords = []jobRecord{
	{Struct: "Declaration", Type: reflect.TypeFor[types.Declaration](), Title: "magus job", File: "job.schema.json", Version: "JobAPIVersion"},
	{Struct: "JobResult", Type: reflect.TypeFor[types.JobResult](), Title: "magus job result", File: "result.schema.json", Version: "JobResultAPIVersion"},
}

const (
	schemaDraft = "http://json-schema.org/draft-07/schema#"
	schemaIDs   = "https://magus.invalid/job/"
	// versionProperty is the one property whose value is fixed rather than described.
	versionProperty = "api_version"
	// leaseIDMarker is the `schema:"leaseid"` tag: this field holds a lease id, so the
	// pattern and length types.ValidJobID enforces are published with it.
	leaseIDMarker = "leaseid"
	// The other markers a field may carry; each is a string constraint a struct tag cannot
	// spell on its own. A marker on a []string constrains the items.
	writePathMarker = "writepath"
	durationMarker  = "duration"
	absPathMarker   = "abspath"
	targetMarker    = "target"
	nonBlankMarker  = "nonblank"
	// leaseCheckStruct is the one record rendered as a choice between two shapes.
	leaseCheckStruct = "LeaseCheck"
	// absPathPattern is what filepath.IsAbs accepts on the platforms magus ships for.
	absPathPattern = "^/"
)

// narrowedSets are the open types a record publishes a narrower set for,
// keyed by the name a field is declared with.
var narrowedSets = map[string]func() []string{
	// Deviation.Code names a code a deviation may waive, as the decoder checks.
	"DiagnosticCode": permissibleCodeNames,
}

func runJobSchema(args []string) error {
	fs := flag.NewFlagSet("jobschema", flag.ExitOnError)
	out := fs.String("out", "internal/job/gen", "directory the schema files are written into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	decls, err := jobDecls(jobRecords)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	for _, rec := range jobRecords {
		body, err := renderJobSchema(rec, decls)
		if err != nil {
			return err
		}
		path := filepath.Join(*out, rec.File)
		if err := emit.File(path, body); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Printf("wrote %s\n", path)
	}
	return nil
}

// renderJobSchema renders one record. Every rule it applies is derived:
//
//   - one property per json tag, in declaration order and named by the tag;
//   - required is every field whose tag carries no omitempty, because that is the only
//     requiredness the decoder expresses;
//   - an untagged embedded struct contributes its properties inline, as the decoder reads
//     it, and a `,unknown` bag contributes none;
//   - api_version is the one version its package declares, the only one
//     a decoder takes;
//   - a field typed by a closed set carries that set as an enum, plus "" when the field
//     is omitempty and an absent value is therefore legal;
//   - a field marked `schema:"..."` carries the string constraint that marker names (a lease
//     id, a write path, a duration, an absolute path, a target, a non-blank string); on a
//     []string the items carry it;
//   - LeaseCheck is a choice between a target and a script, since its struct cannot say so;
//   - a description is the doc comment's first paragraph, so the rationale a later
//     paragraph carries stays in the source.
func renderJobSchema(rec jobRecord, d *goDecls) ([]byte, error) {
	st, ok := d.structs[rec.Struct]
	if !ok {
		return nil, fmt.Errorf("no struct %s is declared in the sources the records reach", rec.Struct)
	}
	version, ok := d.consts[rec.Version]
	if !ok {
		return nil, fmt.Errorf("%s: no constant %s is declared in the sources the records reach", rec.Struct, rec.Version)
	}
	props, required, err := properties(rec.Struct, st, d, version)
	if err != nil {
		return nil, err
	}

	root := node{
		{"$schema", schemaDraft},
		{"$id", schemaIDs + rec.File},
		{"$comment", fmt.Sprintf("Generated from %s by `magus-utils jobschema`."+
			" DO NOT EDIT; run `magus run job-generate .`.", rec.Struct)},
		{"title", rec.Title},
	}
	if doc := d.docs[rec.Struct]; doc != "" {
		root = append(root, member{"description", doc})
	}
	root = append(root,
		member{"type", "object"},
		member{"additionalProperties", false},
		member{"required", required},
		member{"properties", props},
	)

	var b strings.Builder
	if err := writeValue(&b, root, ""); err != nil {
		return nil, err
	}
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// properties renders one struct's fields, and the names of those a caller must send.
func properties(structName string, st *ast.StructType, d *goDecls, version string) (node, []string, error) {
	var props node
	required := []string{}
	for _, f := range st.Fields.List {
		tag := ""
		if f.Tag != nil {
			tag = f.Tag.Value
		}
		name, opts, _ := strings.Cut(godecl.TagRaw(tag, "json"), ",")
		if len(f.Names) == 0 {
			// An embedded struct with no json name is inlined by the decoder, so its
			// properties are the parent's.
			embedded, ok := f.Type.(*ast.Ident)
			if !ok || name != "" || d.structs[embedded.Name] == nil {
				return nil, nil, fmt.Errorf("%s: only an untagged embedded struct these sources declare can be published", structName)
			}
			inner, innerRequired, err := properties(embedded.Name, d.structs[embedded.Name], d, version)
			if err != nil {
				return nil, nil, err
			}
			props = append(props, inner...)
			required = append(required, innerRequired...)
			continue
		}
		if len(f.Names) != 1 {
			return nil, nil, fmt.Errorf("%s: a multi-name field has no single json name", structName)
		}
		field := f.Names[0].Name
		switch {
		case name == "-", name == "" && opts == "unknown":
			continue
		case name == "":
			return nil, nil, fmt.Errorf("%s.%s carries no json tag, so no schema can name it", structName, field)
		}
		// Under jsonv2 omitempty keeps a false or a zero, so omitzero is how a scalar says it
		// is optional, and either makes the field one a record may leave out.
		tagOpts := strings.Split(opts, ",")
		omitempty := slices.Contains(tagOpts, "omitempty") || slices.Contains(tagOpts, "omitzero")

		prop, err := property(f, structName, field, name, omitempty, d, version)
		if err != nil {
			return nil, nil, err
		}
		props = append(props, member{name, prop})
		if !omitempty {
			required = append(required, name)
		}
	}
	return props, required, nil
}

func property(f *ast.Field, structName, field, name string, omitempty bool, d *goDecls, version string) (node, error) {
	tag := ""
	if f.Tag != nil {
		tag = f.Tag.Value
	}
	var prop node
	if name == versionProperty {
		prop = node{{"type", "string"}, {"const", version}}
	} else {
		var err error
		if prop, err = typeMembers(f.Type, omitempty, godecl.Tag(tag, "schema"), d, version); err != nil {
			return nil, fmt.Errorf("%s.%s: %w", structName, field, err)
		}
	}
	if doc := d.docs[fmt.Sprintf("%s.%s", structName, field)]; doc != "" {
		prop = append(prop, member{"description", doc})
	}
	return prop, nil
}

// typeMembers is the JSON shape of one Go type. A schema marker constrains the string it
// sits on, or the items of a []string, since the array itself has no charset.
func typeMembers(expr ast.Expr, omitempty bool, marker string, d *goDecls, version string) (node, error) {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return typeMembers(t.X, omitempty, marker, d, version)
	case *ast.ArrayType:
		items, err := typeMembers(t.Elt, false, marker, d, version)
		if err != nil {
			return nil, err
		}
		return node{{"type", "array"}, {"items", items}}, nil
	case *ast.SelectorExpr:
		return namedType(t.Sel.Name, omitempty, marker, d, version)
	case *ast.Ident:
		switch t.Name {
		case "string":
			members, err := markerMembers(marker)
			return append(node{{"type", "string"}}, members...), err
		case "int", "int64":
			return withoutMarker(node{{"type", "integer"}}, marker)
		case "bool":
			return withoutMarker(node{{"type", "boolean"}}, marker)
		case "float64":
			return withoutMarker(node{{"type", "number"}}, marker)
		}
		return namedType(t.Name, omitempty, marker, d, version)
	}
	return nil, fmt.Errorf("%T is not a type this generator can publish", expr)
}

// markerMembers is what a `schema:"..."` marker publishes for a string. An unknown marker
// is an error, since a typo would otherwise publish the field with no constraint.
func markerMembers(marker string) (node, error) {
	switch marker {
	case "":
		return nil, nil
	case leaseIDMarker:
		return node{{"pattern", leaseIDPattern()}, {"maxLength", types.MaxJobIDLen}}, nil
	case writePathMarker:
		return node{{"pattern", writePathPattern()}}, nil
	case durationMarker:
		return node{{"pattern", durationPattern()}}, nil
	case absPathMarker:
		return node{{"pattern", absPathPattern}}, nil
	case nonBlankMarker:
		return node{{"minLength", 1}}, nil
	case targetMarker:
		return node{{"pattern", targetPattern()}}, nil
	}
	return nil, fmt.Errorf("schema marker %q is not one this generator publishes", marker)
}

// withoutMarker refuses a marker on a type with no string to constrain.
func withoutMarker(shape node, marker string) (node, error) {
	if marker != "" {
		return nil, fmt.Errorf("schema marker %q marks a string, and this field is not one", marker)
	}
	return shape, nil
}

func namedType(name string, omitempty bool, marker string, d *goDecls, version string) (node, error) {
	if set, ok := d.sets[name]; ok {
		values := set
		if omitempty {
			values = append([]string{""}, set...)
		}
		return withoutMarker(node{{"type", "string"}, {"enum", values}}, marker)
	}
	st, ok := d.structs[name]
	if !ok {
		return nil, fmt.Errorf("type %s is neither a struct nor a closed set these sources declare", name)
	}
	if marker != "" {
		return nil, fmt.Errorf("schema marker %q marks a string, and %s is a record", marker, name)
	}
	props, required, err := properties(name, st, d, version)
	if err != nil {
		return nil, err
	}
	shape := node{{"type", "object"}, {"additionalProperties", false}}
	if name == leaseCheckStruct {
		// The struct cannot say "one of": target is required there only because its tag
		// has no omitempty, and a script check leaves it empty.
		required = slices.DeleteFunc(required, func(r string) bool { return r == "target" })
		if len(required) > 0 {
			shape = append(shape, member{"required", required})
		}
		return append(shape, member{"properties", props}, member{"oneOf", leaseCheckChoice()}), nil
	}
	return append(shape, member{"required", required}, member{"properties", props}), nil
}

// leaseCheckChoice is the either-or Declaration.Problems enforces on a check: a target
// and no script, or a script and no target. "No" is absent or empty, because a Go-encoded
// script check carries an empty target.
func leaseCheckChoice() []node {
	return []node{
		{{"required", []string{"target"}}, {"properties", node{
			{"target", node{{"minLength", 1}}},
			{"script", node{{"maxLength", 0}}},
		}}},
		{{"required", []string{"script"}}, {"properties", node{
			{"script", node{{"minLength", 1}}},
			{"target", node{{"maxLength", 0}}},
		}}},
	}
}

// goDecls is what the sources declare: every struct, the first paragraph of every type
// and field doc comment, and every integer constant.
type goDecls struct {
	structs map[string]*ast.StructType
	docs    map[string]string
	consts  map[string]string
	// sets holds each closed set's values, keyed by the name a field
	// declares it with.
	sets map[string][]string
}

// jobDecls reads the files that declare every struct the records reach and
// their version constants, as the boundary load found them. Each enum
// the records reach publishes its cases as a closed set.
func jobDecls(records []jobRecord) (*goDecls, error) {
	paths, sets, err := jobSources(records)
	if err != nil {
		return nil, err
	}
	d, err := loadGoDecls(paths...)
	if err != nil {
		return nil, err
	}
	d.sets = sets
	return d, nil
}

func jobSources(records []jobRecord) ([]string, map[string][]string, error) {
	prog := boundary().prog
	files := map[string]bool{}
	sets := map[string][]string{}
	for name, set := range narrowedSets {
		sets[name] = set()
	}
	var errs []error
	locate := func(pkgPath, name string) {
		pkg, err := prog.Package(pkgPath)
		if err != nil {
			errs = append(errs, err)
			return
		}
		obj := pkg.Types.Scope().Lookup(name)
		if obj == nil {
			errs = append(errs, fmt.Errorf("%s declares no %s", pkgPath, name))
			return
		}
		files[pkg.Fset.Position(obj.Pos()).Filename] = true
	}
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.PkgPath() == "" || seen[t] {
			return
		}
		seen[t] = true
		switch t.Kind() {
		case reflect.String:
			if e, ok := buzzEnum(t); ok {
				sets[t.Name()] = caseValues(e)
			}
		case reflect.Struct:
			locate(t.PkgPath(), t.Name())
			for i := range t.NumField() {
				if f := t.Field(i); f.IsExported() || f.Anonymous {
					walk(f.Type)
				}
			}
		}
	}
	for _, rec := range records {
		walk(rec.Type)
		locate(rec.Type.PkgPath(), rec.Version)
	}
	paths := slices.Sorted(maps.Keys(files))
	return paths, sets, errors.Join(errs...)
}

// caseValues is an enum's values without its zero case: an omitempty field
// adds "" itself.
func caseValues(e boundaryEnum) []string {
	var values []string
	for _, c := range e.Cases {
		if c.Value != "" {
			values = append(values, c.Value)
		}
	}
	return values
}

func loadGoDecls(paths ...string) (*goDecls, error) {
	d := &goDecls{
		structs: map[string]*ast.StructType{},
		docs:    map[string]string{},
		consts:  map[string]string{},
	}
	for _, path := range paths {
		file, err := godecl.Parse(path)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				d.read(gd, spec)
			}
		}
	}
	return d, nil
}

func (d *goDecls) read(gd *ast.GenDecl, spec ast.Spec) {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		st, ok := s.Type.(*ast.StructType)
		if !ok {
			return
		}
		d.structs[s.Name.Name] = st
		// A lone type declaration carries its doc on the GenDecl; one inside a
		// parenthesized block carries it on the spec.
		doc := s.Doc
		if doc == nil {
			doc = gd.Doc
		}
		d.docs[s.Name.Name] = firstParagraph(doc.Text())
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				d.docs[fmt.Sprintf("%s.%s", s.Name.Name, name.Name)] = firstParagraph(f.Doc.Text())
			}
		}
	case *ast.ValueSpec:
		if gd.Tok != token.CONST || len(s.Names) != 1 || len(s.Values) != 1 {
			return
		}
		lit, ok := s.Values[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		if v, err := strconv.Unquote(lit.Value); err == nil {
			d.consts[s.Names[0].Name] = v
		}
	}
}

// firstParagraph is a doc comment's summary, on one line.
func firstParagraph(doc string) string {
	head, _, _ := strings.Cut(strings.TrimSpace(doc), "\n\n")
	return strings.Join(strings.Fields(head), " ")
}

func permissibleCodeNames() []string {
	var names []string
	for _, c := range types.AllDiagnosticCodes() {
		if types.Permissible(c) {
			names = append(names, string(c))
		}
	}
	return names
}

// leaseIDPattern derives the id charset by asking types.ValidJobID about every ASCII
// rune, so the published pattern cannot say something other than what the store enforces.
// Probing ASCII alone is enough because the validator accepts nothing above it. Each rune
// is probed inside a segment, since '-', '.' and '/' are refused only at a segment's edge.
func leaseIDPattern() string {
	var accepted []rune
	for r := rune(0); r < 0x80; r++ {
		if types.ValidJobID(fmt.Sprintf("a%sa", string(r))) {
			accepted = append(accepted, r)
		}
	}
	return fmt.Sprintf("^[%s]+$", charClass(accepted))
}

// writePathPattern is the write path shape that needs no workspace: a non-empty,
// workspace-relative path, optionally followed by `#<declaration>`. It refuses a leading
// `~` or `/`, a `..` segment, a trailing `/`, a bare `.` and a final `**` segment. Segments
// are spelled out positively because the pattern must run under both ECMAScript and RE2,
// and neither shares a lookahead. `\#` keeps a `#` inside the path, as types.SplitClaim
// reads it.
func writePathPattern() string {
	const ch = `(?:\\#|[^/#])`
	const dotted = `\.(?:[^/#.]` + ch + `*)?|\.\.` + ch + `+`
	anySeg := `(?:[^/#.]` + ch + `*|` + dotted + `)`
	firstSeg := `(?:[^/#.~]` + ch + `*|` + dotted + `)`
	// A last segment is not `**`: a lone `*`, `*x` and `**x` are all allowed.
	const starts = `\*(?:[^*/#]` + ch + `*)?|\*\*` + ch + `+`
	lastSeg := `(?:[^/#.*]` + ch + `*|` + starts + `|` + dotted + `)`
	// A path of one segment is also the first, and a lone `.` is the workspace itself.
	onlySeg := `(?:[^/#.*~]` + ch + `*|` + starts + `|\.[^/#.]` + ch + `*|\.\.` + ch + `+)`
	return fmt.Sprintf("^(?:%s|%s(?:/%s)*/%s)(?:#[^#]+)?$", onlySeg, firstSeg, anySeg, lastSeg)
}

// durationPattern is a Go duration with a positive total, or empty. The nonzero component
// is spelled out because ParseJobTimeout refuses `0s` and a pattern cannot compare.
func durationPattern() string {
	const unit = `(?:ns|us|µs|μs|ms|s|m|h)`
	const number = `(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)`
	const nonzero = `(?:[0-9]*[1-9][0-9]*(?:\.[0-9]*)?|[0-9]*\.[0-9]*[1-9][0-9]*)`
	return `^(?:(?:` + number + unit + `)*` + nonzero + unit + `(?:` + number + unit + `)*)?$`
}

// targetPattern is a target name, optionally behind the `<spell>::` filter a check row
// carries. The charset is asked of types.ValidateTargetName for the same reason as the
// lease id's. It admits the empty string, which a script check leaves.
func targetPattern() string {
	var accepted []rune
	for r := rune(0); r < 0x80; r++ {
		if types.ValidateTargetName(string(r)) == nil {
			accepted = append(accepted, r)
		}
	}
	class := fmt.Sprintf("[%s]", charClass(accepted))
	return fmt.Sprintf("^(?:%s+::%s+|%s*)$", class, class, class)
}

// charClass renders accepted runes as a regexp character class: alphanumeric runs
// collapse to a range and everything else is listed, with `-` last so it cannot read as
// one. The runes come in ascending order.
func charClass(accepted []rune) string {
	var b strings.Builder
	dash := false
	for i := 0; i < len(accepted); i++ {
		r := accepted[i]
		if r == '-' {
			dash = true
			continue
		}
		end := i
		for end+1 < len(accepted) && accepted[end+1] == accepted[end]+1 && sameClass(accepted[end+1], r) {
			end++
		}
		if end-i >= 2 {
			fmt.Fprintf(&b, "%s-%s", string(r), string(accepted[end]))
			i = end
			continue
		}
		if strings.ContainsRune(`\]^`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	if dash {
		b.WriteByte('-')
	}
	return b.String()
}

// sameClass reports whether two runes are both digits, both lower or both upper, which is
// the only kind of run worth collapsing into a range.
func sameClass(a, b rune) bool {
	switch {
	case a >= '0' && a <= '9':
		return b >= '0' && b <= '9'
	case a >= 'a' && a <= 'z':
		return b >= 'a' && b <= 'z'
	case a >= 'A' && a <= 'Z':
		return b >= 'A' && b <= 'Z'
	}
	return false
}

// member is one key of a JSON object and node an ordered set of them: the schema is read
// by people, so its properties are written in the order the struct declares them rather
// than in whatever order a map yields.
type member struct {
	key   string
	value any
}

type node []member

func writeValue(b *strings.Builder, v any, indent string) error {
	switch val := v.(type) {
	case node:
		return writeNode(b, val, indent)
	case []node:
		b.WriteString("[\n")
		inner := indent + "  "
		for i, n := range val {
			b.WriteString(inner)
			if err := writeNode(b, n, inner); err != nil {
				return err
			}
			if i < len(val)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "]")
		return nil
	case []string:
		b.WriteByte('[')
		for i, s := range val {
			if i > 0 {
				b.WriteString(", ")
			}
			if err := writeScalar(b, s); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		return nil
	}
	return writeScalar(b, v)
}

func writeNode(b *strings.Builder, n node, indent string) error {
	if len(n) == 0 {
		b.WriteString("{}")
		return nil
	}
	inner := indent + "  "
	b.WriteString("{\n")
	for i, m := range n {
		b.WriteString(inner)
		if err := writeScalar(b, m.key); err != nil {
			return err
		}
		b.WriteString(": ")
		if err := writeValue(b, m.value, inner); err != nil {
			return err
		}
		if i < len(n)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(indent + "}")
	return nil
}

func writeScalar(b *strings.Builder, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b.Write(raw)
	return nil
}
