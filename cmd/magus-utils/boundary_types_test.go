package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	queuetypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// A closed string type's Values set is written by hand, and buzzgen
// derives the mirror's cases from its constants. A constant the set
// leaves out fails here.
func TestBoundaryEnumsMatchTheirSets(t *testing.T) {
	for _, e := range boundaryEnumList() {
		if e.Type == nil {
			continue
		}
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

type ReachInternal struct{ Change queuetypes.Change }

// BoundaryWiringSuite pins what magus-utils hands the reach.
type BoundaryWiringSuite struct {
	suite.Suite
}

func TestBoundaryWiring(t *testing.T) { suite.Run(t, new(BoundaryWiringSuite)) }

func (s *BoundaryWiringSuite) config(text string) buzzgen.Config {
	cfg, err := buzzgen.ParseConfig(strings.NewReader("version: v1alpha1\n" + text))
	s.Require().NoError(err)
	return cfg
}

func (s *BoundaryWiringSuite) TestAnUnlistedInternalPackageIsRefused() {
	r := buzzgen.NewReach(s.config(""))
	r.Walk(reflect.TypeFor[ReachInternal](), buzzgen.Outbound, "root")
	s.ErrorContains(r.Refused(), "root reaches types.Change from internal package github.com/egladman/magus/internal/queue/types")
}

func (s *BoundaryWiringSuite) TestAListedInternalPackageCrosses() {
	r := buzzgen.NewReach(s.config("packages:\n  github.com/egladman/magus/internal/queue/types:\n    config: {open: test}\n"))
	r.Walk(reflect.TypeFor[ReachInternal](), buzzgen.Outbound, "root")
	s.NoError(r.Refused())
}

func (s *BoundaryWiringSuite) TestAHostSignatureNamesItsOwner() {
	mod := std.Module{Name: "m", Namespaces: []std.Namespace{{Name: "ns", Methods: []std.Method{
		{Name: "f", Extern: (func(context.Context, string) (string, error))(nil)},
	}}}}
	sigs := hostSignatures([]std.Module{mod})
	s.Require().Len(sigs, 1)
	s.Equal("m.ns.f", sigs[0].Owner)
}
