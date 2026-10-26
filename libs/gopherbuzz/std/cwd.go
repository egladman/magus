package std

import (
	"context"
	"path/filepath"
)

// cwdKey carries the working directory the file and exec builtins resolve
// relative paths against. An embedder (e.g. magus) sets it per run so a script
// behaves as if it ran from that directory — without os.Chdir-ing the whole
// process, which would race across concurrent runs sharing one process.
type cwdKey struct{}

// WithCurrentWorkingDirectory returns ctx carrying dir as the working directory the std builtins
// (fs.*, io.File/io.runFile, os.execute) resolve relative paths against. An empty
// dir is a no-op. Exported so an embedder can establish it; the stdlib reads it
// internally via resolve. Absolute paths and scripts run without a cwd are
// unaffected, so this extends the standard library's behavior rather than
// changing it.
func WithCurrentWorkingDirectory(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, cwdKey{}, dir)
}

// currentWorkingDirectoryFromContext returns the context working directory, or "" when none is set.
func currentWorkingDirectoryFromContext(ctx context.Context) string {
	d, _ := ctx.Value(cwdKey{}).(string)
	return d
}

// CurrentWorkingDirectoryFromContext returns the working directory set by WithCurrentWorkingDirectory and whether one is
// set. It is the reader counterpart to WithCurrentWorkingDirectory, so an embedder that layers its own
// cwd on top (e.g. magus) can confirm the value propagated into this stdlib.
func CurrentWorkingDirectoryFromContext(ctx context.Context) (string, bool) {
	d := currentWorkingDirectoryFromContext(ctx)
	return d, d != ""
}

// resolve resolves a (possibly relative) path against the context working
// directory. It is a no-op — returning path unchanged — when path is absolute or
// no cwd is set, so a script run without an embedder-set cwd resolves against the
// process working directory exactly as before.
func resolve(ctx context.Context, path string) string {
	base := currentWorkingDirectoryFromContext(ctx)
	if base == "" || path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}
