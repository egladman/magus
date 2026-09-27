package edit

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// ReceiptDir is where receipts live under a workspace's cache dir.
func ReceiptDir(cacheDir string) string { return filepath.Join(cacheDir, "edit") }

// receiptIDRe is the shape newReceiptID mints. An id reaches a file path, so anything
// else is refused before it is joined to one.
var receiptIDRe = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{8}$`)

// newReceiptID sorts by time and is unique within a second.
func newReceiptID(at time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return at.Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// SaveReceipt writes r to dir as <id>.json, replacing any receipt with that id.
func SaveReceipt(dir string, r types.EditReceipt) error {
	if !receiptIDRe.MatchString(r.ID) {
		return fmt.Errorf("edit: receipt id %q is not one magus minted", r.ID)
	}
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("edit: encode receipt %s: %w", r.ID, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("edit: receipt dir: %w", err)
	}
	dest := filepath.Join(dir, r.ID+".json")
	s, err := stage(dest, append(body, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("edit: write receipt %s: %w", r.ID, err)
	}
	if err := os.Rename(s, dest); err != nil {
		_ = os.Remove(s)
		return fmt.Errorf("edit: write receipt %s: %w", r.ID, err)
	}
	return nil
}

// LoadReceipt reads the receipt id names from dir.
func LoadReceipt(dir, id string) (types.EditReceipt, error) {
	if !receiptIDRe.MatchString(id) {
		return types.EditReceipt{}, fmt.Errorf("edit: %q is not a receipt id (the form is 20260926-101500-1a2b3c4d)", id)
	}
	body, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return types.EditReceipt{}, fmt.Errorf("edit: no receipt %s in %s", id, dir)
	}
	if err != nil {
		return types.EditReceipt{}, fmt.Errorf("edit: read receipt %s: %w", id, err)
	}
	var r types.EditReceipt
	if err := json.Unmarshal(body, &r); err != nil {
		return types.EditReceipt{}, fmt.Errorf("edit: decode receipt %s: %w", id, err)
	}
	if r.Undo == nil || len(r.Undo.Edits) == 0 {
		return types.EditReceipt{}, fmt.Errorf("edit: receipt %s carries no undo set", id)
	}
	return r, nil
}

// RemoveReceipt deletes a receipt whose apply did not land. A missing one is not an error.
func RemoveReceipt(dir, id string) error {
	if !receiptIDRe.MatchString(id) {
		return fmt.Errorf("edit: %q is not a receipt id", id)
	}
	if err := os.Remove(filepath.Join(dir, id+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("edit: remove receipt %s: %w", id, err)
	}
	return nil
}
