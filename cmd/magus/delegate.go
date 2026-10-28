package main

import (
	"encoding/hex"
	"path/filepath"
	"strings"
)

// builtByGoRun reports whether this process is a `go run` build of cmd/magus, such as the
// stale-binary escape: a fresh build of the tree that must never hand off to the ./magus
// it exists to replace, which may be too old to load the workspace.
func builtByGoRun() bool {
	self, err := runningBinaryPath()
	return err == nil && goRunBinary(self)
}

// goRunBinary reports whether self sits where `go run` puts what it builds: the link
// directory <tmp>/go-build<N>/b001/exe/<name>, or, since Go 1.24 caches go run
// executables, the build cache entry <GOCACHE>/<xx>/<output id>-d/<name>.
func goRunBinary(self string) bool {
	entry := filepath.Dir(self)
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
