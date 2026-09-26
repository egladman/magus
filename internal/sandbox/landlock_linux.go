//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// fsAccessV1 is every filesystem right landlock ABI v1 (Linux 5.13) handles.
const fsAccessV1 uint64 = unix.LANDLOCK_ACCESS_FS_EXECUTE |
	unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
	unix.LANDLOCK_ACCESS_FS_READ_FILE |
	unix.LANDLOCK_ACCESS_FS_READ_DIR |
	unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
	unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
	unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
	unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
	unix.LANDLOCK_ACCESS_FS_MAKE_REG |
	unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
	unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
	unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
	unix.LANDLOCK_ACCESS_FS_MAKE_SYM

// fsAccessReadOnly grants file and directory reads without execve permission.
// This is sufficient for the dynamic linker to mmap(PROT_EXEC) shared libs
// (which needs READ_FILE, not EXECUTE) while preventing a spell from execve-ing
// arbitrary binaries found under read-only system paths (/usr/lib, /nix/store…).
const fsAccessReadOnly uint64 = unix.LANDLOCK_ACCESS_FS_READ_FILE |
	unix.LANDLOCK_ACCESS_FS_READ_DIR

// fsAccessFile are the only rights a non-directory can carry: the kernel's ACCESS_FILE
// (security/landlock/fs.c). Any other right on a file or device makes landlock_add_rule
// fail with EINVAL. It is an allowlist because a list of directory-only rights missed
// REFER, and rw("/dev/null") then failed every sandboxed run on ABI v2 and later.
const fsAccessFile uint64 = unix.LANDLOCK_ACCESS_FS_EXECUTE |
	unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
	unix.LANDLOCK_ACCESS_FS_READ_FILE |
	unix.LANDLOCK_ACCESS_FS_TRUNCATE |
	unix.LANDLOCK_ACCESS_FS_IOCTL_DEV

// accessForPathType drops the rights a non-directory cannot hold. Split out from
// addPathRule so the masking rule is testable without a kernel.
func accessForPathType(access uint64, isDir bool) uint64 {
	if isDir {
		return access
	}
	return access & fsAccessFile
}

// fsAccessWrite is the full write/create/rename surface. Device ioctls ride with
// write: they can change device state, and a read-only grant never allowed that.
// Bits the running ABI does not handle are masked off before use.
const fsAccessWrite uint64 = unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
	unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
	unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
	unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
	unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
	unix.LANDLOCK_ACCESS_FS_MAKE_REG |
	unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
	unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
	unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
	unix.LANDLOCK_ACCESS_FS_MAKE_SYM |
	unix.LANDLOCK_ACCESS_FS_REFER |
	unix.LANDLOCK_ACCESS_FS_TRUNCATE |
	unix.LANDLOCK_ACCESS_FS_IOCTL_DEV

// handledAccessFS is every filesystem right the kernel at abi can deny. Requesting
// a bit beyond the ABI makes landlock_create_ruleset fail with EINVAL. The ABI table
// is https://docs.kernel.org/userspace-api/landlock.html#previous-limitations.
// Network rights (v4) are deliberately absent: network is unconfined, and handling
// them would deny every port no rule names.
func handledAccessFS(abi int) uint64 {
	access := fsAccessV1
	if abi >= 2 {
		access |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		access |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		access |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return access
}

// handledScopes is every IPC scope the kernel at abi can restrict to the domain.
func handledScopes(abi int) uint64 {
	if abi >= 6 {
		return unix.LANDLOCK_SCOPE_SIGNAL | unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET
	}
	return 0
}

// ABI reports the highest landlock ABI version the running kernel supports. It
// changes no process state. The error wraps ErrUnsupported when landlock is absent
// (ENOSYS), disabled at boot (EOPNOTSUPP) or its syscalls are filtered (EPERM from a
// seccomp policy: the version query needs no privilege, so nothing else denies it).
func ABI() (int, error) {
	ret, _, errno := syscall.Syscall(
		unix.SYS_LANDLOCK_CREATE_RULESET,
		0, // NULL attr
		0, // size = 0
		unix.LANDLOCK_CREATE_RULESET_VERSION,
	)
	if errno != 0 {
		if errors.Is(errno, syscall.ENOSYS) || errors.Is(errno, syscall.EOPNOTSUPP) || errors.Is(errno, syscall.EPERM) {
			return 0, fmt.Errorf("%w: landlock_create_ruleset(VERSION): %w", ErrUnsupported, errno)
		}
		return 0, fmt.Errorf("sandbox: landlock_create_ruleset(VERSION): %w", errno)
	}
	return int(ret), nil
}

// buildRuleset compiles rules and the given scopes into a landlock ruleset for abi and
// returns its descriptor, close-on-exec. It changes no process state.
func buildRuleset(rules []filesystem.Rule, abi int, scopes uint64) (int, error) {
	handledFS := handledAccessFS(abi)
	attr := unix.LandlockRulesetAttr{
		Access_fs: handledFS,
		Scoped:    scopes,
	}
	fd, _, errno := syscall.Syscall(
		unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)),
		unsafe.Sizeof(attr),
		0,
	)
	if errno != 0 {
		if errors.Is(errno, syscall.ENOSYS) || errors.Is(errno, syscall.EOPNOTSUPP) {
			return -1, fmt.Errorf("%w: sandbox: landlock_create_ruleset: %w", ErrUnsupported, errno)
		}
		return -1, fmt.Errorf("sandbox: landlock_create_ruleset: %w", errno)
	}
	rulesetFD := int(fd)
	if err := addPathRules(rulesetFD, rules, handledFS); err != nil {
		unix.Close(rulesetFD)
		return -1, err
	}
	return rulesetFD, nil
}

// addPathRules attaches each of rules to the ruleset. A missing path is not an error:
// a Rust toolchain allowlist may name $CARGO_HOME on a host that only builds Go, and an
// unlisted path is denied either way.
//
// A missing writable path a declaration may create (Rule.Create) is made first, as a
// directory: landlock attaches a rule only to a path that exists, so a cache a tool has
// not made yet (buf's under a fresh XDG_CACHE_HOME) would otherwise be denied to the
// very tool that would create it.
func addPathRules(rulesetFD int, rules []filesystem.Rule, handledFS uint64) error {
	for _, r := range rules {
		err := addPathRule(rulesetFD, r, handledFS)
		if errors.Is(err, syscall.ENOENT) && r.Write && r.Create {
			if mkErr := os.MkdirAll(r.Path, 0o700); mkErr == nil {
				err = addPathRule(rulesetFD, r, handledFS)
			}
		}
		if err != nil && !errors.Is(err, syscall.ENOENT) {
			return fmt.Errorf("sandbox: landlock_add_rule %s: %w", r.Path, err)
		}
	}
	return nil
}

// restrictSelf sets no_new_privs, which unprivileged landlock_restrict_self
// requires, and enforces the ruleset on the calling thread alone. The caller must
// have locked the thread and must exec from it, since sibling threads stay
// unconfined (https://docs.kernel.org/userspace-api/landlock.html#inheritance).
func restrictSelf(rulesetFD int) error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("sandbox: prctl(PR_SET_NO_NEW_PRIVS): %w", err)
	}
	if _, _, errno := syscall.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFD), 0, 0); errno != 0 {
		return fmt.Errorf("sandbox: landlock_restrict_self: %w", errno)
	}
	return nil
}

// addPathRule attaches one Rule to the ruleset. handledFS is the ABI's handled set;
// rule rights are masked against it so the kernel is never asked for one it lacks.
func addPathRule(rulesetFD int, r filesystem.Rule, handledFS uint64) error {
	if !r.Read && !r.Write && !r.Exec {
		return nil
	}
	// Rule paths were symlink-resolved when the policy was built. O_NOFOLLOW keeps a
	// link planted at that path since then from granting whatever it points at.
	pathFD, err := unix.Open(r.Path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(pathFD)

	var st unix.Stat_t
	if err := unix.Fstat(pathFD, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return nil
	}

	var access uint64
	if r.Read {
		access |= fsAccessReadOnly
	}
	if r.Exec {
		access |= unix.LANDLOCK_ACCESS_FS_EXECUTE
	}
	if r.Write {
		access |= fsAccessWrite
	}
	// A rule's path may be a file (an allowlist entry pointing at a config or a
	// socket); ask only for rights its type can hold.
	access = accessForPathType(access&handledFS, st.Mode&unix.S_IFMT == unix.S_IFDIR)
	if access == 0 {
		return nil
	}
	pba := unix.LandlockPathBeneathAttr{
		Allowed_access: access,
		Parent_fd:      int32(pathFD),
	}
	if _, _, errno := syscall.Syscall6(
		unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFD),
		uintptr(unix.LANDLOCK_RULE_PATH_BENEATH),
		uintptr(unsafe.Pointer(&pba)),
		0, 0, 0,
	); errno != 0 {
		return errno
	}
	return nil
}
