package console

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPresent(t *testing.T) {
	t.Parallel()

	got, err := Present("127.0.0.1:7391", "", "  inspect the jobs  ")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:7391/console/dashboard/", got.URL)
	assert.Equal(t, "dashboard", got.Surface)
	assert.Equal(t, "inspect the jobs", got.Reason)
	assert.Contains(t, got.OpenCommand, `"http://127.0.0.1:7391/console/dashboard/#token=$(magus config token print)"`)
	assert.NotContains(t, got.URL, "token=")
}

func TestPresentRejectsUnknownSurface(t *testing.T) {
	t.Parallel()

	_, err := Present("127.0.0.1:7391", "settings", "")
	require.EqualError(t, err, `console: unknown surface "settings"`)
}
