package figure

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
	"strings"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceIsTheFigureModule(t *testing.T) {
	assert.True(t, strings.Contains(Source, "\nnamespace figure;\n"), "figure.buzz declares the figure namespace")
	assert.True(t, strings.Contains(Source, "\nexport fun of(id: str,"), "of() is the module's entry point")
	assert.True(t, strings.Contains(Source, "\nexport fun draw(f: Figure, theme: Theme,"), "draw() paints a Figure")
}

func TestSourceSHA256IsTheFileOnDisk(t *testing.T) {
	disk, err := os.ReadFile("figure.buzz")
	require.NoError(t, err)
	sum := sha256.Sum256(disk)
	assert.Equal(t, hex.EncodeToString(sum[:]), SourceSHA256())
}

// hostless names the record fields a host has no value for, so the Go mirror leaves them
// out; Draw hands Buzz null for each.
var hostless = map[string]string{
	"Box.symbol": "a magus\\refs result, which no host holds",
}

// TestMirrorMatchesTheModule walks every record Figure reaches in the module's own
// declarations, and magus\Dir's in the generated magus declarations, and holds the Go
// mirror to each field's name and type.
func TestMirrorMatchesTheModule(t *testing.T) {
	magusDecls, err := os.ReadFile("../../internal/spell/gen/decls/magus.buzz")
	require.NoError(t, err)
	m := mirror{t: t, figure: declared(t, Source), magus: declared(t, string(magusDecls)), seen: map[string]bool{}}
	m.record(m.figure.objects["Figure"], reflect.TypeFor[Figure](), "Figure", m.figure)
	assert.Contains(t, m.seen, "Dir", "the walk reaches magus\\Dir")
	assert.Contains(t, m.seen, "Legend", "the walk reaches every record Figure holds")
}

type declarations struct {
	objects map[string]*ast.ObjectDecl
	enums   map[string]bool
}

func declared(t *testing.T, src string) declarations {
	t.Helper()
	prog, err := buzz.ParseEmbedded(src)
	require.NoError(t, err)
	d := declarations{objects: map[string]*ast.ObjectDecl{}, enums: map[string]bool{}}
	for _, stmt := range prog.Stmts {
		switch n := stmt.(type) {
		case *ast.ObjectDecl:
			if n.IsExported {
				d.objects[n.Name] = n
			}
		case *ast.EnumDecl:
			if n.IsExported {
				d.enums[n.Name] = true
			}
		}
	}
	return d
}

type mirror struct {
	t      *testing.T
	figure declarations
	magus  declarations
	seen   map[string]bool
}

// record holds got to obj, whose field types resolve among in's declarations.
func (m mirror) record(obj *ast.ObjectDecl, got reflect.Type, at string, in declarations) {
	require.NotNil(m.t, obj, "%s: the module declares no such exported record", at)
	require.Equal(m.t, reflect.Struct, got.Kind(), "%s: mirrored by %s", at, got)
	assert.Equal(m.t, obj.Name, got.Name(), "%s: the Go mirror takes the record's name", at)
	if m.seen[obj.Name] {
		return
	}
	m.seen[obj.Name] = true
	fields := map[string]reflect.StructField{}
	for i := range got.NumField() {
		f := got.Field(i)
		fields[strings.Split(f.Tag.Get("json"), ",")[0]] = f
	}
	for _, f := range obj.Fields {
		where := obj.Name + "." + f.Name
		gf, ok := fields[f.Name]
		delete(fields, f.Name)
		if _, skip := hostless[where]; skip {
			assert.False(m.t, ok, "%s is hostless, so the Go mirror leaves it out", where)
			continue
		}
		if !assert.True(m.t, ok, "the Go mirror %s lacks %s (%s)", got.Name(), where, f.TypeAnnot) {
			continue
		}
		m.typed(f.TypeAnnot, gf.Type, where, in)
	}
	for name := range fields {
		assert.Fail(m.t, "the Go mirror has a field the record lacks", "%s.%s", got.Name(), name)
	}
}

// typed holds got to a Buzz type annotation: T? is a pointer, [T] a slice, an enum a named
// string of the enum's name, a record the struct of its name.
func (m mirror) typed(annot string, got reflect.Type, at string, in declarations) {
	annot = strings.TrimSpace(annot)
	switch {
	case strings.HasSuffix(annot, "?"):
		if assert.Equal(m.t, reflect.Pointer, got.Kind(), "%s is %s", at, annot) {
			m.typed(strings.TrimSuffix(annot, "?"), got.Elem(), at, in)
		}
	case strings.HasPrefix(annot, "[") && strings.HasSuffix(annot, "]"):
		if assert.Equal(m.t, reflect.Slice, got.Kind(), "%s is %s", at, annot) {
			m.typed(annot[1:len(annot)-1], got.Elem(), at, in)
		}
	case annot == "str":
		assert.Equal(m.t, reflect.TypeFor[string](), got, at)
	case annot == "bool":
		assert.Equal(m.t, reflect.TypeFor[bool](), got, at)
	case annot == "int":
		assert.Equal(m.t, reflect.TypeFor[int](), got, at)
	case in.enums[annot]:
		assert.Equal(m.t, reflect.String, got.Kind(), at)
		assert.Equal(m.t, annot, got.Name(), "%s: an enum's case name is typed by the enum", at)
	case strings.HasPrefix(annot, `magus\`):
		name := strings.TrimPrefix(annot, `magus\`)
		m.record(m.magus.objects[name], got, at+" ("+annot+")", m.magus)
	default:
		m.record(in.objects[annot], got, at+" ("+annot+")", in)
	}
}
