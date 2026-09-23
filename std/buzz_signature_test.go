package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuzzSignature pins the call form docs and `magus describe module` render.
func TestBuzzSignature(t *testing.T) {
	mod := Module{Name: "env"}

	for _, tc := range []struct {
		name   string
		method Method
		want   string
	}{
		{
			name:   "snake_case name camelCases, types render the returns",
			method: Method{Name: "get_or", Args: []Arg{{Name: "name", Type: TypeString}, {Name: "def", Type: TypeString}}, Returns: []Ret{{Type: TypeString}}},
			want:   "env\\getOr(name, def)" + covArrow + "string",
		},
		{
			name:   "several returns are comma-joined",
			method: Method{Name: "lookup", Args: []Arg{{Name: "name", Type: TypeString}}, Returns: []Ret{{Type: TypeString}, {Type: TypeBool}}},
			want:   "env\\lookup(name)" + covArrow + "string, bool",
		},
		{
			name:   "no declared return means no suffix",
			method: Method{Name: "set", Args: []Arg{{Name: "name", Type: TypeString}, {Name: "value", Type: TypeString}}},
			want:   "env\\set(name, value)",
		},
		{
			name:   "BuzzName overrides the camelCase derivation",
			method: Method{Name: "has_charm", BuzzName: "has_charm", Args: []Arg{{Name: "name", Type: TypeString}}, Returns: []Ret{{Type: TypeBool}}},
			want:   "env\\has_charm(name)" + covArrow + "bool",
		},
		{
			name:   "a variadic arg trails dots and is never also bracketed",
			method: Method{Name: "join", Args: []Arg{{Name: "parts", Type: TypeString, Variadic: true, Optional: true}}, Returns: []Ret{{Type: TypeString}}},
			want:   "env\\join(parts...)" + covArrow + "string",
		},
		{
			name:   "an optional arg is bracketed",
			method: Method{Name: "arch", Args: []Arg{{Name: "name", Type: TypeString}, {Name: "style", Type: TypeString, Optional: true}}, Returns: []Ret{{Type: TypeString}}},
			want:   "env\\arch(name, [style])" + covArrow + "string",
		},
		{
			name:   "a named return prints its name",
			method: Method{Name: "size", Returns: []Ret{{Name: "width", Type: TypeInt}, {Name: "height", Type: TypeInt}}},
			want:   "env\\size()" + covArrow + "width, height",
		},
		{
			name:   "an object return prints the object, not the map it marshals to",
			method: Method{Name: "stat", Args: []Arg{{Name: "path", Type: TypeString}}, Returns: []Ret{{Type: TypeAnyMap, Object: "FileInfo"}}},
			want:   "env\\stat(path)" + covArrow + "FileInfo",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, BuzzSignature(mod, tc.method))
		})
	}
}

// TestBuzzMethodName: the declared name and the callable one differ, and only the
// callable one resolves.
func TestBuzzMethodName(t *testing.T) {
	assert.Equal(t, "readFile", BuzzMethodName(Method{Name: "read_file"}))
	assert.Equal(t, "glob", BuzzMethodName(Method{Name: "glob"}))
	assert.Equal(t, "has_charm", BuzzMethodName(Method{Name: "has_charm", BuzzName: "has_charm"}))
}
