package httpx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOrigin(t *testing.T) {
	got, err := ParseOrigin("https://eli.gladman.cc/magus/logs/")
	require.NoError(t, err)
	assert.Equal(t, "https://eli.gladman.cc", got)

	_, err = ParseOrigin("not-a-url")
	assert.Error(t, err)
}
