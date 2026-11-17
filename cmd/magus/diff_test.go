package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/clikit/exit"
)

// TestDiffThreadRefusesWhatAnswersForTheWholeChangeset. --thread narrows the review to one
// thread, so a flag that answers for every file is a misunderstanding to name, not one to resolve
// by picking a winner.
func TestDiffThreadRefusesWhatAnswersForTheWholeChangeset(t *testing.T) {
	for _, other := range []string{"--unread", "--ack", "--impact"} {
		t.Run(other, func(t *testing.T) {
			err := diffCommand(t.Context(), t.TempDir(), []string{"--thread", "c1", other})

			require.Error(t, err)
			assert.Equal(t, exit.Usage, exit.Code(err))
			assert.Contains(t, err.Error(), "--thread narrows the review to one thread")
		})
	}
}
