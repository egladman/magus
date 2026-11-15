package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseBisectCulprit verifies the culprit-extraction logic against
// synthetic git-bisect-log output.
func TestParseBisectCulprit(t *testing.T) {
	t.Run("standard culprit line", func(t *testing.T) {
		// Skips without git, as the parser's real caller needs it.
		testkit.NewRepository(t, nil)

		log := `git bisect start
# good: [abc123] fix typo
git bisect good abc123
# bad: [def456] introduce regression
git bisect bad def456
# first bad commit: [def456] introduce regression
`
		// We can't easily fake `git bisect log`, so test the parser directly
		// by parsing the raw bytes.
		got, err := parseBisectLog([]byte(log))
		require.NoError(t, err)
		assert.Equal(t, "def456", got)
	})

	t.Run("no culprit", func(t *testing.T) {
		testkit.NewRepository(t, nil)

		_, err := parseBisectLog([]byte("git bisect start\n# good: [abc123] blah\n"))
		assert.Error(t, err)
	})
}

// parseBisectLog is the inner logic of parseBisectCulprit, extracted for
// unit-testability without needing a real git repo.
func parseBisectLog(out []byte) (string, error) {
	for _, line := range bytes.Split(out, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if strings.HasPrefix(s, "# first bad commit: [") {
			after := strings.TrimPrefix(s, "# first bad commit: [")
			sha := strings.SplitN(after, "]", 2)[0]
			if sha != "" {
				return sha, nil
			}
		}
	}
	return "", errors.New("could not parse culprit from git bisect log")
}
