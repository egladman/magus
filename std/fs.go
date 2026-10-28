//go:build !wasm

package std

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/readlog"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/libs/image"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

//go:generate go run ../cmd/magus-utils bindings -module fs -lang buzz -out ../internal/interp/bindings/gen/fs.go -pkg bindgen

// Fs is the "fs" host module: filesystem and path primitives.
var Fs = Module{
	Name: "fs",
	Doc:  "Filesystem and path primitives.",
	Methods: []Method{
		{
			Name:    "glob",
			Doc:     "Return paths matching pattern (doublestar-style).",
			Args:    []Arg{{Name: "pattern", Type: TypeString}},
			Returns: []Ret{{Type: TypeAny, Object: "[Path]"}},
			Raises:  true,
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
			Raises:  true,
			Impl:    FsExists,
		},
		{
			Name:    "read_file",
			Doc:     "Return the contents of path as a string.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    FsReadFile,
		},
		{
			Name:    "write_file",
			Doc:     "Write content to path (mode 0644).",
			Args:    []Arg{{Name: "path", Type: TypeString}, {Name: "content", Type: TypeString}},
			Returns: nil,
			Raises:  true,
			Impl:    FsWriteFile,
		},
		{
			Name: "mkdir_all",
			Doc:  "Create path and parents (default mode 0755).",
			Args: []Arg{
				{Name: "path", Type: TypeString},
				{Name: "perm", Type: TypeInt, Optional: true, Default: int(0o755)},
			},
			Returns: nil,
			Raises:  true,
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
			Raises:  true,
			Impl:    FsRemoveAll,
		},
		{
			Name:    "remove",
			Doc:     "Remove a single file or empty directory (no error if missing). Unlike remove_all it refuses a non-empty directory, so a wrong path costs one error rather than a recursive delete.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: nil,
			Raises:  true,
			Impl:    FsRemove,
		},
		{
			Name:    "rename",
			Doc:     "Move or rename src to dst, creating dst's parent directory if needed. Within one filesystem this is atomic, which is what makes it the last step of a write-to-temp-then-swap. Across filesystems the underlying rename fails rather than silently copying; copy_file plus remove is the explicit form for that.",
			Args:    []Arg{{Name: "src", Type: TypeString}, {Name: "dst", Type: TypeString}},
			Returns: nil,
			Raises:  true,
			Impl:    FsRename,
		},
		{
			Name:    "size",
			Doc:     "Return path's size in bytes. Raises when path does not exist; stat returns the whole FileInfo when more than the size is wanted.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeInt}},
			Raises:  true,
			Impl:    FsSize,
		},
		{
			Name: "temp_file",
			Doc:  "Create a new empty temporary file with an optional name prefix and return its path. It is made in the sandbox's private temp dir (the TMPDIR its children get) when the sandbox is on, in the system temp dir otherwise. The file is left in place for the caller to write and remove; temp_dir is the form for a whole tree.",
			Args: []Arg{
				{Name: "prefix", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    FsTempFile,
		},
		{
			Name: "write_file_atomic",
			Doc:  "Write content to path so a reader sees either the old bytes or the new ones, never a partial file: the content goes to a temporary file in the same directory, is flushed to disk, then renamed over path. Use it for anything another process may read while a target runs - a generated file, a lockfile, a cache index. write_file is the cheaper form when nothing else is looking.",
			Args: []Arg{
				{Name: "path", Type: TypeString},
				{Name: "content", Type: TypeString},
			},
			Returns: nil,
			Raises:  true,
			Impl:    FsWriteFileAtomic,
		},
		{
			Name:    "list_dir",
			Doc:     "Return directory entries; empty if path does not exist.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Raises:  true,
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
			Doc:     "True iff path exists and is a directory. A sandbox-denied path raises rather than reading as false.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeBool}},
			Raises:  true,
			Impl:    FsIsDir,
		},
		{
			Name:    "is_file",
			Doc:     "True iff path exists and is a regular file. A sandbox-denied path raises rather than reading as false.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeBool}},
			Raises:  true,
			Impl:    FsIsFile,
		},
		{
			Name:    "stat",
			Doc:     "Return metadata for path as {size, mtime, mode, is_dir}: size in bytes, mtime as Unix millis, mode as the integer permission bits. Errors if path is missing.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "FileInfo"}},
			Raises:  true,
			Impl:    FsStat,
		},
		{
			Name:    "copy_file",
			Doc:     "Copy the file at src to dst (overwriting), preserving its permission bits.",
			Args:    []Arg{{Name: "src", Type: TypeString}, {Name: "dst", Type: TypeString}},
			Returns: nil,
			Raises:  true,
			Impl:    FsCopyFile,
		},
		{
			Name:    "copy_dir",
			Doc:     "Recursively copy the directory tree at src to dst, preserving permission bits.",
			Args:    []Arg{{Name: "src", Type: TypeString}, {Name: "dst", Type: TypeString}},
			Returns: nil,
			Raises:  true,
			Impl:    FsCopyDir,
		},
		{
			Name: "watch",
			Doc:  "Blocking. Watch paths (directories, recursively) and call callback with each debounced batch of changed paths until the callback returns true or the run is interrupted.",
			Args: []Arg{
				{Name: "paths", Type: TypeStringSlice},
				{Name: "callback", Type: TypeFunc, Func: "fun (paths: [str]) > bool !> any"},
			},
			Returns: nil,
			Raises:  true,
			Impl:    FsWatch,
		},
		{
			Name: "walk",
			Doc:  "Recursively walk the directory tree rooted at root, calling callback(path, is_dir) for each entry. Return true from callback to stop the walk early. Sandbox-denied entries are silently skipped.",
			Args: []Arg{
				{Name: "root", Type: TypeString},
				{Name: "callback", Type: TypeFunc, Func: "fun (path: str, isDir: bool) > bool !> any"},
			},
			Returns: nil,
			Raises:  true,
			Impl:    FsWalk,
		},
		{
			Name:    "append_file",
			Doc:     "Append content to path (creating if absent, mode 0644).",
			Args:    []Arg{{Name: "path", Type: TypeString}, {Name: "content", Type: TypeString}},
			Returns: nil,
			Raises:  true,
			Impl:    FsAppendFile,
		},
		{
			Name:    "mode",
			Doc:     "Permission bits from octal (\"0755\", \"1777\") or ls notation (\"rwxr-xr-x\"), for chmod, mkdirAll and image placements. Zero and bits beyond setuid, setgid and sticky raise.",
			Args:    []Arg{{Name: "notation", Type: TypeString}},
			Returns: []Ret{{Type: TypeInt}},
			Raises:  true,
			Impl:    FsMode,
		},
		{
			Name:    "chmod",
			Doc:     "Change the permission bits of path to mode (an integer; fs\\mode(\"0755\") spells one).",
			Args:    []Arg{{Name: "path", Type: TypeString}, {Name: "mode", Type: TypeInt}},
			Returns: nil,
			Raises:  true,
			Impl:    FsChmod,
		},
		{
			Name:    "symlink",
			Doc:     "Create a symbolic link at link pointing to target.",
			Args:    []Arg{{Name: "target", Type: TypeString}, {Name: "link", Type: TypeString}},
			Returns: nil,
			Raises:  true,
			Impl:    FsSymlink,
		},
		{
			Name:    "readlink",
			Doc:     "Return the target of the symbolic link at path.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    FsReadlink,
		},
		{
			Name: "temp_dir",
			Doc:  "Create a new temporary directory with an optional name prefix and return its path. It is made in the sandbox's private temp dir (the TMPDIR its children get) when the sandbox is on, in the system temp dir otherwise.",
			Args: []Arg{
				{Name: "prefix", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    FsTempDir,
		},
		{
			Name:    "scratch_checkout_dir",
			Doc:     "Create a new empty 0700 directory for one disposable copy of a repository, in $XDG_CACHE_HOME/magus/checkouts/<purpose>, and return its path. purpose names the feature making copies and must be one path element; the parent of the result is the directory that feature cleans, and one mise trusted_config_paths entry for magus/checkouts trusts every copy. The caller removes the copy. Plain scratch files belong in temp_dir.",
			Args:    []Arg{{Name: "purpose", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    FsScratchCheckoutDir,
		},
		{
			Name:    "read_lines",
			Doc:     "Read path and return its lines as a list, with the line terminators stripped. A single trailing newline yields no extra empty element; an empty file yields an empty list.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Raises:  true,
			Impl:    FsReadLines,
		},
		{
			Name:    "write_lines",
			Doc:     "Write lines to path (mode 0644), each followed by a newline. The companion to read_lines: write_lines(p, read_lines(p)) round-trips a newline-terminated file.",
			Args:    []Arg{{Name: "path", Type: TypeString}, {Name: "lines", Type: TypeStringSlice}},
			Returns: nil,
			Raises:  true,
			Impl:    FsWriteLines,
		},
		{
			Name:    "disk_usage",
			Doc:     "Return {path, totalBytes, freeBytes} for the filesystem holding path: statfs on unix, GetDiskFreeSpaceEx on Windows. freeBytes is what this process may still write, so it leaves out the blocks reserved for root.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "DiskUsage"}},
			Raises:  true,
			Impl:    FsDiskUsage,
		},
		{
			Name:    "state_dir",
			Doc:     "Return $XDG_STATE_HOME/magus/<purpose> (LocalAppData on Windows, else ~/.local/state), created 0700: the per-user home for state nothing can rebuild and that outlives any one workspace, such as a store several checkouts append to. purpose must be one path element.",
			Args:    []Arg{{Name: "purpose", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    FsStateDir,
		},
		{
			Name:    "trim_oldest",
			Doc:     "Remove the least recently modified files under dir, whole days of age at a time, until what is left fits in maxBytes, and return a TrimReport {dir, beforeBytes, afterBytes, removedFiles, removedBytes, cutoffMs, stopped}. Files modified within the last day are never removed, so the directory is never emptied; stopped says when they alone pass the budget. Two walks with one counter per day of age, so it suits a cache of millions of files. timeoutMs bounds the trim, which then stops between files and says so in stopped. Directories stay.",
			Args: []Arg{
				{Name: "dir", Type: TypeString},
				{Name: "maxBytes", Type: TypeInt},
				{Name: "timeoutMs", Type: TypeInt, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "TrimReport"}},
			Raises:  true,
			Impl:    FsTrimOldest,
		},
	},
}

// FsGlob returns paths matching the doublestar pattern, filtered to those the
// sandbox policy permits reading.
func FsGlob(ctx context.Context, pattern string) ([]types.Path, error) {
	// Glob against the project dir (resolvePath is a no-op without a context cwd),
	// then report matches relative to it so the returned paths read like the
	// pattern the caller passed, independent of the process working directory.
	//
	// That base is carried on each result rather than discarded. It was already computed
	// here and thrown away, so every caller had to know out of band which directory the
	// strings were measured from, and a target's cwd is its PROJECT directory, not the
	// workspace root, which is exactly the distinction a bare string cannot express.
	// EffectiveCurrentWorkingDirectory, not currentWorkingDirectoryFromContext: outside a target there is no context cwd, but the
	// matches are still relative (to the PROCESS cwd), so reporting an empty base would
	// hand back a relative value that names nothing. EffectiveCurrentWorkingDirectory falls back to the
	// process cwd, which makes "a glob result always knows its base" total rather than
	// true-only-inside-a-target.
	base, cwdErr := EffectiveCurrentWorkingDirectory(ctx)
	if cwdErr != nil {
		base = ""
	}
	matches, err := doublestar.FilepathGlob(resolvePath(ctx, pattern))
	if err != nil {
		return nil, fmt.Errorf("fs.glob %q: %w", pattern, err)
	}
	p := sandbox.PolicyFromContext(ctx)
	allowed := make([]types.Path, 0, len(matches))
	for _, m := range matches {
		// The sandbox sees the absolute match; the caller sees it relative to base.
		if p != nil && p.CheckRead(ctx, m) != nil {
			continue
		}
		// Rel only succeeds when both sides are absolute; a match that is already
		// relative to the process cwd is left as-is, and the base names it either way.
		if base != "" {
			if rel, rerr := filepath.Rel(base, m); rerr == nil {
				m = rel
			}
		}
		allowed = append(allowed, types.Path{Value: m, Base: base})
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

// FsExists reports whether path exists. A sandbox-denied path RAISES rather than
// being reported as absent (see the inline comment below for why).
func FsExists(ctx context.Context, path string) (bool, error) {
	path = resolvePath(ctx, path)
	if err := checkRead(ctx, path); err != nil {
		// RAISE the policy error rather than answering "does not exist".
		//
		// Reporting false conflated two different facts: "the sandbox will not let me
		// look" and "there is nothing there". A caller acts on those in opposite ways:
		// `if (!fs\exists(p)) { write(p); }` then attempts a write that is ALSO denied,
		// and the failure surfaces far from the policy that caused it.
		//
		// Raising leaks nothing extra: checkRead's diagnostic names the RULE that denied
		// the path, which the workspace author configured, and says nothing about whether
		// the file is there.
		return false, err
	}
	readlog.File(ctx, path)
	_, err := os.Stat(path)
	return err == nil, nil
}

// FsReadFile returns the contents of path as a string, subject to the sandbox read policy.
func FsReadFile(ctx context.Context, path string) (string, error) {
	path = resolvePath(ctx, path)
	if err := checkRead(ctx, path); err != nil {
		return "", err
	}
	readlog.File(ctx, path)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("fs.read_file %q: %w", path, err)
	}
	return string(data), nil
}

// FsWriteFile writes content to path (mode 0644), subject to the sandbox write policy.
func FsWriteFile(ctx context.Context, path string, content string) error {
	if types.Tracing(ctx) {
		return nil
	}
	path = resolvePath(ctx, path)
	if err := checkWrite(ctx, path); err != nil {
		return err
	}
	if err := checkSchemaDowngrade(ctx, path); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("fs.write_file %q: %w", path, err)
	}
	return nil
}

// FsMkdirAll creates path and any missing parents with the given mode, subject to the sandbox write policy.
func FsMkdirAll(ctx context.Context, path string, perm int) error {
	if types.Tracing(ctx) {
		return nil
	}
	path = resolvePath(ctx, path)
	if err := checkWrite(ctx, path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, os.FileMode(perm)); err != nil {
		return fmt.Errorf("fs.mkdirAll %q: %w", path, err)
	}
	return nil
}

// FsJoin joins path elements with the OS separator.
func FsJoin(_ context.Context, parts ...string) (string, error) {
	return filepath.Join(parts...), nil
}

// FsRemoveAll recursively removes path (no error if missing), subject to the sandbox write policy.
func FsRemoveAll(ctx context.Context, path string) error {
	if types.Tracing(ctx) {
		return nil
	}
	path = resolvePath(ctx, path)
	if err := checkWrite(ctx, path); err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("fs.remove_all %q: %w", path, err)
	}
	return nil
}

// FsRemove removes a single file or empty directory, subject to the sandbox write
// policy. A missing path is not an error, matching remove_all.
func FsRemove(ctx context.Context, path string) error {
	if types.Tracing(ctx) {
		return nil
	}
	path = resolvePath(ctx, path)
	if err := checkWrite(ctx, path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("fs.remove %q: %w", path, err)
	}
	return nil
}

// FsRename moves src to dst, subject to the sandbox write policy on both ends.
// dst's parent is created first so the common "rename into a fresh output dir"
// does not need a separate mkdirall.
func FsRename(ctx context.Context, src, dst string) error {
	if types.Tracing(ctx) {
		return nil
	}
	src = resolvePath(ctx, src)
	dst = resolvePath(ctx, dst)
	// Both ends are writes: src loses its contents, dst gains them. Checking only
	// one would let a sandboxed target move a file out of, or into, a path its
	// policy denies.
	if err := checkWrite(ctx, src); err != nil {
		return err
	}
	if err := checkWrite(ctx, dst); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("fs.rename %q -> %q: %w", src, dst, err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("fs.rename %q -> %q: %w", src, dst, err)
	}
	return nil
}

// FsSize returns path's size in bytes, subject to the sandbox read policy.
func FsSize(ctx context.Context, path string) (int, error) {
	path = resolvePath(ctx, path)
	if err := checkRead(ctx, path); err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("fs.size %q: %w", path, err)
	}
	return int(info.Size()), nil
}

// FsTempFile creates an empty temporary file and returns its path, in the sandbox's
// private temp dir when a policy is attached.
func FsTempFile(ctx context.Context, prefix string) (string, error) {
	base := sandbox.PolicyFromContext(ctx).TempBase()
	if types.Tracing(ctx) {
		// Dry run: name a plausible path without creating it, matching temp_dir.
		// Writes to it are themselves recorded as skipped, so it never needs to exist.
		return filepath.Join(cmp.Or(base, os.TempDir()), prefix+"magus-dry-run"), nil
	}
	f, err := os.CreateTemp(base, prefix)
	if err != nil {
		return "", fmt.Errorf("fs.temp_file: %w", err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("fs.temp_file %q: %w", name, err)
	}
	return name, nil
}

// FsWriteFileAtomic writes content to path via a same-directory temporary file
// renamed into place, subject to the sandbox write policy.
func FsWriteFileAtomic(ctx context.Context, path string, content string) error {
	if types.Tracing(ctx) {
		return nil
	}
	path = resolvePath(ctx, path)
	if err := checkWrite(ctx, path); err != nil {
		return err
	}
	if err := checkSchemaDowngrade(ctx, path); err != nil {
		return err
	}
	// 0644, matching write_file, so an atomically written file is not readable by a
	// narrower set of users than a plain one.
	if err := file.WriteFileAtomic(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("fs.write_file_atomic %q: %w", path, err)
	}
	return nil
}

// FsListDir returns the entry names in path, or nil if it does not exist, subject to the sandbox read policy.
func FsListDir(ctx context.Context, path string) ([]string, error) {
	path = resolvePath(ctx, path)
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

// FsIsDir reports whether path exists and is a directory. A sandbox-denied path RAISES
// (see FsExists): "not allowed to look" is not the same answer as "not a directory".
func FsIsDir(ctx context.Context, path string) (bool, error) {
	path = resolvePath(ctx, path)
	if err := checkRead(ctx, path); err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir(), nil
}

// FsIsFile reports whether path exists and is a regular file. A sandbox-denied path RAISES
// (see FsExists).
func FsIsFile(ctx context.Context, path string) (bool, error) {
	path = resolvePath(ctx, path)
	if err := checkRead(ctx, path); err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular(), nil
}

// FsStat returns metadata for path as {size, mtime, mode, is_dir}, subject to the
// sandbox read policy. Unlike the probe predicates a missing path is an error,
// since a caller asking for metadata expects the entry to exist.
func FsStat(ctx context.Context, path string) (types.FileInfo, error) {
	path = resolvePath(ctx, path)
	if err := checkRead(ctx, path); err != nil {
		return types.FileInfo{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return types.FileInfo{}, fmt.Errorf("fs.stat %q: %w", path, err)
	}
	return types.FileInfo{
		Size:  info.Size(),
		Mtime: float64(info.ModTime().UnixMilli()),
		Mode:  int64(info.Mode().Perm()),
		IsDir: info.IsDir(),
	}, nil
}

// FsCopyFile copies src to dst (overwriting), preserving src's permission bits.
// Both ends are subject to the sandbox policy: src must be readable, dst writable.
func FsCopyFile(ctx context.Context, src, dst string) error {
	if types.Tracing(ctx) {
		return nil
	}
	src, dst = resolvePath(ctx, src), resolvePath(ctx, dst)
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
	if types.Tracing(ctx) {
		return nil
	}
	src, dst = resolvePath(ctx, src), resolvePath(ctx, dst)
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
			if err := checkRead(ctx, path); err != nil {
				return err
			}
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
	p := sandbox.PolicyFromContext(ctx)
	if p == nil {
		return nil
	}
	if err := p.CheckRead(ctx, path); err != nil {
		sandbox.EmitDenyHint(ctx, p, filesystem.Read, path)
		return types.DiagnosticErrorf(types.PathReadDenied, "fs read denied: %s", path)
	}
	return nil
}

// checkWrite returns a MGS2002 diag error when ctx carries a sandbox policy
// that denies path for writing.
func checkWrite(ctx context.Context, path string) error {
	p := sandbox.PolicyFromContext(ctx)
	if p == nil {
		return nil
	}
	if err := p.CheckWrite(ctx, path); err != nil {
		sandbox.EmitDenyHint(ctx, p, filesystem.Write, path)
		return types.DiagnosticErrorf(types.PathWriteDenied, "fs write denied: %s", path)
	}
	return nil
}

// schemaStampWindow bounds the read the downgrade guard makes. Every stamp the
// renderers write lands in a document's opening prose or an export's leading
// fields, so a window keeps the check off the size of a 2MB graph export.
const schemaStampWindow = 64 << 10

// checkSchemaDowngrade refuses a write that would replace generated output carrying a
// NEWER knowledge-schema stamp than this build renders.
//
// Bootstrapping is what makes this reachable: `magus run go-build .` is orchestrated
// by whatever magus is on PATH, so an installed binary older than the checkout drives
// the run and its in-process renderer answers magus\describe. Measured 2026-09-01: a
// PATH magus at schema v9 rewrote a committed v10 MAGUS.md down to v9 during a
// go-build, and nothing reported it, because a generator reached through ctx.needs
// runs inside its parent's body where no drift gate compares the result.
//
// It sits beside checkWrite because both answer whether a write may proceed, and
// every magusfile generator reaches the filesystem through this seam. The renderers
// that embed the stamp build strings and never learn a destination, so the
// precondition cannot live with them.
//
// A file with no stamp, an unreadable file, and an absent file all pass. The guard
// speaks only when it can read a version and that version is ahead of this build.
// So does a file the VCS ignores: nothing commits it, so overwriting it downgrades
// nothing, and a checkout switched between branches of different schemas would
// otherwise strand the ignored exports on the newer branch's stamp.
func checkSchemaDowngrade(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, schemaStampWindow)
	n, err := f.Read(head)
	if n == 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return nil
	}
	stamped, ok := types.StampedSchemaVersion(head[:n])
	if !ok || stamped <= types.KnowledgeSchemaVersion {
		return nil
	}
	if vcsIgnores(ctx, path) {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		self = "the running magus"
	}
	return types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus,
		"refusing to overwrite %s: it carries knowledge-schema v%d and this build renders v%d, "+
			"so regenerating it here would downgrade committed output. The running binary is %s. "+
			"Build one from this checkout with `magus run go-build .` and re-run the target with `./magus`.",
		path, stamped, types.KnowledgeSchemaVersion, self)
}

// vcsIgnores reports whether the VCS covering path's directory ignores it. A VCS that
// cannot answer ignores nothing, so the refusal stands: a missed ignore costs a rebuild,
// a guessed one overwrites committed output.
func vcsIgnores(ctx context.Context, path string) bool {
	dir := filepath.Dir(path)
	res, err := vcs.Resolve(ctx, dir, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return false
	}
	ignored, err := res.VCS.IgnoredFiles(ctx, dir, []string{filepath.Base(path)})
	return err == nil && len(ignored) > 0
}

// FsWatch is BLOCKING: it watches paths (directories, recursively) and invokes
// cb with each debounced batch of changed paths (relative to the current
// directory) until cb returns true or the run is cancelled (Ctrl-C). Editor/VCS
// noise (.git, build caches, ...) is filtered by the built-in ignore set. It
// returns nil on a clean stop; a watcher setup error or an error raised by the
// callback propagates. Because it holds its session for its whole life, the
// idiomatic use is a reactive loop (rebuild on change) run as its own target,
// with parallelism coming from magus running other targets concurrently.
func FsWatch(ctx context.Context, paths []string, cb Callback) error {
	if len(paths) == 0 {
		return fmt.Errorf("fs.watch: at least one path is required")
	}
	// Under dry-run tracing a target body is evaluated to learn its declarations,
	// not to run, and fs.watch would otherwise block forever on its event loop.
	// Return immediately (callback never invoked) so discovering a watch/serve
	// target does not hang.
	if types.Tracing(ctx) {
		return nil
	}
	opts := []watch.Option{watch.WithIgnore(watch.BuiltinIgnore)}
	for _, p := range paths {
		p = resolvePath(ctx, p)
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

	cwd, _ := EffectiveCurrentWorkingDirectory(ctx)
	// A watch loop is legitimately idle: it is the one supported shape that blocks
	// indefinitely while producing nothing, so the stall watchdog would abort it
	// (MGS3012). Beating while parked says "waiting on purpose" rather than "wedged".
	// Any other way of blocking forever is what MGS5003 already rejects.
	alive := time.NewTicker(30 * time.Second)
	defer alive.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-alive.C:
			cache.ProgressFromContext(ctx).Beat()
		case err := <-w.Errors():
			if err != nil {
				slog.WarnContext(ctx, "fs.watch", attr.Error(err))
			}
		case batch, ok := <-w.Events():
			if !ok {
				return nil
			}
			ret, err := cb.Call(ctx, relToCurrentWorkingDirectory(cwd, batch.Paths))
			if err != nil {
				return err
			}
			if callbackTruthy(ret) {
				return nil
			}
		}
	}
}

// FsWalk walks the directory tree rooted at root, calling cb(path, isDir) for
// each entry in lexical order. If the callback returns true the walk stops early.
// Sandbox-denied entries are silently skipped (dirs skip their whole subtree;
// files are just omitted), matching the filtering policy of FsGlob.
func FsWalk(ctx context.Context, root string, cb Callback) error {
	base := currentWorkingDirectoryFromContext(ctx)
	root = resolvePath(ctx, root)
	if err := checkRead(ctx, root); err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if rerr := checkRead(ctx, path); rerr != nil {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil // sandbox-denied path silently skipped
		}
		// The callback sees project-relative paths matching the root it passed
		// in; the sandbox check above used the absolute path.
		cbPath := path
		if base != "" {
			if rel, rerr := filepath.Rel(base, path); rerr == nil {
				cbPath = rel
			}
		}
		ret, callErr := cb.Call(ctx, cbPath, d.IsDir())
		if callErr != nil {
			return callErr
		}
		if callbackTruthy(ret) {
			return filepath.SkipAll
		}
		return nil
	})
}

// FsAppendFile appends content to path (creating the file if absent, mode 0644),
// subject to the sandbox write policy.
func FsAppendFile(ctx context.Context, path, content string) error {
	if types.Tracing(ctx) {
		return nil
	}
	path = resolvePath(ctx, path)
	if err := checkWrite(ctx, path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("fs.append_file %q: %w", path, err)
	}
	_, werr := f.WriteString(content)
	if cerr := f.Close(); cerr != nil && werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("fs.append_file %q: %w", path, werr)
	}
	return nil
}

// FsMode parses a permission with image.ParseMode. It lives in fs because chmod,
// mkdirAll and stat speak the same integer bits; the parser is libs/image's because a Go
// caller assembling an image needs it without std.
func FsMode(_ context.Context, notation string) (int, error) {
	m, err := image.ParseMode(notation)
	if err != nil {
		return 0, fmt.Errorf("fs.mode: %w", err)
	}
	return int(m), nil
}

// FsChmod changes the permission bits of path, subject to the sandbox write policy.
func FsChmod(ctx context.Context, path string, mode int) error {
	if types.Tracing(ctx) {
		return nil
	}
	path = resolvePath(ctx, path)
	if err := checkWrite(ctx, path); err != nil {
		return err
	}
	if err := os.Chmod(path, os.FileMode(mode)); err != nil {
		return fmt.Errorf("fs.chmod %q: %w", path, err)
	}
	return nil
}

// FsSymlink creates a symbolic link at link pointing to target, subject to the
// sandbox write policy on link and the read policy on target: a link is a way to
// reach its target, so one the policy could not read is refused.
func FsSymlink(ctx context.Context, target, link string) error {
	if types.Tracing(ctx) {
		return nil
	}
	// Only link (the path being created) is resolved against the project dir;
	// target is the link's stored contents, interpreted relative to the link.
	link = resolvePath(ctx, link)
	if err := checkWrite(ctx, link); err != nil {
		return err
	}
	// Concatenated, not joined: Join cleans "..", and the check must resolve the
	// link's directory before it climbs out of it.
	reached := target
	if !filepath.IsAbs(reached) {
		reached = fmt.Sprintf(`%s%s%s`, filepath.Dir(link), string(filepath.Separator), reached)
	}
	if err := checkRead(ctx, reached); err != nil {
		return err
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("fs.symlink %q -> %q: %w", link, target, err)
	}
	return nil
}

// FsReadlink returns the destination of the symbolic link at path, subject to
// the sandbox read policy.
func FsReadlink(ctx context.Context, path string) (string, error) {
	path = resolvePath(ctx, path)
	if err := checkRead(ctx, path); err != nil {
		return "", err
	}
	dst, err := os.Readlink(path)
	if err != nil {
		return "", fmt.Errorf("fs.readlink %q: %w", path, err)
	}
	return dst, nil
}

// FsReadLines reads path and returns its lines with terminators stripped,
// subject to the sandbox read policy. A single trailing newline is not reported
// as a final empty line (the common "lines of a text file" expectation), and an
// empty file yields an empty list rather than [""].
func FsReadLines(ctx context.Context, path string) ([]string, error) {
	content, err := FsReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	if content == "" {
		return []string{}, nil
	}
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n"), nil
}

// FsWriteLines writes lines to path (mode 0644), each terminated by a newline,
// subject to the sandbox write policy. An empty list writes an empty file, so
// write_lines(p, read_lines(p)) round-trips a newline-terminated file.
func FsWriteLines(ctx context.Context, path string, lines []string) error {
	if types.Tracing(ctx) {
		return nil
	}
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	return FsWriteFile(ctx, path, content)
}

// FsTempDir creates a new temporary directory with an optional name prefix and returns
// its path. Under a policy it is made in the policy's private temp dir: the shared one
// is granted to none of the run's children, so a directory there is one they could
// neither read nor write.
func FsTempDir(ctx context.Context, prefix string) (string, error) {
	base := sandbox.PolicyFromContext(ctx).TempBase()
	if types.Tracing(ctx) {
		// Dry run: return a plausible path without creating it. Writes into it are
		// themselves recorded (skipped), so the directory never needs to exist.
		return filepath.Join(cmp.Or(base, os.TempDir()), prefix+"magus-dry-run"), nil
	}
	dir, err := os.MkdirTemp(base, prefix)
	if err != nil {
		return "", fmt.Errorf("fs.temp_dir: %w", err)
	}
	return dir, nil
}

// FsScratchCheckoutDir is xdg.ScratchCheckoutDir subject to the sandbox write policy,
// which judges the purpose's parent directory before anything is created.
func FsScratchCheckoutDir(ctx context.Context, purpose string) (string, error) {
	parent, err := xdg.ScratchCheckoutParent(purpose)
	if err != nil {
		return "", fmt.Errorf("fs.scratch_checkout_dir: %w", err)
	}
	if types.Tracing(ctx) {
		return filepath.Join(parent, "magus-dry-run"), nil
	}
	if err := checkWrite(ctx, parent); err != nil {
		return "", err
	}
	dir, err := xdg.ScratchCheckoutDir(purpose)
	if err != nil {
		return "", fmt.Errorf("fs.scratch_checkout_dir: %w", err)
	}
	return dir, nil
}

// relToCurrentWorkingDirectory renders absolute watch paths relative to base when possible, so the
// callback sees the same project-relative paths the rest of fs.* works with.
func relToCurrentWorkingDirectory(base string, abs []string) []string {
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
// matching the host predicate convention (nil/false is false; a bool is its
// value; any other value is true).
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

// FsDiskUsage reads the filesystem holding path, subject to the sandbox read policy.
func FsDiskUsage(ctx context.Context, path string) (types.DiskUsage, error) {
	path = resolvePath(ctx, path)
	if err := checkRead(ctx, path); err != nil {
		return types.DiskUsage{}, err
	}
	total, free, err := statDisk(path)
	if err != nil {
		return types.DiskUsage{}, fmt.Errorf("fs.disk_usage: %w", err)
	}
	return types.DiskUsage{Path: path, TotalBytes: total, FreeBytes: free}, nil
}

// FsStateDir is $XDG_STATE_HOME/magus/<purpose>, created 0700, subject to the sandbox
// write policy.
func FsStateDir(ctx context.Context, purpose string) (string, error) {
	if purpose == "" || purpose != filepath.Base(purpose) || purpose == "." || purpose == ".." {
		return "", fmt.Errorf("fs.state_dir: purpose %q must be one path element", purpose)
	}
	base, err := xdg.StateDir()
	if err != nil {
		return "", fmt.Errorf("fs.state_dir: %w", err)
	}
	dir := filepath.Join(base, "magus", purpose)
	if err := checkWrite(ctx, dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("fs.state_dir: %w", err)
	}
	return dir, nil
}

// FsTrimOldest is TrimOldest subject to the sandbox write policy.
func FsTrimOldest(ctx context.Context, dir string, maxBytes int, timeoutMs int) (types.TrimReport, error) {
	dir = resolvePath(ctx, dir)
	if err := checkWrite(ctx, dir); err != nil {
		return types.TrimReport{}, err
	}
	if timeoutMs < 0 {
		return types.TrimReport{}, fmt.Errorf("fs.trim_oldest: timeoutMs is %d; it takes a positive bound, or 0 for none", timeoutMs)
	}
	return TrimOldest(ctx, dir, int64(maxBytes), time.Duration(timeoutMs)*time.Millisecond, time.Now())
}

// trimDay is one day of file age: the files under a trimmed directory, bucketed by how
// many whole days before now they were last modified.
const trimDay = 24 * time.Hour

// TrimOldest removes the least recently modified regular files under dir, whole days of
// age at a time, until what is left fits in maxBytes. Files modified within a day of now
// are never removed, so the directory is never emptied: when they alone pass the budget,
// the report's Stopped says so. It walks dir twice and holds one counter per day of age,
// never a list of files. timeout bounds both walks (zero is none); past it the trim stops
// between files. Directories are left in place, and a file that vanishes mid-walk is the
// directory's owner at work, not an error. A missing dir reports zero.
func TrimOldest(ctx context.Context, dir string, maxBytes int64, timeout time.Duration, now time.Time) (types.TrimReport, error) {
	report := types.TrimReport{Dir: dir}
	if maxBytes < 0 {
		return report, fmt.Errorf("trim %s: maxBytes is %d; it takes 0 or more", dir, maxBytes)
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	byDay := map[int]int64{}
	err := walkFiles(ctx, dir, func(_ string, info fs.FileInfo) error {
		byDay[ageInDays(now, info.ModTime())] += info.Size()
		report.BeforeBytes += info.Size()
		return nil
	})
	if stopped, err := trimStopped(ctx, err); stopped != "" || err != nil {
		report.AfterBytes, report.Stopped = report.BeforeBytes, stopped
		return report, err
	}
	report.AfterBytes = report.BeforeBytes
	if report.BeforeBytes <= maxBytes {
		return report, nil
	}
	cutoff, floor := trimCutoff(byDay, maxBytes)
	if floor {
		report.Stopped = fmt.Sprintf("files modified within a day hold %d bytes, past the %d byte budget, and a trim never removes them", byDay[0], maxBytes)
	}
	report.CutoffMs = float64(now.Add(-time.Duration(cutoff) * trimDay).UnixMilli())
	err = walkFiles(ctx, dir, func(path string, info fs.FileInfo) error {
		if ageInDays(now, info.ModTime()) < cutoff {
			return nil
		}
		if err := os.Remove(path); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		report.RemovedFiles++
		report.RemovedBytes += info.Size()
		report.AfterBytes -= info.Size()
		return nil
	})
	stopped, err := trimStopped(ctx, err)
	if stopped != "" {
		report.Stopped = stopped
	}
	return report, err
}

// trimCutoff is the youngest age in days whose files go: every day younger is kept while
// the running total fits the budget. Day 0 is always kept; floor reports that it alone
// does not fit.
func trimCutoff(byDay map[int]int64, maxBytes int64) (cutoff int, floor bool) {
	floor = byDay[0] > maxBytes
	kept := byDay[0]
	for _, day := range slices.Sorted(maps.Keys(byDay)) {
		if day == 0 {
			continue
		}
		if kept+byDay[day] > maxBytes {
			return day, floor
		}
		kept += byDay[day]
	}
	return 1, floor
}

func ageInDays(now, modified time.Time) int {
	return max(0, int(now.Sub(modified)/trimDay))
}

// trimStopped turns a walk's end into the report's Stopped line: the timeout is a reason
// to stop, not a failure to raise.
func trimStopped(ctx context.Context, err error) (string, error) {
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "stopped at its timeout", nil
	}
	return "", err
}

// walkFiles calls fn with each regular file under dir, checking ctx between entries.
func walkFiles(ctx context.Context, dir string, fn func(path string, info fs.FileInfo) error) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return fn(path, info)
	})
}
