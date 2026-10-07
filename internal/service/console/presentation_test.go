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
	assert.Contains(t, got.OpenCommand, `"http://127.0.0.1:7391/console/dashboard/#code=$(magus config console token create --code --expires 12h)"`)
	assert.Equal(t, Presentation{
		URL:         "http://127.0.0.1:7391/console/dashboard/",
		Surface:     "dashboard",
		Reason:      "inspect the jobs",
		OpenCommand: got.OpenCommand, // asserted above
	}, got)
	assert.NotContains(t, got.URL, "token=")
}

func TestPresentRejectsUnknownSurface(t *testing.T) {
	t.Parallel()

	_, err := Present("127.0.0.1:7391", "settings", "")
	require.EqualError(t, err, `console: unknown surface "settings"`)
}
