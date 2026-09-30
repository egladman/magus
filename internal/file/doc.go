package file

import (
	"cmp"
	"context"
	"errors"
	"os"

	"github.com/egladman/magus/internal/json"
)

// SkipWrite, returned by a [Doc.Update] fn, ends the update without writing and
// without an error: the fn looked and found nothing to change.
var SkipWrite = errors.New("skip this write")

// Doc is a document of type T kept whole in one file that several processes rewrite:
// a ledger, a history, a watermark. Reads take no lock, because every write replaces
// the file by rename and a reader sees one whole version or the next. Writes go
// through Update, which re-reads under a lock so no writer drops another's change.
//
// Version policy is not Doc's: a state file embeds types.Schema so a rewrite keeps the
// members it does not declare; a cache folds its version into its key and misses.
type Doc[T any] struct {
	// Path is the document.
	Path string
	// Perm is the document's mode; zero means 0644.
	Perm os.FileMode
	// Decode parses the file's bytes into v; nil means JSON. It is where a store
	// refuses a document it cannot act on.
	Decode func(b []byte, v *T) error
	// Encode serializes the document; nil means compact JSON.
	Encode func(v T) ([]byte, error)
}

// Load reads the document. An absent file is the zero T and no error: nothing has
// been written yet.
func (d Doc[T]) Load() (T, error) {
	var v T
	b, err := os.ReadFile(d.Path)
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if d.Decode != nil {
		err = d.Decode(b, &v)
	} else {
		err = json.Unmarshal(b, &v)
	}
	return v, err
}

// Update is the locked read-modify-write: it takes the lock at Path+".lock", waiting up
// to [LockWait] (see [WithLock]), loads the document as it is NOW, applies fn, and writes the result with
// [WriteFileAtomic]. A change another process made between this caller's last Load and
// its Update is therefore in what fn sees, never overwritten.
//
// Nothing is written when fn fails; fn returning [SkipWrite] is success without a
// write. ctx bounds only the wait for the lock.
func (d Doc[T]) Update(ctx context.Context, fn func(v *T) error) error {
	return WithLock(ctx, d.Path+".lock", LockWait, func() error {
		v, err := d.Load()
		if err != nil {
			return err
		}
		if err := fn(&v); err != nil {
			if errors.Is(err, SkipWrite) {
				return nil
			}
			return err
		}
		var b []byte
		if d.Encode != nil {
			b, err = d.Encode(v)
		} else {
			b, err = json.Marshal(v)
		}
		if err != nil {
			return err
		}
		return WriteFileAtomic(d.Path, b, cmp.Or(d.Perm, 0o644))
	})
}
