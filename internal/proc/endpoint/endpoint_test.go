package endpoint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	// canonical unix:// form
	t.Run("canonical unix scheme", func(t *testing.T) {
		ep, err := Parse("unix:///var/run/magus.sock")
		require.NoError(t, err)
		assert.Equal(t, "/var/run/magus.sock", ep.Addr)
		assert.Equal(t, "unix:///var/run/magus.sock", ep.String())
		assert.Equal(t, "unix", ep.Network())
	})
	t.Run("canonical unix scheme with temp path", func(t *testing.T) {
		ep, err := Parse("unix:///tmp/magus-1234-abcd.sock")
		require.NoError(t, err)
		assert.Equal(t, "/tmp/magus-1234-abcd.sock", ep.Addr)
		assert.Equal(t, "unix:///tmp/magus-1234-abcd.sock", ep.String())
		assert.Equal(t, "unix", ep.Network())
	})
	// bare path back-compat
	t.Run("bare path back-compat", func(t *testing.T) {
		ep, err := Parse("/tmp/magus.sock")
		require.NoError(t, err)
		assert.Equal(t, "/tmp/magus.sock", ep.Addr)
		assert.Equal(t, "unix:///tmp/magus.sock", ep.String())
		assert.Equal(t, "unix", ep.Network())
	})
	// errors
	t.Run("empty unix scheme", func(t *testing.T) {
		_, err := Parse("unix://")
		assert.Error(t, err)
	})
	t.Run("tcp scheme rejected", func(t *testing.T) {
		_, err := Parse("tcp://localhost:9000")
		assert.Error(t, err)
	})
	t.Run("grpc scheme rejected", func(t *testing.T) {
		_, err := Parse("grpc://foo")
		assert.Error(t, err)
	})
	t.Run("empty input rejected", func(t *testing.T) {
		_, err := Parse("")
		assert.Error(t, err)
	})
}
