package main

import (
	"reflect"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A closed string type's Values set is written by hand, and buzzgen
// derives the mirror's cases from its constants. A constant the set
// leaves out fails here.
func TestBoundaryEnumsMatchTheirSets(t *testing.T) {
	for _, e := range boundaryEnumList() {
		values, ok := reflect.Zero(e.Type).Interface().(interface{ Values() []string })
		if !ok {
			continue
		}
		var mirrored []string
		for _, c := range e.Cases {
			if c.Value != "" {
				mirrored = append(mirrored, c.Value)
			}
		}
		require.NotEmpty(t, mirrored, e.Name)
		assert.Equal(t, values.Values(), mirrored, "%s: the Values set and the type's constants differ", e.Name)
	}
}

func TestBoundaryRegistryLookups(t *testing.T) {
	entry, ok := boundaryTypeNamed("ExecResult")
	require.True(t, ok)
	assert.Equal(t, reflect.TypeFor[types.ExecResult](), entry.Type)

	_, ok = boundaryTypeNamed("NotDeclared")
	assert.False(t, ok)

	// The registry key, not the Go name: types.HTTPResponse mirrors as Response,
	// and emitting the Go name would reference an object nothing declares.
	assert.Equal(t, "Response", buzzName(reflect.TypeFor[types.HTTPResponse]()))
	// An unregistered type falls back to its own name, which an anonymous struct
	// does not have.
	assert.Equal(t, "", buzzName(reflect.TypeFor[struct{}]()))

	e, ok := buzzEnum(reflect.TypeFor[types.TermStyle]())
	require.True(t, ok)
	assert.Equal(t, "TermStyle", e.Name)
	require.NotEmpty(t, e.Cases)

	_, ok = buzzEnum(reflect.TypeFor[string]())
	assert.False(t, ok)
}
