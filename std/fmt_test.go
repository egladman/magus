package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFmtSprintf(t *testing.T) {
	sprintf := func(format string, args ...string) string {
		got, err := FmtSprintf(context.Background(), format, args...)
		require.NoError(t, err)
		return got
	}

	t.Run("no args", func(t *testing.T) {
		assert.Equal(t, "hello", sprintf("hello"))
	})
	t.Run("one verb", func(t *testing.T) {
		assert.Equal(t, "v1.2.3", sprintf("v%s", "1.2.3"))
	})
	t.Run("many verbs", func(t *testing.T) {
		assert.Equal(t, "magus_1.0_linux_amd64.tar.gz", sprintf("magus_%s_%s_%s.tar.gz", "1.0", "linux", "amd64"))
	})
	t.Run("quote verb", func(t *testing.T) {
		assert.Equal(t, `"x y"`, sprintf("%q", "x y"))
	})
	t.Run("literal percent", func(t *testing.T) {
		assert.Equal(t, "100%", sprintf("100%%"))
	})
}
