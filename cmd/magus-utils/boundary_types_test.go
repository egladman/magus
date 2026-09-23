package main

import (
	"reflect"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoundaryRegistryLookups(t *testing.T) {
	entry, ok := boundaryTypeNamed("ExecResult")
	require.True(t, ok)
	assert.Equal(t, reflect.TypeFor[types.ExecResult](), entry.Type)

	_, ok = boundaryTypeNamed("NotDeclared")
	assert.False(t, ok)

	// The registry key, not the Go name: types.HTTPResponse mirrors as HttpResponse,
	// and emitting the Go name would reference an object nothing declares.
	assert.Equal(t, "HttpResponse", buzzName(reflect.TypeFor[types.HTTPResponse]()))
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
