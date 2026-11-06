package main

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/types"
)

type GateUnrunSuite struct {
	suite.Suite
	g *gateRedundancy
}

func TestGateUnrunSuite(t *testing.T) {
	suite.Run(t, new(GateUnrunSuite))
}

func (s *GateUnrunSuite) SetupTest() {
	s.g = &gateRedundancy{target: types.TargetCI}
}

func absent(project, target string) journal.Event {
	return journal.Event{Project: project, Target: target, DiagnosticCode: string(types.ToolNotReady),
		Text: "MGS3004 docker is installed but not ready\nCannot connect to the Docker daemon"}
}

func (s *GateUnrunSuite) TestEveryFailureAnAbsentCapabilityNamesEachTarget() {
	s.Equal([]string{
		"images/magus:image-build: MGS3004 docker is installed but not ready",
		"images/web:image-build: MGS3004 docker is installed but not ready",
	}, s.g.unrun([]journal.Event{absent("images/magus", "image-build"), absent("images/web", "image-build")}))
}

func (s *GateUnrunSuite) TestOneFailureOfTheCodeMakesTheGateFail() {
	s.Nil(s.g.unrun([]journal.Event{absent("images/magus", "image-build"), {Project: ".", Target: "go-test", Text: "FAIL"}}))
}

func (s *GateUnrunSuite) TestNoFailedTargetIsNoUnrunGate() {
	s.Nil(s.g.unrun(nil), "a run that failed before any target ran failed")
}
