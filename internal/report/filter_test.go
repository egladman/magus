package report

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFilter_Nil_AdmitsAll(t *testing.T) {
	f, err := ParseFilter(nil)
	require.NoError(t, err)
	// nil signals admit-all.
	assert.Nil(t, f, "ParseFilter(nil) should return nil to signal admit-all")
}

func TestParseFilter_BlankTerms_Nil(t *testing.T) {
	f, err := ParseFilter([]string{"", "  "})
	require.NoError(t, err)
	assert.Nil(t, f, "ParseFilter(blank terms) should return nil")
}

func TestFilter_Admit_DefaultAllow(t *testing.T) {
	f, err := ParseFilter([]string{"-run"})
	require.NoError(t, err)
	assert.True(t, f.Admit("log"), "default-allow: 'log' should be admitted")
	assert.False(t, f.Admit("run"), "excluded type 'run' should not be admitted")
}

func TestFilter_Admit_DefaultDeny(t *testing.T) {
	f, err := ParseFilter([]string{"+log"})
	require.NoError(t, err)
	assert.True(t, f.Admit("log"), "included type 'log' should be admitted")
	assert.False(t, f.Admit("run"), "default-deny: 'run' should not be admitted")
}
