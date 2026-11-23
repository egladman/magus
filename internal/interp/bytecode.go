package interp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/log/attr"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/secrets"
)

// The HMAC binds a chunk to its directory and key so copied chunks fail
// verification outside their original cache entry.
type sealedBytecodeStore struct {
	inner  buzz.BytecodeStore
	secret secrets.Secret
	scope  string
}

var errUnsealedChunk = errors.New("bytecode chunk does not verify")

func (s sealedBytecodeStore) Load(key string) ([]byte, error) {
	sealed, err := s.inner.Load(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < sha256.Size || !hmac.Equal(sealed[:sha256.Size], s.seal(key, sealed[sha256.Size:])) {
		return nil, errUnsealedChunk
	}
	return sealed[sha256.Size:], nil
}

func (s sealedBytecodeStore) Store(key string, blob []byte) error {
	now := time.Now()
	stampErr := prepareStamp(filepath.Dir(s.scope), now)
	if err := s.inner.Store(key, append(s.seal(key, blob), blob...)); err != nil {
		return err
	}
	if stampErr != nil {
		slog.With(attr.Component("interp")).WarnContext(context.Background(), "could not refresh compiler cache timestamp", attr.Error(stampErr))
	}
	if err := sweepChunks(s.scope, now); err != nil {
		slog.With(attr.Component("interp")).WarnContext(context.Background(), "could not sweep compiler cache", attr.Error(err))
	}
	return nil
}

func (s sealedBytecodeStore) seal(key string, blob []byte) []byte {
	m := hmac.New(sha256.New, []byte(s.secret.Reveal()))
	for _, part := range []string{s.scope, key} {
		_, _ = m.Write([]byte(part))
		_, _ = m.Write([]byte{0})
	}
	_, _ = m.Write(blob)
	return m.Sum(nil)
}
