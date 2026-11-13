package buzzgen

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

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

// ReachSuite pins how membership and direction follow from a reach.
type ReachSuite struct {
	suite.Suite
}

func TestReach(t *testing.T) { suite.Run(t, new(ReachSuite)) }

const reachPackage = "github.com/egladman/magus/libs/gopherbuzz/buzzgen"

func (s *ReachSuite) reach(config string) *Reach {
	cfg, err := ParseConfig(strings.NewReader("version: v1alpha1\n" + config))
	s.Require().NoError(err)
	return NewReach(cfg)
}

func (s *ReachSuite) member(r *Reach, t reflect.Type) (Member, bool) {
	for _, m := range r.Members() {
		if m.Type == t {
			return m, true
		}
	}
	return Member{}, false
}

func (s *ReachSuite) claims(r *Reach) []Claim {
	var out []Claim
	for _, m := range r.Members() {
		out = append(out, Claim{Key: m.Key(), Type: m.Type})
	}
	return out
}

func (s *ReachSuite) TestAParameterCrossesInAndAResultOut() {
	r := s.reach("")
	r.Walk(reflect.TypeOf(func(context.Context, ReachArg) (ReachResult, error) { return ReachResult{}, nil }), Outbound, "m.f")
	s.Require().NoError(r.Err())

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
	r.Walk(reflect.TypeFor[ReachArg](), Inbound, "root")

	leaf, ok := s.member(r, reflect.TypeFor[ReachLeaf]())
	s.Require().True(ok, "a field's type needs no line of its own")
	s.True(leaf.Inbound)
	s.Equal("root", r.From(reflect.TypeFor[ReachLeaf]()))
}

func (s *ReachSuite) TestACallbackFlipsTheDirection() {
	r := s.reach("")
	type callback = func(context.Context, ReachArg) (ReachResult, error)
	r.Walk(reflect.TypeOf(func(context.Context, callback) error { return nil }), Outbound, "m.watch")

	arg, _ := s.member(r, reflect.TypeFor[ReachArg]())
	s.True(arg.Outbound, "the host hands the callback its argument")
	result, _ := s.member(r, reflect.TypeFor[ReachResult]())
	s.True(result.Inbound, "the host reads what the callback returns")
}

func (s *ReachSuite) TestAGenericInstanceCrossesOnlyItsArgument() {
	r := s.reach("")
	r.Walk(reflect.TypeFor[ReachHolder](), Outbound, "root")

	_, ok := s.member(r, reflect.TypeFor[ReachLeaf]())
	s.True(ok)
	_, ok = s.member(r, reflect.TypeFor[ReachGeneric[ReachLeaf]]())
	s.False(ok, "the instance mirrors as its generic object")
}

func (s *ReachSuite) TestAnUnexportedTypeIsRefused() {
	r := s.reach("")
	r.Walk(reflect.TypeFor[ReachLeaky](), Outbound, "root")

	s.ErrorContains(r.Err(), "root reaches unexported type buzzgen.reachHidden")
}

func (s *ReachSuite) TestTwoTypesTakingOneNameAreRefused() {
	r := s.reach("packages:\n  " + reachPackage + ":\n    types:\n      ReachResult: {config: {name: ReachLeaf}}\n")
	r.Walk(reflect.TypeFor[ReachLeaf](), Outbound, "first")
	r.Walk(reflect.TypeFor[ReachResult](), Outbound, "second")

	err := r.CheckNames(s.claims(r))
	s.Require().Error(err)
	s.Contains(err.Error(), "two crossing types take Buzz name ReachLeaf")
	s.Contains(err.Error(), "(reached from first)")
	s.Contains(err.Error(), "(reached from second)")
}

func (s *ReachSuite) TestAModuleSettingKeepsTwoNamesApart() {
	r := s.reach("packages:\n  " + reachPackage + ":\n    types:\n      ReachResult: {config: {name: ReachLeaf, module: fs}}\n")
	r.Walk(reflect.TypeFor[ReachLeaf](), Outbound, "first")
	r.Walk(reflect.TypeFor[ReachResult](), Outbound, "second")

	s.NoError(r.CheckNames(s.claims(r)))
}

const derivedReach = "module: shop\npackages:\n  github.com/egladman/magus/libs/gopherbuzz: {}\n"

func (s *ReachSuite) TestADerivedNamespaceQualifiesTheKey() {
	r := s.reach(derivedReach)
	r.Walk(reflect.TypeFor[ReachLeaf](), Outbound, "root")

	leaf, ok := s.member(r, reflect.TypeFor[ReachLeaf]())
	s.Require().True(ok)
	s.Equal(`shop\buzzgen\ReachLeaf`, leaf.Key())
	s.NoError(r.CheckNamespaces(func(string) []string { return []string{"other"} }))
}

func (s *ReachSuite) TestANamespaceThatIsAlsoAParentMemberIsRefused() {
	r := s.reach(derivedReach)
	r.Walk(reflect.TypeFor[ReachLeaf](), Outbound, "root")

	var asked string
	err := r.CheckNamespaces(func(module string) []string {
		asked = module
		return []string{"buzzgen"}
	})
	s.Equal("shop", asked)
	s.ErrorContains(err, `namespace shop\buzzgen (reached from root) is also the member buzzgen of module shop`)
}

func (s *ReachSuite) TestALineForATypeNothingReachesIsRefused() {
	r := s.reach("packages:\n  " + reachPackage + ":\n    types:\n      ReachResult: {config: {name: Result2}}\n")
	r.Walk(reflect.TypeFor[ReachLeaf](), Outbound, "root")

	s.ErrorContains(r.CheckListed(), "types.ReachResult names "+reachPackage+".ReachResult, which nothing crosses as: delete the line")
	s.NoError(r.CheckListed(reachPackage+".ReachResult"), "a key the caller says crosses keeps its line")
}

func (s *ReachSuite) TestAMirrorSettingCrossesAsItsRecord() {
	r := s.reach("packages:\n  " + reachPackage + ":\n    types:\n      ReachLive: {config: {mirror: ReachRecord}}\n")
	r.Walk(reflect.TypeFor[ReachLive](), Outbound, "m.status")
	r.Walk(reflect.TypeFor[ReachRecord](), Outbound, "root")

	_, ok := s.member(r, reflect.TypeFor[ReachLive]())
	s.False(ok, "the live type crosses as its record's mirror")
	s.NoError(r.CheckListed())
}

func (s *ReachSuite) TestPackagesListsWhereEachReachedTypeLives() {
	r := s.reach("")
	r.Walk(reflect.TypeFor[ReachArg](), Inbound, "root")

	s.Equal([]string{reachPackage}, r.Packages())
}
