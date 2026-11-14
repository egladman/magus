package commands

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAPIUpToDate is the drift gate for the committed public-API snapshot: the
// checked-in testdata/api.lock must equal what API emits today, so the public CLI
// API cannot silently change without the diff appearing in a PR. On failure,
// regenerate:
//
//	./magus run man-generate . -s
func TestAPIUpToDate(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("testdata", "api.lock"))
	require.NoError(t, err, "read testdata/api.lock")
	lines, err := API(config.KnownKeys())
	require.NoError(t, err)
	got := strings.Join(lines, "\n") + "\n"
	assert.Equal(t, string(committed), got,
		"internal/commands/testdata/api.lock is out of date; run: ./magus run man-generate . -s")
}

// The lock is the contract a binary and its tree compare, so it names
// what a load reads beyond the command tree.
func TestAPINamesTheContractBeyondTheCommandTree(t *testing.T) {
	got, err := API(nil)
	require.NoError(t, err)
	assert.Contains(t, got, "store magus.job/v1alpha1")
	assert.Contains(t, got, "guard-rule stale-binary")
	assert.Contains(t, got, "spell golang")
}

type unreadableFS struct{}

func (unreadableFS) Open(string) (fs.File, error) { return nil, errors.New("disk gone") }

func TestShippedSpellsReportsATreeItCannotList(t *testing.T) {
	_, err := shippedSpells(unreadableFS{})
	require.Error(t, err)
	assert.ErrorContains(t, err, "list the shipped spells")
	assert.ErrorContains(t, err, "disk gone")
}

// The embedded lock is the committed one this binary was built with.
func TestLockIsTheCommittedFile(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("testdata", "api.lock"))
	require.NoError(t, err)
	assert.Equal(t, committed, Lock())
}
