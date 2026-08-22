package generate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaFixture is a stand-in for internal/config/config.go carrying one field
// per branch the walk has to decide: every scalar kind, both `cli` options, a
// nested struct, and the shapes that must be skipped. "@" stands in for a
// backtick so the struct tags can live in a raw string literal.
const schemaFixture = `package config

import "time"

type Config struct {
	hidden string
	Embedded
	// Where state lives.
	StateDir string @yaml:"state_dir" cli:"short=s"@
	Secret   string @yaml:"-"@
	// Not a flag.
	Internal string @yaml:"internal" cli:"-"@
	Retries  int
	Ratio    float64
	Verbose  bool
	Timeout  time.Duration @yaml:"timeout"@
	Strict   *bool         @yaml:"strict"@
	Charms   []string      @yaml:"default_charms"@
	// A "quoted" and @backticked@ description that runs well past the one hundred and twenty characters the generator allows itself for flag usage.
	Long  string @yaml:"long"@
	Tags  map[string]string
	Ports []int
	Ptr   *Nested
	Where somepkg.Thing
	Cache CacheConfig @yaml:"cache"@
}

type CacheConfig struct {
	// Directory holding cached artifacts.
	Dir string @yaml:"dir"@
}

type Embedded struct {
	X string
}
`

// writeSchema materializes schemaFixture and returns its path.
func writeSchema(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.go")
	require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(schemaFixture, "@", "`")), 0o644))
	return path
}

func defByYamlPath(defs []FlagDef, yamlPath string) (FlagDef, bool) {
	for _, d := range defs {
		if d.YamlPath == yamlPath {
			return d, true
		}
	}
	return FlagDef{}, false
}

// TestParseConfigFlagsDerivesEveryScalarKind pins what a struct tag MEANS: the
// yaml path, the MAGUS_* name derived from it, the flag name, and the kind that
// decides which binder the templates emit. A tag added to config.go that this
// walk does not understand is silently absent from every artifact, so the
// mapping is asserted field by field rather than by counting defs.
func TestParseConfigFlagsDerivesEveryScalarKind(t *testing.T) {
	defs, err := parseConfigFlags(writeSchema(t))
	require.NoError(t, err)

	for _, want := range []FlagDef{
		{Flag: "state-dir", FlagShort: "s", EnvVar: "MAGUS_STATE_DIR", Kind: "string", GoPath: "cfg.StateDir", YamlPath: "state_dir", Usage: "MAGUS_STATE_DIR: Where state lives."},
		{Flag: "retries", EnvVar: "MAGUS_RETRIES", Kind: "int", GoPath: "cfg.Retries", YamlPath: "retries", Usage: "MAGUS_RETRIES"},
		{Flag: "ratio", EnvVar: "MAGUS_RATIO", Kind: "float64", GoPath: "cfg.Ratio", YamlPath: "ratio", Usage: "MAGUS_RATIO"},
		{Flag: "verbose", EnvVar: "MAGUS_VERBOSE", Kind: "bool", GoPath: "cfg.Verbose", YamlPath: "verbose", Usage: "MAGUS_VERBOSE"},
		{Flag: "timeout", EnvVar: "MAGUS_TIMEOUT", Kind: "duration", GoPath: "cfg.Timeout", YamlPath: "timeout", Usage: "MAGUS_TIMEOUT"},
		{Flag: "cache-dir", EnvVar: "MAGUS_CACHE_DIR", Kind: "string", GoPath: "cfg.Cache.Dir", YamlPath: "cache.dir", Usage: "MAGUS_CACHE_DIR: Directory holding cached artifacts."},
	} {
		got, ok := defByYamlPath(defs, want.YamlPath)
		require.True(t, ok, "no def for yaml path %q", want.YamlPath)
		assert.Equal(t, want, got)
	}
}

// TestEnvOnlyKindsCarryNoFlagName guards the rule the templates depend on: a
// *bool keeps three-way nil semantics and a []string has no flag syntax, so both
// are env-only and their Flag must be empty. A non-empty one here would register
// a flag that destroys the state it is meant to preserve.
func TestEnvOnlyKindsCarryNoFlagName(t *testing.T) {
	defs, err := parseConfigFlags(writeSchema(t))
	require.NoError(t, err)

	strict, ok := defByYamlPath(defs, "strict")
	require.True(t, ok)
	assert.Equal(t, FlagDef{EnvVar: "MAGUS_STRICT", Kind: "boolptr", GoPath: "cfg.Strict", YamlPath: "strict", Usage: "MAGUS_STRICT"}, strict)

	charms, ok := defByYamlPath(defs, "default_charms")
	require.True(t, ok)
	assert.Equal(t, FlagDef{EnvVar: "MAGUS_DEFAULT_CHARMS", Kind: "stringslice", GoPath: "cfg.Charms", YamlPath: "default_charms", Usage: "MAGUS_DEFAULT_CHARMS"}, charms)
}

// TestParseConfigFlagsSkipsWhatItCannotBind names each exclusion, because every
// one of them is a field a reader could reasonably expect to find a flag for.
func TestParseConfigFlagsSkipsWhatItCannotBind(t *testing.T) {
	defs, err := parseConfigFlags(writeSchema(t))
	require.NoError(t, err)

	for _, skipped := range []struct{ why, yamlPath string }{
		{"unexported fields are not part of the config surface", "hidden"},
		{"an embedded field has no name to derive a path from", "x"},
		{`yaml:"-" opts the field out of the file entirely`, "secret"},
		{`cli:"-" opts the field out of the CLI`, "internal"},
		{"a map has no flag syntax", "tags"},
		{"only []string is supported, not []int", "ports"},
		{"a pointer to a struct is not a scalar and not walkable", "ptr"},
		{"an imported non-duration type is opaque here", "where"},
	} {
		_, ok := defByYamlPath(defs, skipped.yamlPath)
		assert.False(t, ok, "%s: %s", skipped.yamlPath, skipped.why)
	}
}

// TestUsageIsSafeForAGoStringLiteral guards the templates, which interpolate
// Usage into a quoted Go string with no escaping of their own: a doc comment
// carrying a quote or a backtick would otherwise emit source that will not
// compile.
func TestUsageIsSafeForAGoStringLiteral(t *testing.T) {
	defs, err := parseConfigFlags(writeSchema(t))
	require.NoError(t, err)

	long, ok := defByYamlPath(defs, "long")
	require.True(t, ok)
	assert.NotContains(t, long.Usage, `"`)
	assert.NotContains(t, long.Usage, "`")
	assert.Len(t, long.Usage, len("MAGUS_LONG: ")+120)
	assert.True(t, strings.HasSuffix(long.Usage, "..."), "a truncated usage says so: %q", long.Usage)
}

func TestParseConfigFlagsErrors(t *testing.T) {
	_, err := parseConfigFlags(filepath.Join(t.TempDir(), "missing.go"))
	assert.ErrorContains(t, err, "parse ")

	// Every declaration form the collector has to step over on its way to a
	// Config it will not find: a func, a const, and a type that is not a struct.
	noConfig := filepath.Join(t.TempDir(), "config.go")
	src := "package config\n\nfunc f() {}\n\nconst X = 1\n\ntype Alias = int\n\ntype Other struct{}\n"
	require.NoError(t, os.WriteFile(noConfig, []byte(src), 0o644))
	_, err = parseConfigFlags(noConfig)
	assert.ErrorContains(t, err, "no Config struct found")
}

// TestWriteRendersParsableGoForEveryArtifact is the generator's real contract:
// four files, each valid Go. emit.Go gofmts before writing, so an unparsable
// template fails Write - but nothing checks that the RIGHT binder reached each
// field, which is what the per-kind assertions below do.
func TestWriteRendersParsableGoForEveryArtifact(t *testing.T) {
	out := t.TempDir()
	p := Paths{
		Flags:    filepath.Join(out, "flags.go"),
		Fields:   filepath.Join(out, "fields.go"),
		Bind:     filepath.Join(out, "bind.go"),
		ApplyEnv: filepath.Join(out, "env.go"),
	}
	require.NoError(t, Write(writeSchema(t), p))

	for _, path := range []string{p.Flags, p.Fields, p.Bind, p.ApplyEnv} {
		_, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		assert.NoError(t, err, "%s is not parsable Go", filepath.Base(path))
	}

	flags := read(t, p.Flags)
	assert.Contains(t, flags, `{"state-dir", "MAGUS_STATE_DIR", "string"},`)
	assert.Contains(t, flags, `fs.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "MAGUS_TIMEOUT")`)
	assert.NotContains(t, flags, "cfg.Strict", "boolptr fields are env-only and must not be bound as flags")

	fields := read(t, p.Fields)
	assert.Contains(t, fields, `GoPath:   "StateDir",`)
	assert.Contains(t, fields, `Flag:     fieldtype.FlagNames{Long: "state-dir", Short: "s"},`)
	assert.Contains(t, fields, "Kind:     fieldtype.KindBoolPtr,")
	assert.Contains(t, fields, "Kind:     fieldtype.KindStringSlice,")

	bind := read(t, p.Bind)
	assert.Contains(t, bind, `fs.StringVar(&cfg.StateDir, "s", cfg.StateDir, "Short for --state-dir")`)
	assert.NotContains(t, bind, "cfg.Strict")

	env := read(t, p.ApplyEnv)
	assert.Contains(t, env, `if v := getenv("MAGUS_RETRIES"); v != ""`)
	assert.Contains(t, env, "cfg.Strict = &b")
	assert.Contains(t, env, `strings.Split(v, ",")`)
}

// TestWriteSkipsUnnamedArtifacts pins Paths' documented behavior: an empty path
// writes nothing rather than defaulting to a location the caller never named.
func TestWriteSkipsUnnamedArtifacts(t *testing.T) {
	out := t.TempDir()
	fields := filepath.Join(out, "fields.go")
	require.NoError(t, Write(writeSchema(t), Paths{Fields: fields}))

	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, filepath.Base(fields), entries[0].Name())
}

func TestWritePropagatesAnUnwritableTarget(t *testing.T) {
	nested := filepath.Join(t.TempDir(), "no-such-dir", "flags.go")
	schema := writeSchema(t)

	assert.Error(t, Write(schema, Paths{Flags: nested}))
	assert.Error(t, Write(schema, Paths{Fields: nested}))
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func TestBindOnlyFieldsDropsBoolPtr(t *testing.T) {
	got := bindOnlyFields([]FlagDef{
		{Flag: "a", Kind: "string"},
		{Flag: "b", Kind: "boolptr"},
		{Flag: "c", Kind: "duration"},
	})
	assert.Equal(t, []FlagDef{{Flag: "a", Kind: "string"}, {Flag: "c", Kind: "duration"}}, got)
}

func TestTypeIdentOf(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr ast.Expr
		want string
	}{
		{"ident", &ast.Ident{Name: "string"}, "string"},
		{"pointer to ident", &ast.StarExpr{X: &ast.Ident{Name: "bool"}}, "*bool"},
		{"pointer to anything else", &ast.StarExpr{X: &ast.ArrayType{Elt: &ast.Ident{Name: "byte"}}}, ""},
		{"qualified ident", &ast.SelectorExpr{X: &ast.Ident{Name: "time"}, Sel: &ast.Ident{Name: "Duration"}}, "time.Duration"},
		{"selector on a non-ident", &ast.SelectorExpr{X: &ast.SelectorExpr{X: &ast.Ident{Name: "a"}, Sel: &ast.Ident{Name: "b"}}, Sel: &ast.Ident{Name: "C"}}, ""},
		{"string slice", &ast.ArrayType{Elt: &ast.Ident{Name: "string"}}, "[]string"},
		{"slice of anything else", &ast.ArrayType{Elt: &ast.Ident{Name: "int"}}, ""},
		{"unsupported form", &ast.MapType{Key: &ast.Ident{Name: "string"}, Value: &ast.Ident{Name: "string"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, typeIdentOf(tc.expr))
		})
	}
}

func TestScalarKind(t *testing.T) {
	for in, want := range map[string]string{
		"string":        "string",
		"int":           "int",
		"bool":          "bool",
		"float64":       "float64",
		"*bool":         "boolptr",
		"time.Duration": "duration",
		"[]string":      "stringslice",
		"int64":         "",
		"":              "",
	} {
		assert.Equal(t, want, scalarKind(in), "scalarKind(%q)", in)
	}
}

// TestLookupTagRawToleratesMalformedTags matters because these two helpers parse
// the tag by hand rather than through reflect.StructTag: a tag the parser cannot
// find must yield "" rather than a slice of the neighbouring tag's value.
func TestLookupTagRawToleratesMalformedTags(t *testing.T) {
	assert.Equal(t, "cache_dir", lookupTag(`yaml:"cache_dir,omitempty" cli:"short=c"`, "yaml"))
	assert.Equal(t, "cache_dir,omitempty", lookupTagRaw(`yaml:"cache_dir,omitempty"`, "yaml"))
	assert.Equal(t, "", lookupTag(`yaml:"x"`, "json"), "an absent key yields nothing")
	assert.Equal(t, "", lookupTagRaw(`yaml:"unterminated`, "yaml"), "an unclosed quote yields nothing")
}

func TestCliOptions(t *testing.T) {
	field := func(tag string) *ast.Field {
		if tag == "" {
			return &ast.Field{}
		}
		return &ast.Field{Tag: &ast.BasicLit{Value: "`" + tag + "`"}}
	}

	optOut, short := cliOptions(field(""))
	assert.False(t, optOut)
	assert.Equal(t, "", short)

	optOut, short = cliOptions(field(`yaml:"x"`))
	assert.False(t, optOut)
	assert.Equal(t, "", short)

	optOut, short = cliOptions(field(`cli:"short=c"`))
	assert.False(t, optOut)
	assert.Equal(t, "c", short)

	optOut, short = cliOptions(field(`cli:"-,short=c"`))
	assert.True(t, optOut)
	assert.Equal(t, "c", short, "opting out and naming a short flag are independent")
}

func TestYamlTagOf(t *testing.T) {
	assert.Equal(t, "", yamlTagOf(&ast.Field{}))
	assert.Equal(t, "dir", yamlTagOf(&ast.Field{Tag: &ast.BasicLit{Value: "`yaml:\"dir\"`"}}))
}

func TestFirstDocLine(t *testing.T) {
	assert.Equal(t, "", firstDocLine(nil))
	assert.Equal(t, "", firstDocLine(&ast.CommentGroup{List: []*ast.Comment{{Text: "//"}, {Text: "//  "}}}))
	assert.Equal(t, "Second line wins.", firstDocLine(&ast.CommentGroup{List: []*ast.Comment{
		{Text: "//"},
		{Text: "// Second line wins."},
		{Text: "// Third is ignored."},
	}}))
}

func TestSanitizeUsage(t *testing.T) {
	assert.Equal(t, "a 'quoted' and 'backticked' note", sanitizeUsage("a \"quoted\" and `backticked` note"))
	assert.Equal(t, "", sanitizeUsage(""))

	long := sanitizeUsage(strings.Repeat("x", 200))
	assert.Len(t, long, 120)
	assert.Equal(t, strings.Repeat("x", 117)+"...", long)
}
