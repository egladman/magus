package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ResolveFailuresSuite struct {
	suite.Suite
	ran []string
}

func TestResolveFailuresSuite(t *testing.T) { suite.Run(t, new(ResolveFailuresSuite)) }

func (s *ResolveFailuresSuite) SetupTest() { s.ran = nil }

func (s *ResolveFailuresSuite) runner(failing ...string) func([]string) error {
	return func(args []string) error {
		s.ran = append(s.ran, args[0])
		for _, name := range failing {
			if args[0] == name {
				return errors.New(name + " is stale")
			}
		}
		return nil
	}
}

func (s *ResolveFailuresSuite) TestEveryTargetRunsAfterAFailure() {
	rebuild := map[string][]string{"a": {"p"}, "b": {"p"}, "c": {"p"}}

	err := runRebuildTargets(s.runner("a"), rebuild)

	s.Require().Error(err)
	s.Equal([]string{"a", "b", "c"}, s.ran)
}

func (s *ResolveFailuresSuite) TestErrorListsEachFailedCommand() {
	rebuild := map[string][]string{"a": {"p", "q"}, "b": {"p"}, "c": {"p"}}

	err := runRebuildTargets(s.runner("a", "c"), rebuild)

	s.Require().Error(err)
	s.Contains(err.Error(), "2 of 3 target(s) failed")
	s.Contains(err.Error(), "run a p q")
	s.Contains(err.Error(), "a is stale")
	s.Contains(err.Error(), "run c p")
	s.Contains(err.Error(), "c is stale")
	s.NotContains(err.Error(), "b is stale")
}

func (s *ResolveFailuresSuite) TestNoFailureReturnsNil() {
	s.NoError(runRebuildTargets(s.runner(), map[string][]string{"a": {"p"}}))
}

func (s *ResolveFailuresSuite) TestStageFailureNamesStagesThatDidNotRun() {
	err := stageFailed(errors.New("boom"), "settling by job claims", "planning the rest")

	s.EqualError(err, "boom\nnot run because of it: settling by job claims, planning the rest")
}
