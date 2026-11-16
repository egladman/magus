package describe

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type renderSuite struct{ suite.Suite }

func TestRenderSuite(t *testing.T) { suite.Run(t, new(renderSuite)) }

func (s *renderSuite) TestParseSchemaNamesTheTopLevelProperties() {
	doc, names, err := ParseSchema(`{"properties":{"b":{},"a":{}},"title":"t"}`)
	s.Require().NoError(err)
	s.Equal([]string{"a", "b"}, names)
	s.Equal("t", doc["title"])
}

func (s *renderSuite) TestParseSchemaRefusesNonJSON() {
	_, _, err := ParseSchema("not json")
	s.Error(err)
}
