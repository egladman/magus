package jsonl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type JSONSuite struct{ suite.Suite }

func TestJSON(t *testing.T) { suite.Run(t, new(JSONSuite)) }

func (s *JSONSuite) TestMarshalLeavesHTMLUnescapedAndEndsWithoutANewline() {
	b, err := Marshal(map[string]string{"command": "a && b <c>"})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), `{"command":"a && b <c>"}`, string(b))
}

func (s *JSONSuite) TestUnmarshalRoundTrips() {
	var got map[string]int
	require.NoError(s.T(), Unmarshal([]byte(`{"a":1}`), &got))
	assert.Equal(s.T(), map[string]int{"a": 1}, got)
}
