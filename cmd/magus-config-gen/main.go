// Command magus-config-gen generates config_flags.go and env_names.go from the magus config struct.
// Invoked by go:generate in cmd/magus/main.go; run via go run ../magus-config-gen; never linked into the binary.
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

func main() {
	configPath := flag.String("config", "../../internal/config/config.go", "Path to config.go")
	outPath := flag.String("out", "", "Flag-binding output file path (skip when empty)")
	fieldsOut := flag.String("fields-out", "", "Generated Fields-table output path (skip when empty)")
	bindOut := flag.String("bind-out", "", "Generated BindFlags output path (skip when empty)")
	applyEnvOut := flag.String("apply-env-out", "", "Generated ApplyEnv output path (skip when empty)")
	flag.Parse()

	specs, err := parseConfigSpecs(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config/flag: %v\n", err)
		os.Exit(1)
	}

	if *outPath != "" {
		flagData := flagTmplData{Specs: specs}
		if err := writeTemplate(*outPath, outputTmpl, flagData); err != nil {
			fmt.Fprintf(os.Stderr, "config/flag: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "config/flag: wrote %d specs to %s\n", len(specs), *outPath)
	}

	schemaOutputs := []struct {
		path string
		tmpl *template.Template
		data any
	}{
		{*fieldsOut, fieldsTmpl, specs},
		{*bindOut, bindTmpl, bindOnlyFields(specs)},
		{*applyEnvOut, applyEnvTmpl, specs},
	}
	for _, o := range schemaOutputs {
		if o.path == "" {
			continue
		}
		if err := writeTemplate(o.path, o.tmpl, o.data); err != nil {
			fmt.Fprintf(os.Stderr, "config/flag: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "config/flag: wrote %s\n", o.path)
	}
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

// flagTmplData bundles specs with derived metadata for the flags template.
type flagTmplData struct {
	Specs []FlagSpec
}

// FlagSpec describes one config field exposed as a CLI flag.
type FlagSpec struct {
	Flag      string // CLI flag long name, e.g. "cache-dir"
	FlagShort string // optional short name, e.g. "c" (from `cli:"short=c"`)
	EnvVar    string // matching MAGUS_* env var, e.g. "MAGUS_CACHE_DIR"
	Kind      string // "string", "int", "bool", or "float64"
	GoPath    string // Go field selector, e.g. "cfg.Cache.Dir"
	YamlPath  string // dotted yaml path, e.g. "cache.dir"
	Usage     string // sanitized one-line description for flag.Usage
}

func parseConfigSpecs(configPath string) ([]FlagSpec, error) {
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
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
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

	var specs []FlagSpec
	walkStruct(root, structs, nil, "cfg", &specs)
	return specs, nil
}

// walkStruct recurses through st collecting scalar leaf fields.
func walkStruct(st *ast.StructType, structs map[string]*ast.StructType, yamlPath []string, goBase string, out *[]FlagSpec) {
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

		*out = append(*out, FlagSpec{
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

var outputTmpl = template.Must(template.New("flags").Parse(`// Code generated by cmd/magus-config-gen; DO NOT EDIT.
package gen

import (
	"flag"

	"github.com/egladman/magus/internal/config"
)

// ConfigFlagSpec documents one config field exposed as a CLI flag.
type ConfigFlagSpec struct {
	Flag   string // CLI flag name, e.g. "cache-dir"
	EnvVar string // matching MAGUS_* env var
	Kind   string // "string", "int", "bool", "float64", "duration", or "boolptr"
}

// ConfigFlagSpecs is the generated inventory of every config-backed flag.
// Consumed by magus doctor (env-var typo detection) and the man-page generator.
// Do not edit by hand — regenerate with: go generate ./cmd/magus/...
var ConfigFlagSpecs = []ConfigFlagSpec{
{{- range .Specs}}
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
{{- range .Specs}}
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
