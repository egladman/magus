// Package generate is the config generator: it reads the magus config struct and
// renders the flag-binding, schema-field, bind and env artifacts derived from it.
//
// It lives beside the schema it interprets rather than in the tool that invokes it,
// because everything here encodes what internal/config's struct tags MEAN - the
// yaml-path derivation, the MAGUS_* env-var naming, the flag-kind switch. A tag
// added to config.go and a reader that does not know about it are one edit apart,
// and that edit is easier to get right when the two sit in the same directory.
// cmd/magus-utils keeps only the flag parsing that calls [Write].
//
// Note the directory vocabulary: internal/config/ is the source of truth,
// internal/config/generate/ (here) is the generator, internal/config/gen/ is the
// generated output. Generator code never lives in a gen/ directory.
//
// Build-time only, like the rest of the generator pipeline: go/ast has no place in
// a task runner at runtime, and nothing in the magus binary imports this.
package generate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"text/template"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/generate/emit"
)

// Paths names the artifacts to render. An empty path SKIPS that artifact rather
// than defaulting one, so a caller asking for two files cannot silently write four.
type Paths struct {
	Flags    string // the ConfigFlags table + BindConfigFlags
	Fields   string // the schema Fields table
	Bind     string // BindFlags
	ApplyEnv string // ApplyEnv
}

// Write reads the config struct at schema and renders each artifact p names.
func Write(schema string, p Paths) error {
	defs, err := parseConfigFlags(schema)
	if err != nil {
		return err
	}

	if p.Flags != "" {
		if err := emit.GoTemplate(p.Flags, outputTmpl, flagTmplData{Defs: defs}); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "config: wrote %d defs to %s\n", len(defs), p.Flags)
	}

	schemaOutputs := []struct {
		path string
		tmpl *template.Template
		data any
	}{
		{p.Fields, fieldsTmpl, defs},
		{p.Bind, bindTmpl, bindOnlyFields(defs)},
		{p.ApplyEnv, applyEnvTmpl, defs},
	}
	for _, o := range schemaOutputs {
		if o.path == "" {
			continue
		}
		if err := emit.GoTemplate(o.path, o.tmpl, o.data); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "config: wrote %s\n", o.path)
	}
	return nil
}

// flagTmplData bundles defs with derived metadata for the flags template.
type flagTmplData struct {
	Defs []FlagDef
}

// FlagDef describes one config field exposed as a CLI flag.
type FlagDef struct {
	Flag      string // CLI flag long name, e.g. "cache-dir"
	FlagShort string // optional short name, e.g. "c" (from `cli:"short=c"`)
	EnvVar    string // matching MAGUS_* env var, e.g. "MAGUS_CACHE_DIR"
	Kind      string // "string", "int", "bool", or "float64"
	GoPath    string // Go field selector, e.g. "cfg.Cache.Dir"
	YamlPath  string // dotted yaml path, e.g. "cache.dir"
	Usage     string // sanitized one-line description for flag.Usage
}

func parseConfigFlags(configPath string) ([]FlagDef, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, configPath, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", configPath, err)
	}

	structs := map[string]*ast.StructType{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, def := range gd.Specs {
			ts, ok := def.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			structs[ts.Name.Name] = st
		}
	}

	root, ok := structs["Config"]
	if !ok {
		return nil, fmt.Errorf("no Config struct found in %s", configPath)
	}

	var defs []FlagDef
	walkStruct(root, structs, nil, "cfg", &defs)
	return defs, nil
}

// walkStruct recurses through st collecting scalar leaf fields.
func walkStruct(st *ast.StructType, structs map[string]*ast.StructType, yamlPath []string, goBase string, out *[]FlagDef) {
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue // skip embedded fields
		}
		name := field.Names[0].Name
		if !ast.IsExported(name) {
			continue
		}

		yamlTag := yamlTagOf(field)
		if yamlTag == "-" {
			continue
		}
		if yamlTag == "" {
			yamlTag = strings.ToLower(name)
		}

		cliOptOut, cliShort := cliOptions(field)
		if cliOptOut {
			continue
		}

		thisYAML := append(append([]string{}, yamlPath...), yamlTag)
		goSel := goBase + "." + name

		typeName := typeIdentOf(field.Type)
		kind := scalarKind(typeName)

		if kind == "" {
			if nested, ok := structs[typeName]; ok {
				walkStruct(nested, structs, thisYAML, goSel, out)
			}
			// slices, maps, imported types: skip
			continue
		}

		flagName := config.FlagName(thisYAML...)
		if kind == "stringslice" || kind == "boolptr" { // env-only; no CLI flag
			flagName = ""
		}
		envVar := config.EnvName("MAGUS", thisYAML...)
		// The flag help leads with the env var, then the field's doc comment when
		// it has one. A field with no doc shows just the env var, not "ENV: ENV".
		help := envVar
		if usage := sanitizeUsage(firstDocLine(field.Doc)); usage != "" {
			help = envVar + ": " + usage
		}

		*out = append(*out, FlagDef{
			Flag:      flagName,
			FlagShort: cliShort,
			EnvVar:    envVar,
			Kind:      kind,
			GoPath:    goSel,
			YamlPath:  strings.Join(thisYAML, "."),
			Usage:     help,
		})
	}
}

// typeIdentOf returns the type name; "" for unsupported forms.
func typeIdentOf(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if inner, ok := t.X.(*ast.Ident); ok {
			return "*" + inner.Name
		}
		return ""
	case *ast.SelectorExpr:
		if pkg, ok := t.X.(*ast.Ident); ok {
			return pkg.Name + "." + t.Sel.Name
		}
		return ""
	case *ast.ArrayType:
		if inner, ok := t.Elt.(*ast.Ident); ok && inner.Name == "string" {
			return "[]string"
		}
		return ""
	default:
		return ""
	}
}

func scalarKind(t string) string {
	switch t {
	case "string":
		return "string"
	case "int":
		return "int"
	case "bool":
		return "bool"
	case "float64":
		return "float64"
	case "*bool":
		return "boolptr"
	case "time.Duration":
		return "duration"
	case "[]string":
		return "stringslice"
	}
	return ""
}

func yamlTagOf(f *ast.Field) string {
	if f.Tag == nil {
		return ""
	}
	return lookupTag(strings.Trim(f.Tag.Value, "`"), "yaml")
}

// cliOptions parses the `cli:"…"` struct tag ("-" = opt out; "short=c" = short flag).
func cliOptions(f *ast.Field) (optOut bool, short string) {
	if f.Tag == nil {
		return false, ""
	}
	val := lookupTagRaw(strings.Trim(f.Tag.Value, "`"), "cli")
	if val == "" {
		return false, ""
	}
	for _, part := range strings.Split(val, ",") {
		if part == "-" {
			optOut = true
			continue
		}
		if k, v, ok := strings.Cut(part, "="); ok && k == "short" {
			short = v
		}
	}
	return optOut, short
}

// lookupTag returns the tag value up to the first comma (matches reflect.StructTag.Get's yaml convention).
func lookupTag(raw, key string) string {
	val := lookupTagRaw(raw, key)
	if comma := strings.Index(val, ","); comma >= 0 {
		val = val[:comma]
	}
	return val
}

// lookupTagRaw returns the full unstripped tag value for key.
func lookupTagRaw(raw, key string) string {
	search := key + `:"`
	idx := strings.Index(raw, search)
	if idx < 0 {
		return ""
	}
	rest := raw[idx+len(search):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// firstDocLine returns a doc comment's opening sentence, joining gofmt-wrapped
// physical lines so a sentence broken across lines is not truncated mid-clause
// (a plain first-physical-line read was cutting the warning half off sentences
// like the CacheRemote.Insecure field's). Joining stops at the first blank
// line (a new paragraph) or once a sentence-ending period is found.
func firstDocLine(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	var prose []string
	for _, c := range cg.List {
		line := strings.TrimPrefix(c.Text, "// ")
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimSpace(line)
		if line == "" {
			if len(prose) > 0 {
				break
			}
			continue
		}
		prose = append(prose, line)
	}
	return firstSentence(strings.Join(prose, " "))
}

// firstSentence returns s up to and including the first period that ends a
// sentence (followed by a space or end of string), godoc-style; a period
// inside a token like "e.g." or "-cache-remote-insecure" is not a sentence
// end. Mirrors internal/describe/extract.go's helper of the same name.
func firstSentence(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && (i == len(s)-1 || s[i+1] == ' ') {
			return s[:i+1]
		}
	}
	return s
}

// sanitizeUsage strips quotes/backticks from s and truncates to 120 chars for safe Go string literals.
func sanitizeUsage(s string) string {
	s = strings.ReplaceAll(s, `"`, `'`)
	s = strings.ReplaceAll(s, "`", "'")
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}

var outputTmpl = template.Must(template.New("flags").Parse(`// Code generated by magus-utils config; DO NOT EDIT.
package gen

import (
	"flag"

	"github.com/egladman/magus/internal/config"
)

// ConfigFlag documents one config field exposed as a CLI flag.
type ConfigFlag struct {
	Flag   string // CLI flag name, e.g. "cache-dir"
	EnvVar string // matching MAGUS_* env var
	Kind   string // "string", "int", "bool", "float64", "duration", or "boolptr"
}

// ConfigFlags is the generated inventory of every config-backed flag.
// Consumed by magus doctor (env-var typo detection) and the man-page generator.
// Do not edit by hand — regenerate with: go generate ./cmd/magus/...
var ConfigFlags = []ConfigFlag{
{{- range .Defs}}
	{"{{.Flag}}", "{{.EnvVar}}", "{{.Kind}}"},
{{- end}}
}

// BindConfigFlags registers one CLI flag per config field on fs, storing
// values directly into cfg. Call after config.Load so defaults are already
// applied; flag.Parse will override only the flags the user explicitly passes.
//
// Flags owned by bindGlobalFlags (--concurrency, --output, -v) are excluded
// here to avoid duplicate flag registration on the same FlagSet.
// Fields with kind "boolptr" use three-way nil/true/false semantics and are
// env-only; they are omitted here to avoid losing the nil state via flag.
func BindConfigFlags(fs *flag.FlagSet, cfg *config.Config) {
{{- range .Defs}}
{{- if eq .Kind "string"}}
	fs.StringVar(&{{.GoPath}}, "{{.Flag}}", {{.GoPath}}, "{{.Usage}}")
{{- else if eq .Kind "int"}}
	fs.IntVar(&{{.GoPath}}, "{{.Flag}}", {{.GoPath}}, "{{.Usage}}")
{{- else if eq .Kind "bool"}}
	fs.BoolVar(&{{.GoPath}}, "{{.Flag}}", {{.GoPath}}, "{{.Usage}}")
{{- else if eq .Kind "float64"}}
	fs.Float64Var(&{{.GoPath}}, "{{.Flag}}", {{.GoPath}}, "{{.Usage}}")
{{- else if eq .Kind "duration"}}
	fs.DurationVar(&{{.GoPath}}, "{{.Flag}}", {{.GoPath}}, "{{.Usage}}")
{{- end}}
{{- end}}
}
`))
