package apigroup

import (
	"testing"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/stretchr/testify/suite"
)

type apiGroupSuite struct{ suite.Suite }

func TestAPIGroup(t *testing.T) { suite.Run(t, new(apiGroupSuite)) }

func (s *apiGroupSuite) TestEveryVersionParsesUnderTheModuleGroup() {
	for _, v := range []apiversion.APIVersion{Job, JobResult, Step, Verdict, Session} {
		s.True(v.Valid(), "%s parses", v)
		s.Regexp(`^safetyscissors\.[a-z-]+$`, v.Group())
	}
}

func (s *apiGroupSuite) TestGroupsAreDistinct() {
	groups := map[string]bool{}
	for _, v := range []apiversion.APIVersion{Job, JobResult, Step, Verdict, Session} {
		s.False(groups[v.Group()], "%s shares its group", v)
		groups[v.Group()] = true
	}
}
