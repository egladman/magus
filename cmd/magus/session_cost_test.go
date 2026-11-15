package main

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func TestHarnessModelRowsOpenTheWorkspaceOnceAndOnlyWhenAsked(t *testing.T) {
	opened := 0
	rows := harnessModelRows(t.Context(), t.TempDir(), func(context.Context) (types.WorkspaceRepository, error) {
		opened++
		return nil, errors.New("magusfile.buzz does not load")
	})

	assert.Zero(t, opened, "building the reader reads nothing")
	assert.Empty(t, rows(), "a workspace that will not load has no rows")
	assert.Empty(t, rows())
	assert.Equal(t, 1, opened)
}
