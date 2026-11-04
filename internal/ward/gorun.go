package ward

import (
	"encoding/hex"
	"path/filepath"
	"strings"
)

// GoRunBinary reports whether path, a resolved executable, sits where `go run` puts what
// it builds, as [BootstrapCommand] does: the link directory <tmp>/go-build<N>/b001/exe/<name>,
// or, since Go 1.24 caches go run executables, the build cache entry
// <GOCACHE>/<xx>/<output id>-d/<name>. Such a binary is a fresh build of its tree and
// must never hand off to a placed ./magus, which may be too old to load it.
func GoRunBinary(path string) bool {
	entry := filepath.Dir(path)
	if filepath.Base(entry) == "exe" {
		for dir := filepath.Dir(entry); filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
			if strings.HasPrefix(filepath.Base(dir), "go-build") {
				return true
			}
		}
		return false
	}
	id, ok := strings.CutSuffix(filepath.Base(entry), "-d")
	if _, err := hex.DecodeString(id); !ok || err != nil || len(id) != 64 {
		return false
	}
	return filepath.Base(filepath.Dir(entry)) == id[:2]
}
