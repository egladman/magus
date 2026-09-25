//go:build !windows

package sockdir

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Dir prefers $XDG_RUNTIME_DIR/magus/, then the user cache dir's magus/run/, and only
// with neither falls back to $TMPDIR/magus-$UID/. It panics when the directory exists
// but is not private to this user, since a socket there is one another account could
// tamper with.
//
// $TMPDIR is last because the sandbox grants every run write access to it: a confined
// child could unlink a socket there and bind its own in its place, or read the token
// file beside it (see proc.TokenEnv). Neither of the first two is granted.
func Dir() string {
	var bases []string
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		bases = append(bases, filepath.Join(xdg, "magus"))
	}
	if cache, err := os.UserCacheDir(); err == nil {
		bases = append(bases, filepath.Join(cache, "magus", "run"))
	}
	for _, dir := range bases {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			if verr := verifySockDir(dir); verr != nil {
				panic(verr)
			}
			return dir
		}
	}
	// Fail closed: always return the private per-UID (0700) path. Falling back to
	// the shared, world-traversable os.TempDir() would drop the isolation the
	// socket's security depends on; if MkdirAll failed, a later bind errors out
	// safely instead of placing the socket in a shared directory.
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("magus-%d", os.Getuid()))
	_ = os.MkdirAll(dir, 0o700)
	if verr := verifySockDir(dir); verr != nil {
		panic(verr)
	}
	return dir
}

// verifySockDir checks that dir, if it already exists, is safe to place a
// unix-domain socket in: a real directory (not a symlink), owned by the
// current user, and carrying no group/other permission bits.
//
// This exists because os.MkdirAll(dir, 0o700) is a silent no-op when dir
// already exists: it neither chmods nor checks ownership. The fallback path
// is $TMPDIR/magus-$UID, and both the parent (/tmp, world-writable; the
// sticky bit only stops deleting someone else's files, not creating new
// ones) and the name (UIDs are enumerable) are attacker-reachable: another
// local user can pre-create the directory loosely permissioned, or as a
// symlink, before magus ever runs, and have magus bind its server/MCP socket
// inside a directory they control.
//
// The ordinary case (a directory this or an earlier magus version created
// with MkdirAll(dir, 0o700), owned by us) always passes silently: that is
// exactly the shape checked for below, so an upgrade never refuses to start
// over a pre-existing directory of its own making. It also reports nil (not
// an error) when dir does not exist at all, so an unrelated MkdirAll failure
// upstream (e.g. a read-only TMPDIR) keeps failing the way it always did
// (at bind time) rather than becoming a new failure mode here.
func verifySockDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("proc: %s is a symlink, not the socket directory magus expects; remove it and let magus recreate it", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("proc: %s exists and is not a directory; remove it and let magus recreate it", dir)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("proc: %s is group/other accessible (mode %04o); another local user could tamper with the socket - remove it (or chmod 700 it) and let magus recreate it", dir, perm)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("proc: %s is owned by uid %d, not the current user (uid %d); remove it and let magus recreate it", dir, st.Uid, os.Getuid())
	}
	return nil
}
