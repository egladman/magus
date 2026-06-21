// Subcommand `config` generates config_flags.go and the schema/bind/env files
// from the magus config struct. It parses internal/config/config.go and never
// links into the binary.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"text/template"

	"github.com/egladman/magus/internal/config"
)

func runConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	configPath := fs.String("config", "../../internal/config/config.go", "Path to config.go")
	outPath := fs.String("out", "", "Flag-binding output file path (skip when empty)")
	fieldsOut := fs.String("fields-out", "", "Generated Fields-table output path (skip when empty)")
	bindOut := fs.String("bind-out", "", "Generated BindFlags output path (skip when empty)")
	applyEnvOut := fs.String("apply-env-out", "", "Generated ApplyEnv output path (skip when empty)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	defs, err := parseConfigFlags(*configPath)
	if err != nil {
		return err
	}

	if *outPath != "" {
		flagData := flagTmplData{Defs: defs}
		if err := writeTemplate(*outPath, outputTmpl, flagData); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "config: wrote %d defs to %s\n", len(defs), *outPath)
	}

	schemaOutputs := []struct {
		path string
		tmpl *template.Template
		data any
	}{
		{*fieldsOut, fieldsTmpl, defs},
		{*bindOut, bindTmpl, bindOnlyFields(defs)},
		{*applyEnvOut, applyEnvTmpl, defs},
	}
	for _, o := range schemaOutputs {
		if o.path == "" {
			continue
		}
		if err := writeTemplate(o.path, o.tmpl, o.data); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "config: wrote %s\n", o.path)
	}
	return nil
}

func writeTemplate(path string, tmpl *template.Template, data any) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("template %s: %w", path, err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("gofmt %s: %w", path, err)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
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
		usage := sanitizeUsage(firstDocLine(field.Doc))
		if usage == "" {
			usage = envVar
		}

		*out = append(*out, FlagDef{
			Flag:      flagName,
			FlagShort: cliShort,
			EnvVar:    envVar,
			Kind:      kind,
			GoPath:    goSel,
			YamlPath:  strings.Join(thisYAML, "."),
			Usage:     envVar + ": " + usage,
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

func firstDocLine(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	for _, c := range cg.List {
		line := strings.TrimPrefix(c.Text, "// ")
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
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

var outputTmpl = template.Must(template.New("flags").Parse(`// Code generated by magus-scribe config; DO NOT EDIT.
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
