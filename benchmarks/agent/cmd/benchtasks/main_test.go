package main

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type MainSuite struct{ suite.Suite }

func TestMainSuite(t *testing.T) { suite.Run(t, new(MainSuite)) }

func (s *MainSuite) TestRunRefusesAnUnknownSuite() {
	s.ErrorContains(run(s.T().Context(), []string{"verified"}), `unknown suite "verified"`)
}

func (s *MainSuite) TestRunNamesEveryMissingHistoryFlag() {
	s.ErrorContains(run(s.T().Context(), []string{"history", "--repo", "."}), "--after, --ref, -o required")
}
