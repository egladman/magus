// Subcommand `jobschema` emits the published JSON Schema for each job record from
// the Go struct that decodes it: internal/job/gen/job.schema.json from job.Declaration,
// result.schema.json from job.Report.
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
	Struct  string
	Title   string
	File    string
	Version string // the constant holding this record's schema_version
}

var jobRecords = []jobRecord{
	{Struct: "Declaration", Title: "magus job", File: "job.schema.json", Version: "JobSchemaVersion"},
	{Struct: "JobResult", Title: "magus job result", File: "result.schema.json", Version: "ResultSchemaVersion"},
}

// jobSources are the files the records and every type they reach are declared in.
var jobSources = []string{
	"internal/job/decode.go", "internal/job/verify.go",
	"types/job.go", "types/jobresult.go",
}

const (
	schemaDraft = "http://json-schema.org/draft-07/schema#"
	schemaIDs   = "https://magus.invalid/job/"
	// versionProperty is the one property whose value is fixed rather than described.
	versionProperty = "schema_version"
	// leaseIDMarker is the `schema:"leaseid"` tag: this field holds a lease id, so the
	// pattern and length types.ValidJobID enforces are published with it.
	leaseIDMarker = "leaseid"
)

// closedSets are the named types whose values are a published vocabulary, keyed by the
// name a field is declared with. The values come from the package that owns the set, so
// a state added there reaches the schema with nothing here to change.
var closedSets = map[string][]string{"JobState": leaseStateNames()}

func runJobSchema(args []string) error {
	fs := flag.NewFlagSet("jobschema", flag.ExitOnError)
	out := fs.String("out", "internal/job/gen", "directory the schema files are written into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	decls, err := loadGoDecls(jobSources...)
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
//   - schema_version carries the const its package declares;
//   - a field typed by a closed set carries that set as an enum, plus "" when the field
//     is omitempty and an absent value is therefore legal;
//   - a field marked `schema:"leaseid"` carries the pattern and maxLength the id
//     validator enforces;
//   - a description is the doc comment's first paragraph, so the rationale a later
//     paragraph carries stays in the source.
func renderJobSchema(rec jobRecord, d *goDecls) ([]byte, error) {
	st, ok := d.structs[rec.Struct]
	if !ok {
		return nil, fmt.Errorf("no struct %s is declared in %s", rec.Struct, strings.Join(jobSources, ", "))
	}
	version, ok := d.consts[rec.Version]
	if !ok {
		return nil, fmt.Errorf("%s: no constant %s is declared in %s", rec.Struct, rec.Version, strings.Join(jobSources, ", "))
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
func properties(structName string, st *ast.StructType, d *goDecls, version int) (node, []string, error) {
	var props node
	required := []string{}
	for _, f := range st.Fields.List {
		if len(f.Names) != 1 {
			return nil, nil, fmt.Errorf("%s: an embedded or multi-name field has no single json name", structName)
		}
		field := f.Names[0].Name
		tag := ""
		if f.Tag != nil {
			tag = f.Tag.Value
		}
		name, opts, _ := strings.Cut(godecl.TagRaw(tag, "json"), ",")
		switch name {
		case "-":
			continue
		case "":
			return nil, nil, fmt.Errorf("%s.%s carries no json tag, so no schema can name it", structName, field)
		}
		omitempty := slices.Contains(strings.Split(opts, ","), "omitempty")

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

func property(f *ast.Field, structName, field, name string, omitempty bool, d *goDecls, version int) (node, error) {
	prop, err := typeMembers(f.Type, omitempty, d, version)
	if err != nil {
		return nil, fmt.Errorf("%s.%s: %w", structName, field, err)
	}
	if name == versionProperty {
		prop = append(prop, member{"const", version})
	}
	tag := ""
	if f.Tag != nil {
		tag = f.Tag.Value
	}
	if godecl.Tag(tag, "schema") == leaseIDMarker {
		prop = append(prop, member{"pattern", leaseIDPattern()}, member{"maxLength", types.MaxJobIDLen})
	}
	if doc := d.docs[structName+"."+field]; doc != "" {
		prop = append(prop, member{"description", doc})
	}
	return prop, nil
}

// typeMembers is the JSON shape of one Go type.
func typeMembers(expr ast.Expr, omitempty bool, d *goDecls, version int) (node, error) {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return typeMembers(t.X, omitempty, d, version)
	case *ast.ArrayType:
		items, err := typeMembers(t.Elt, false, d, version)
		if err != nil {
			return nil, err
		}
		return node{{"type", "array"}, {"items", items}}, nil
	case *ast.SelectorExpr:
		return namedType(t.Sel.Name, omitempty, d, version)
	case *ast.Ident:
		switch t.Name {
		case "string":
			return node{{"type", "string"}}, nil
		case "int", "int64":
			return node{{"type", "integer"}}, nil
		case "bool":
			return node{{"type", "boolean"}}, nil
		}
		return namedType(t.Name, omitempty, d, version)
	}
	return nil, fmt.Errorf("%T is not a type this generator can publish", expr)
}

func namedType(name string, omitempty bool, d *goDecls, version int) (node, error) {
	if set, ok := closedSets[name]; ok {
		values := set
		if omitempty {
			values = append([]string{""}, set...)
		}
		return node{{"type", "string"}, {"enum", values}}, nil
	}
	st, ok := d.structs[name]
	if !ok {
		return nil, fmt.Errorf("type %s is neither a struct nor a closed set these sources declare", name)
	}
	props, required, err := properties(name, st, d, version)
	if err != nil {
		return nil, err
	}
	return node{
		{"type", "object"},
		{"additionalProperties", false},
		{"required", required},
		{"properties", props},
	}, nil
}

// goDecls is what the sources declare: every struct, the first paragraph of every type
// and field doc comment, and every integer constant.
type goDecls struct {
	structs map[string]*ast.StructType
	docs    map[string]string
	consts  map[string]int
}

func loadGoDecls(paths ...string) (*goDecls, error) {
	d := &goDecls{
		structs: map[string]*ast.StructType{},
		docs:    map[string]string{},
		consts:  map[string]int{},
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
				d.docs[s.Name.Name+"."+name.Name] = firstParagraph(f.Doc.Text())
			}
		}
	case *ast.ValueSpec:
		if gd.Tok != token.CONST || len(s.Names) != 1 || len(s.Values) != 1 {
			return
		}
		lit, ok := s.Values[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return
		}
		if v, err := strconv.Atoi(lit.Value); err == nil {
			d.consts[s.Names[0].Name] = v
		}
	}
}

// firstParagraph is a doc comment's summary, on one line.
func firstParagraph(doc string) string {
	head, _, _ := strings.Cut(strings.TrimSpace(doc), "\n\n")
	return strings.Join(strings.Fields(head), " ")
}

func leaseStateNames() []string {
	states := types.JobStates()
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = string(s)
	}
	return names
}

// leaseIDPattern derives the id charset by asking types.ValidJobID about every ASCII
// rune, so the published pattern cannot say something other than what the store enforces.
// Probing ASCII alone is enough because the validator accepts nothing above it.
func leaseIDPattern() string {
	var accepted []rune
	for r := rune(0); r < 0x80; r++ {
		if types.ValidJobID(string(r)) {
			accepted = append(accepted, r)
		}
	}
	return "^[" + charClass(accepted) + "]+$"
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
			b.WriteString(string(r) + "-" + string(accepted[end]))
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
