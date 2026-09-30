//go:build windows

package vcs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOwnerChecksAreOutOfScopeOnWindows(t *testing.T) {
	dir := t.TempDir()
	assert.True(t, pathOwnedByCurrentUser(dir))
	_, ok := pathDevice(dir)
	assert.False(t, ok)
}
