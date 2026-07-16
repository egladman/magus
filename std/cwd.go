package std

// Working-directory helpers shared across the host modules. These live in their
// own file (rather than in os.go) because the pure-compute modules need them too:
// crypto.sha256File resolves its path through resolvePath, so the helper must stay
// in the wasm build even though os.go (and the other IO modules) are excluded
// there. Keeping the cwd plumbing IO-free is what lets the browser playground
// register the pure modules without dragging in the process/filesystem leaves.

import (
	"context"
	"os"
	"path/filepath"

	buzzstd "github.com/egladman/gopherbuzz/std"
)

// cwdKey carries the default working directory for the exec primitives.
// A spell target's dispatcher sets it (host.WithCwd) so os.exec/os.exec_sh
// run in the project directory without the spell passing it explicitly. Magusfile
// targets leave it unset and instead run under a process chdir, so an unset cwd
// resolves to the process working directory.
type cwdKey struct{}

// WithCwd returns ctx carrying dir as the default working directory for the
// os.* exec primitives. An empty dir is a no-op. It also propagates the cwd to
// Buzz's own stdlib (gopherbuzz io/fs/os) so a magusfile that uses the language
// built-ins - io.File, fs.list, os.execute - resolves relative paths against the
// project dir too, not just the magus host modules. magus extends Buzz's standard
// library; it does not replace it.
func WithCwd(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	ctx = context.WithValue(ctx, cwdKey{}, dir)
	return buzzstd.WithCwd(ctx, dir)
}

// cwdFromContext returns the context working directory, or "" when unset.
func cwdFromContext(ctx context.Context) string {
	d, _ := ctx.Value(cwdKey{}).(string)
	return d
}

// CwdFromContext returns the working directory carried by WithCwd and whether one
// is set. Unlike EffectiveCwd it does not fall back to the process cwd, so a log
// handler can attach the directory only when a magusfile target actually
// established one - the process cwd is no longer meaningful for that correlation.
func CwdFromContext(ctx context.Context) (string, bool) {
	d, ok := ctx.Value(cwdKey{}).(string)
	return d, ok && d != ""
}

// resolvePath resolves a (possibly relative) filesystem path against the context
// working directory set by WithCwd. It is a no-op - returning path unchanged -
// when path is absolute or no context cwd is set, so callers that never set a cwd
// keep resolving against the process working directory exactly as before. The
// magusfile runner sets the cwd (instead of os.Chdir-ing the whole process) so
// targets across projects can execute concurrently; every host module that
// touches the filesystem routes its path argument through here so relative paths
// still resolve against the project directory.
func resolvePath(ctx context.Context, path string) string {
	base := cwdFromContext(ctx)
	if base == "" || path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

// EffectiveCwd reports the working directory a host module should treat as "."
// - the context cwd when set (the magusfile runner sets it per target), else the
// process working directory. Exported so the interp host bindings can resolve
// workspace-local paths against the same base the std modules use.
func EffectiveCwd(ctx context.Context) (string, error) {
	if base := cwdFromContext(ctx); base != "" {
		return base, nil
	}
	return os.Getwd()
}
