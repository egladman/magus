package edit

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestEditReceiptIDShape(t *testing.T) {
	t.Parallel()

	id := newReceiptID(time.Date(2026, 9, 26, 10, 15, 0, 0, time.UTC))
	assert.Regexp(t, `^20260926-101500-[0-9a-f]{8}$`, id)
	assert.True(t, receiptIDRe.MatchString(id))
}

func TestEditReceiptSaveLoadRemove(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "edit")
	r := types.EditReceipt{
		SchemaVersion: types.EditSetSchemaVersion,
		ID:            "20260926-101500-0a1b2c3d",
		Applied:       true,
		AppliedAt:     time.Date(2026, 9, 26, 10, 15, 0, 0, time.UTC),
		Files:         []types.EditedFile{{Path: "a.go", DigestBefore: "sha256:aa", DigestAfter: "sha256:bb"}},
		Undo: &types.EditSet{SchemaVersion: 1, Edits: []types.EditSite{
			{Path: "a.go", Anchor: types.EditAnchorLines, Lines: []int{1, 1}, Old: "b\n", New: "a\n", Digest: "sha256:bb"},
		}},
	}
	require.NoError(t, SaveReceipt(dir, r))

	info, err := os.Stat(filepath.Join(dir, r.ID+".json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	got, err := LoadReceipt(dir, r.ID)
	require.NoError(t, err)
	assert.Equal(t, r, got)

	require.NoError(t, RemoveReceipt(dir, r.ID))
	require.NoError(t, RemoveReceipt(dir, r.ID), "removing a missing receipt is not an error")
	_, err = LoadReceipt(dir, r.ID)
	assert.EqualError(t, err, "edit: no receipt 20260926-101500-0a1b2c3d in "+dir)
}

func TestEditReceiptRefusals(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := LoadReceipt(dir, "../../etc/passwd")
	assert.EqualError(t, err, `edit: "../../etc/passwd" is not a receipt id (the form is 20260926-101500-1a2b3c4d)`)

	assert.EqualError(t, SaveReceipt(dir, types.EditReceipt{ID: "x/y"}), `edit: receipt id "x/y" is not one magus minted`)

	noUndo := types.EditReceipt{ID: "20260926-101500-0a1b2c3d", Applied: true}
	require.NoError(t, SaveReceipt(dir, noUndo))
	_, err = LoadReceipt(dir, noUndo.ID)
	assert.EqualError(t, err, "edit: receipt 20260926-101500-0a1b2c3d carries no undo set")
}
