package interp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/file"
	teal "github.com/egladman/magus/internal/interp/engine/lua/teal"
)

// cacheDir returns $XDG_CACHE_HOME/magus/teal/ or $HOME/.cache/magus/teal/.
func cacheDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "magus", "teal")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// cacheKey returns sha256(teal.Version || teal.LuaTarget || BackendID || sourceBytes).
func cacheKey(source []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(teal.Version))
	_, _ = h.Write([]byte(teal.LuaTarget))
	_, _ = h.Write([]byte(ActiveBackend().ID()))
	_, _ = h.Write(source)
	return hex.EncodeToString(h.Sum(nil))
}

// lookup returns the cached compiled Lua bytes for key, or (nil, false) if absent.
func lookup(key string) ([]byte, bool) {
	dir, err := cacheDir()
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(dir, key+".lua"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		slog.Warn("interp: teal cache read failed", slog.String("error", err.Error()))
		return nil, false
	}
	return data, true
}

// store writes compiled bytes to cacheDir/<key>.lua atomically.
func store(key string, compiled []byte) error {
	dir, err := cacheDir()
	if err != nil {
		return err
	}
	return file.WriteFileAtomic(filepath.Join(dir, key+".lua"), compiled, 0o644)
}
