package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckMCPTokens(t *testing.T) {
	t.Run("absent cli token and no connectors", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		got := (&runner{}).checkMCPTokens()
		assert.Equal(t, StatusOK, got.Status)
		assert.Contains(t, got.Message, "cli token: absent")
		assert.Contains(t, got.Message, "0 connector token(s)")
		assert.Empty(t, got.Details)
	})

	t.Run("present cli token shows fingerprint", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		tok, err := auth.Generate()
		require.NoError(t, err)
		_, err = auth.SaveNew(tok)
		require.NoError(t, err)

		got := (&runner{}).checkMCPTokens()
		assert.Contains(t, got.Message, "cli token: present (fingerprint "+auth.Fingerprint(tok))
	})

	t.Run("expired and soon connectors are flagged; never is quiet", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		store, err := auth.LoadConnectorStore()
		require.NoError(t, err)
		_, _, err = store.Create("expired", time.Now().Add(-time.Hour))
		require.NoError(t, err)
		_, _, err = store.Create("soon", time.Now().Add(48*time.Hour))
		require.NoError(t, err)
		_, _, err = store.Create("forever", time.Time{})
		require.NoError(t, err)

		got := (&runner{}).checkMCPTokens()
		assert.Equal(t, StatusOK, got.Status, "credential state is informational, never a failure")
		assert.Contains(t, got.Message, "3 connector token(s)")

		joined := strings.Join(got.Details, "\n")
		assert.Contains(t, joined, `connector "expired" expired`)
		assert.Contains(t, joined, "revoke it: magus config mcp connector revoke expired")
		assert.Contains(t, joined, `connector "soon" expires in`)
		assert.NotContains(t, joined, "forever", "a never-expiring token must not be flagged")
	})
}
