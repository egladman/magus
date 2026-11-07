package config

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type UseConfigSuite struct{ suite.Suite }

func TestUseConfigSuite(t *testing.T) { suite.Run(t, new(UseConfigSuite)) }

func (s *UseConfigSuite) failures(entries ...UseEntry) []FieldFailure {
	return useFailures(entries)
}

func (s *UseConfigSuite) TestAValidEntryPasses() {
	s.Empty(s.failures(UseEntry{Name: "magus", Path: "../magus", Share: []types.ShareKind{types.ShareLint}}))
}

func (s *UseConfigSuite) TestNoShareListTakesEveryImplementedKind() {
	s.Equal([]types.ShareKind{types.ShareLint}, UseEntry{Name: "magus", Path: "../magus"}.Kinds())
}

func (s *UseConfigSuite) TestAnUnimplementedKindIsRefused() {
	got := s.failures(UseEntry{Name: "magus", Path: "../magus", Share: []types.ShareKind{"proofread"}})
	s.Equal([]FieldFailure{{Field: "use[0].share[0]", Tag: "use_share_kind", Param: "lint", Value: "proofread"}}, got)
}

func (s *UseConfigSuite) TestOneKindSharedByTwoEntriesIsRefused() {
	got := s.failures(UseEntry{Name: "a", Path: "../a"}, UseEntry{Name: "b", Path: "../b"})
	s.Equal([]FieldFailure{{Field: "use[1].share[0]", Tag: "use_share_owner", Param: "a", Value: "lint"}}, got)
}

func (s *UseConfigSuite) TestAMalformedNameIsRefused() {
	got := s.failures(UseEntry{Name: "Magus/x", Path: "../magus"})
	s.Equal([]FieldFailure{{Field: "use[0].name", Tag: "use_name", Value: "Magus/x"}}, got)
}

func (s *UseConfigSuite) TestADuplicateNameIsRefused() {
	got := s.failures(UseEntry{Name: "a", Path: "../a"},
		UseEntry{Name: "a", Path: "../b", Share: []types.ShareKind{}})
	s.Contains(got, FieldFailure{Field: "use[1].name", Tag: "unique", Param: "name", Value: "a"})
}

func (s *UseConfigSuite) TestAnEmptyPathIsRefused() {
	got := s.failures(UseEntry{Name: "magus"})
	s.Equal([]FieldFailure{{Field: "use[0].path", Tag: "required"}}, got)
}

func (s *UseConfigSuite) TestValidateReportsUseFailures() {
	err := Validate(Config{Use: []UseEntry{{Name: "magus", Path: "../magus", Share: []types.ShareKind{"guard"}}}})
	s.Require().Error(err)
	s.Contains(err.Error(), "use[0].share[0]: is not a kind magus shares")
}
