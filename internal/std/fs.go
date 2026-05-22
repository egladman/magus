package std

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/types"
)

//go:generate go run ../../cmd/magus-bindings-gen -module fs -lang buzz -out gen/buzz/fs.go

func init() { Register(Fs) }

// Fs is the "fs" host module: filesystem and path primitives.
var Fs = Module{
	Name: "fs",
	Doc:  "Filesystem and path primitives.",
	Methods: []Method{
		{
			Name:    "glob",
			Doc:     "Return paths matching pattern (doublestar-style).",
			Args:    []Arg{{Name: "pattern", Type: TypeString}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    FsGlob,
		},
		{
			Name:    "dirname",
			Doc:     "Directory portion of path.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    FsDirname,
		},
		{
			Name:    "basename",
			Doc:     "Final element of path.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    FsBasename,
		},
		{
			Name:    "exists",
			Doc:     "True iff path exists.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    FsExists,
		},
		{
			Name:    "read_file",
			Doc:     "Return the contents of path as a string.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    FsReadFile,
		},
		{
			Name:    "write_file",
			Doc:     "Write content to path (mode 0644).",
			Args:    []Arg{{Name: "path", Type: TypeString}, {Name: "content", Type: TypeString}},
			Returns: nil,
			Impl:    FsWriteFile,
		},
		{
			Name: "mkdirall",
			Doc:  "Create path and parents (default mode 0755).",
			Args: []Arg{
				{Name: "path", Type: TypeString},
				{Name: "perm", Type: TypeInt, Optional: true, Default: int(0o755)},
			},
			Returns: nil,
			Impl:    FsMkdirAll,
		},
		{
			Name:    "join",
			Doc:     "Join path elements with the OS separator.",
			Args:    []Arg{{Name: "parts", Type: TypeString, Variadic: true}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    FsJoin,
		},
		{
			Name:    "remove_all",
			Doc:     "Recursively remove path (no error if missing).",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: nil,
			Impl:    FsRemoveAll,
		},
		{
			Name:    "list_dir",
			Doc:     "Return directory entries; empty if path does not exist.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    FsListDir,
		},
		{
			Name: "watch",
			Doc:  "Blocking. Watch paths (directories, recursively) and call callback with each debounced batch of changed paths until the callback returns true or the run is interrupted.",
			Args: []Arg{
				{Name: "paths", Type: TypeStringSlice},
				{Name: "callback", Type: TypeFunc},
			},
			Returns: nil,
			Impl:    FsWatch,
		},
	},
}

// FsGlob returns paths matching the doublestar pattern, filtered to those the
// sandbox policy permits reading.
func FsGlob(ctx context.Context, pattern string) ([]string, error) {
	matches, err := doublestar.FilepathGlob(pattern)
	if err != nil {
		return nil, fmt.Errorf("fs.glob %q: %w", pattern, err)
	}
	p := sandbox.FromContext(ctx)
	if p == nil {
		return matches, nil
	}
	// Filter out paths the policy denies so spells cannot enumerate filenames
	// outside the allowlist even when the actual read would later be blocked.
	allowed := matches[:0]
	for _, m := range matches {
		if p.CheckRead(m) == nil {
			allowed = append(allowed, m)
		}
	}
	return allowed, nil
}

// FsDirname returns the directory portion of path.
func FsDirname(_ context.Context, path string) (string, error) {
	return filepath.Dir(path), nil
}

// FsBasename returns the final element of path.
func FsBasename(_ context.Context, path string) (string, error) {
	return filepath.Base(path), nil
}

// FsExists reports whether path exists; a sandbox-denied path is reported as absent.
func FsExists(ctx context.Context, path string) (bool, error) {
	if err := checkRead(ctx, path); err != nil {
		// Treat a sandbox-denied path as "does not exist" rather than
		// raising — many magusfiles call fs.exists as a probe and a hard
		// error would break unrelated checks for paths the spell is
		// allowed to touch.
		return false, nil //nolint:nilerr // sandbox-denied path is reported as non-existent by design
	}
	_, err := os.Stat(path)
	return err == nil, nil
}

// FsReadFile returns the contents of path as a string, subject to the sandbox read policy.
func FsReadFile(ctx context.Context, path string) (string, error) {
	if err := checkRead(ctx, path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("fs.read_file %q: %w", path, err)
	}
	return string(data), nil
}

// FsWriteFile writes content to path (mode 0644), subject to the sandbox write policy.
func FsWriteFile(ctx context.Context, path string, content string) error {
	if err := checkWrite(ctx, path); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("fs.write_file %q: %w", path, err)
	}
	return nil
}

// FsMkdirAll creates path and any missing parents with the given mode, subject to the sandbox write policy.
func FsMkdirAll(ctx context.Context, path string, perm int) error {
	if err := checkWrite(ctx, path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, os.FileMode(perm)); err != nil {
		return fmt.Errorf("fs.mkdirall %q: %w", path, err)
	}
	return nil
}

// FsJoin joins path elements with the OS separator.
func FsJoin(_ context.Context, parts ...string) (string, error) {
	return filepath.Join(parts...), nil
}

// FsRemoveAll recursively removes path (no error if missing), subject to the sandbox write policy.
func FsRemoveAll(ctx context.Context, path string) error {
	if err := checkWrite(ctx, path); err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("fs.remove_all %q: %w", path, err)
	}
	return nil
}

// FsListDir returns the entry names in path, or nil if it does not exist, subject to the sandbox read policy.
func FsListDir(ctx context.Context, path string) ([]string, error) {
	if err := checkRead(ctx, path); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fs.list_dir %q: %w", path, err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}

// checkRead returns a MGS2001 diag error when ctx carries a sandbox policy
// that denies path. nil otherwise (sandbox off or path allowed).
func checkRead(ctx context.Context, path string) error {
	p := sandbox.FromContext(ctx)
	if p == nil {
		return nil
	}
	if err := p.CheckRead(path); err != nil {
		sandbox.EmitDenyHint("ro", path)
		return types.DiagnosticErrorf(types.PathReadDenied, "fs read denied: %s", path)
	}
	return nil
}

// checkWrite returns a MGS2002 diag error when ctx carries a sandbox policy
// that denies path for writing.
func checkWrite(ctx context.Context, path string) error {
	p := sandbox.FromContext(ctx)
	if p == nil {
		return nil
	}
	if err := p.CheckWrite(path); err != nil {
		sandbox.EmitDenyHint("rw", path)
		return types.DiagnosticErrorf(types.PathWriteDenied, "fs write denied: %s", path)
	}
	return nil
}

// FsWatch is BLOCKING: it watches paths (directories, recursively) for changes
// and invokes cb with each debounced batch of changed paths — relative to the
// current directory — until cb returns true or the run is cancelled (Ctrl-C).
// Editor/VCS noise (.git, build caches, …) is filtered by the built-in ignore
// set. It returns nil on a clean stop; a watcher setup error or an error raised
// by the callback propagates. Because it holds its session for its whole life,
// the idiomatic use is a reactive loop (rebuild on change) run as its own
// target; parallelism comes from magus running other targets concurrently.
func FsWatch(ctx context.Context, paths []string, cb Callback) error {
	if len(paths) == 0 {
		return fmt.Errorf("fs.watch: at least one path is required")
	}
	opts := []watch.Option{watch.WithIgnore(watch.BuiltinIgnore)}
	for _, p := range paths {
		if err := checkRead(ctx, p); err != nil {
			return err
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("fs.watch %q: %w", p, err)
		}
		opts = append(opts, watch.WithRoot(abs))
	}
	w, err := watch.New(ctx, opts...)
	if err != nil {
		return fmt.Errorf("fs.watch: %w", err)
	}
	defer func() { _ = w.Close() }()

	cwd, _ := os.Getwd()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-w.Errors():
			if err != nil {
				slog.WarnContext(ctx, "fs.watch", slog.String("error", err.Error()))
			}
		case batch, ok := <-w.Events():
			if !ok {
				return nil
			}
			ret, err := cb.Call(ctx, relToCwd(cwd, batch.Paths))
			if err != nil {
				return err
			}
			if callbackTruthy(ret) {
				return nil
			}
		}
	}
}

// relToCwd renders absolute watch paths relative to base when possible, so the
// callback sees the same project-relative paths the rest of fs.* works with.
func relToCwd(base string, abs []string) []string {
	out := make([]string, len(abs))
	for i, p := range abs {
		if rel, err := filepath.Rel(base, p); err == nil {
			out[i] = rel
		} else {
			out[i] = p
		}
	}
	return out
}

// callbackTruthy reports whether a callback's first return value is truthy,
// matching the host predicate convention (nil/false → false; a bool → its value;
// any other value → true).
func callbackTruthy(ret []any) bool {
	if len(ret) == 0 {
		return false
	}
	switch v := ret[0].(type) {
	case nil:
		return false
	case bool:
		return v
	default:
		return true
	}
}
