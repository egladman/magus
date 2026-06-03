package std

import (
	"context"
	"fmt"
	"io"
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
			Name:    "ext",
			Doc:     "File-name extension of path, including the leading dot (\"\" if none).",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    FsExt,
		},
		{
			Name:    "is_dir",
			Doc:     "True iff path exists and is a directory (a sandbox-denied path reads as false).",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    FsIsDir,
		},
		{
			Name:    "is_file",
			Doc:     "True iff path exists and is a regular file (a sandbox-denied path reads as false).",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    FsIsFile,
		},
		{
			Name:    "stat",
			Doc:     "Return metadata for path as {size, mtime, mode, is_dir}: size in bytes, mtime as Unix millis, mode as the integer permission bits. Errors if path is missing.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    FsStat,
		},
		{
			Name:    "copy_file",
			Doc:     "Copy the file at src to dst (overwriting), preserving its permission bits.",
			Args:    []Arg{{Name: "src", Type: TypeString}, {Name: "dst", Type: TypeString}},
			Returns: nil,
			Impl:    FsCopyFile,
		},
		{
			Name:    "copy_dir",
			Doc:     "Recursively copy the directory tree at src to dst, preserving permission bits.",
			Args:    []Arg{{Name: "src", Type: TypeString}, {Name: "dst", Type: TypeString}},
			Returns: nil,
			Impl:    FsCopyDir,
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

// FsExt returns the file-name extension of path (including the leading dot).
func FsExt(_ context.Context, path string) (string, error) {
	return filepath.Ext(path), nil
}

// FsIsDir reports whether path exists and is a directory. Like FsExists, a
// sandbox-denied path is reported as false rather than raising, so the predicate
// is safe to use as a probe.
func FsIsDir(ctx context.Context, path string) (bool, error) {
	if err := checkRead(ctx, path); err != nil {
		return false, nil //nolint:nilerr // sandbox-denied path is reported as non-existent by design
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir(), nil
}

// FsIsFile reports whether path exists and is a regular file. A sandbox-denied
// path is reported as false (see FsIsDir).
func FsIsFile(ctx context.Context, path string) (bool, error) {
	if err := checkRead(ctx, path); err != nil {
		return false, nil //nolint:nilerr // sandbox-denied path is reported as non-existent by design
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular(), nil
}

// FsStat returns metadata for path as {size, mtime, mode, is_dir}, subject to the
// sandbox read policy. Unlike the probe predicates a missing path is an error,
// since a caller asking for metadata expects the entry to exist.
func FsStat(ctx context.Context, path string) (map[string]any, error) {
	if err := checkRead(ctx, path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("fs.stat %q: %w", path, err)
	}
	return map[string]any{
		"size":   info.Size(),
		"mtime":  float64(info.ModTime().UnixMilli()),
		"mode":   int64(info.Mode().Perm()),
		"is_dir": info.IsDir(),
	}, nil
}

// FsCopyFile copies src to dst (overwriting), preserving src's permission bits.
// Both ends are subject to the sandbox policy: src must be readable, dst writable.
func FsCopyFile(ctx context.Context, src, dst string) error {
	if err := checkRead(ctx, src); err != nil {
		return err
	}
	if err := checkWrite(ctx, dst); err != nil {
		return err
	}
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("fs.copy_file %q -> %q: %w", src, dst, err)
	}
	return nil
}

// FsCopyDir recursively copies the directory tree at src to dst, preserving
// permission bits. Each source entry is checked for read and each destination
// for write, so a sandbox-denied path stops the copy with a diag error.
func FsCopyDir(ctx context.Context, src, dst string) error {
	walkErr := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if err := checkWrite(ctx, target); err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if err := checkRead(ctx, path); err != nil {
			return err
		}
		if err := checkWrite(ctx, target); err != nil {
			return err
		}
		return copyFile(path, target)
	})
	if walkErr != nil {
		return fmt.Errorf("fs.copy_dir %q -> %q: %w", src, dst, walkErr)
	}
	return nil
}

// copyFile streams src to dst, creating or truncating dst with src's permission
// bits. It is the unguarded primitive behind FsCopyFile/FsCopyDir; callers apply
// the sandbox checks.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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
