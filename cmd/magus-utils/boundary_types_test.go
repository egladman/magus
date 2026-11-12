package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	queuetypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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

type ReachLeaf struct{ Name string }

type ReachArg struct {
	Leaf ReachLeaf
	Many []ReachLeaf
}

type ReachResult struct{ Count int }

type ReachLive struct{ Name string }

type ReachRecord struct{ Name string }

type ReachGeneric[V any] struct{ Value V }

type ReachHolder struct {
	Box ReachGeneric[ReachLeaf]
}

type reachHidden struct{ Name string }

type ReachLeaky struct{ Hidden reachHidden }

type ReachInternal struct{ Change queuetypes.Change }

// ReachSuite pins how membership and direction follow from a reach.
type ReachSuite struct {
	suite.Suite
}

func TestReachSuite(t *testing.T) { suite.Run(t, new(ReachSuite)) }

const reachPackage = "github.com/egladman/magus/cmd/magus-utils"

func (s *ReachSuite) reach(config string) *reach {
	cfg, err := buzzgen.ParseConfig(strings.NewReader("version: v1alpha1\n" + config))
	s.Require().NoError(err)
	return newReach(cfg)
}

func (s *ReachSuite) member(r *reach, t reflect.Type) (boundaryType, bool) {
	for _, entry := range r.members() {
		if entry.Type == t {
			return entry, true
		}
	}
	return boundaryType{}, false
}

func (s *ReachSuite) TestAParameterCrossesInAndAResultOut() {
	r := s.reach("")
	r.walk(reflect.TypeOf(func(context.Context, ReachArg) (ReachResult, error) { return ReachResult{}, nil }), crossOut, "m.f")
	s.Require().Empty(r.errs)

	arg, ok := s.member(r, reflect.TypeFor[ReachArg]())
	s.Require().True(ok)
	s.True(arg.Inbound)
	s.False(arg.Outbound)
	result, ok := s.member(r, reflect.TypeFor[ReachResult]())
	s.Require().True(ok)
	s.True(result.Outbound)
	s.False(result.Inbound)
}

func (s *ReachSuite) TestAFieldCrossesWithItsHolder() {
	r := s.reach("")
	r.walk(reflect.TypeFor[ReachArg](), crossIn, "root")

	leaf, ok := s.member(r, reflect.TypeFor[ReachLeaf]())
	s.Require().True(ok, "a field's type needs no line of its own")
	s.True(leaf.Inbound)
	s.Equal("root", r.from[reflect.TypeFor[ReachLeaf]()])
}

func (s *ReachSuite) TestACallbackFlipsTheDirection() {
	r := s.reach("")
	type callback = func(context.Context, ReachArg) (ReachResult, error)
	r.walk(reflect.TypeOf(func(context.Context, callback) error { return nil }), crossOut, "m.watch")

	arg, _ := s.member(r, reflect.TypeFor[ReachArg]())
	s.True(arg.Outbound, "Go hands the callback its argument")
	result, _ := s.member(r, reflect.TypeFor[ReachResult]())
	s.True(result.Inbound, "Go reads what the callback returns")
}

func (s *ReachSuite) TestAGenericInstanceCrossesOnlyItsArgument() {
	r := s.reach("")
	r.walk(reflect.TypeFor[ReachHolder](), crossOut, "root")

	_, ok := s.member(r, reflect.TypeFor[ReachLeaf]())
	s.True(ok)
	_, ok = s.member(r, reflect.TypeFor[ReachGeneric[ReachLeaf]]())
	s.False(ok, "the instance mirrors as its generic object")
}

func (s *ReachSuite) TestAnUnexportedTypeIsRefused() {
	r := s.reach("")
	r.walk(reflect.TypeFor[ReachLeaky](), crossOut, "root")

	s.Require().Len(r.errs, 1)
	s.ErrorContains(r.errs[0], "root reaches unexported type main.reachHidden")
}

func (s *ReachSuite) TestAnInternalTypeNeedsItsPackageListed() {
	r := s.reach("")
	r.walk(reflect.TypeFor[ReachInternal](), crossOut, "root")

	s.Require().NotEmpty(r.errs)
	s.ErrorContains(r.errs[0], "root reaches types.Change from internal package github.com/egladman/magus/internal/queue/types")

	listed := s.reach("packages:\n  github.com/egladman/magus/internal/queue/types:\n    config: {open: test}\n")
	listed.walk(reflect.TypeFor[ReachInternal](), crossOut, "root")
	s.Empty(listed.errs)
}

func (s *ReachSuite) TestTwoTypesTakingOneNameAreRefused() {
	r := s.reach("packages:\n  " + reachPackage + ":\n    types:\n      ReachResult: {config: {name: ReachLeaf}}\n")
	r.walk(reflect.TypeFor[ReachLeaf](), crossOut, "first")
	r.walk(reflect.TypeFor[ReachResult](), crossOut, "second")

	err := r.checkNames(boundarySet{types: r.members()})
	s.Require().Error(err)
	s.Contains(err.Error(), "Buzz name ReachLeaf is taken by")
	s.Contains(err.Error(), "(reached from first)")
	s.Contains(err.Error(), "(reached from second)")
}

func (s *ReachSuite) TestAModuleSettingKeepsTwoNamesApart() {
	r := s.reach("packages:\n  " + reachPackage + ":\n    types:\n      ReachResult: {config: {name: ReachLeaf, module: fs}}\n")
	r.walk(reflect.TypeFor[ReachLeaf](), crossOut, "first")
	r.walk(reflect.TypeFor[ReachResult](), crossOut, "second")

	s.NoError(r.checkNames(boundarySet{types: r.members()}))
}

func (s *ReachSuite) TestALineForATypeNothingReachesIsRefused() {
	r := s.reach("packages:\n  " + reachPackage + ":\n    types:\n      ReachResult: {config: {name: Result2}}\n")
	r.walk(reflect.TypeFor[ReachLeaf](), crossOut, "root")

	err := r.checkListed(boundarySet{types: r.members()})
	s.ErrorContains(err, "types.ReachResult names "+reachPackage+".ReachResult, which nothing crosses as: delete the line")
}

func (s *ReachSuite) TestAMirrorSettingCrossesAsItsRecord() {
	r := s.reach("packages:\n  " + reachPackage + ":\n    types:\n      ReachLive: {config: {mirror: ReachRecord}}\n")
	r.walk(reflect.TypeFor[ReachLive](), crossOut, "m.status")
	r.walk(reflect.TypeFor[ReachRecord](), crossOut, "root")

	_, ok := s.member(r, reflect.TypeFor[ReachLive]())
	s.False(ok, "the live type crosses as its record's mirror")
	s.NoError(r.checkListed(boundarySet{types: r.members()}))
}
