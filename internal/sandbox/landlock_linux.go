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

// Landlock ABI versions and the access bits they introduce.
// v1 = kernel 5.13, v2 = 5.19 (adds REFER), v3 = 6.2 (adds TRUNCATE).
// We probe the running kernel's ABI and mask fsAccessAll accordingly so
// that landlock_create_ruleset does not fail with EINVAL on older kernels.
const (
	// fsAccessV1 is the complete set of FS access rights introduced in ABI v1.
	fsAccessV1 uint64 = unix.LANDLOCK_ACCESS_FS_EXECUTE |
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

	// fsAccessV2 adds REFER (hard-link across directories) in ABI v2.
	fsAccessV2 = fsAccessV1 | unix.LANDLOCK_ACCESS_FS_REFER

	// fsAccessV3 adds TRUNCATE in ABI v3.
	fsAccessV3 = fsAccessV2 | unix.LANDLOCK_ACCESS_FS_TRUNCATE
)

// fsAccessReadOnly grants file and directory reads without execve permission.
// This is sufficient for the dynamic linker to mmap(PROT_EXEC) shared libs
// (which needs READ_FILE, not EXECUTE) while preventing a spell from execve-ing
// arbitrary binaries found under read-only system paths (/usr/lib, /nix/store…).
const fsAccessReadOnly uint64 = unix.LANDLOCK_ACCESS_FS_READ_FILE |
	unix.LANDLOCK_ACCESS_FS_READ_DIR

// fsAccessWrite is the full write/create/rename surface. REFER and TRUNCATE
// are masked against the probed ABI before use.
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
	unix.LANDLOCK_ACCESS_FS_TRUNCATE

// landlock ABI version flag; queries supported version without creating a ruleset.
const landlockCreateRulesetVersion = 1 // LANDLOCK_CREATE_RULESET_VERSION

// probeABI returns the highest landlock ABI version the running kernel supports.
// Returns 0 and an error if landlock is not available on this kernel.
func probeABI() (int, error) {
	ret, _, errno := syscall.Syscall(
		unix.SYS_LANDLOCK_CREATE_RULESET,
		0, // NULL attr
		0, // size = 0
		landlockCreateRulesetVersion,
	)
	if errno != 0 {
		if errors.Is(errno, syscall.ENOSYS) || errors.Is(errno, syscall.EOPNOTSUPP) {
			return 0, fmt.Errorf("%w: landlock_create_ruleset(VERSION): %w", ErrUnsupported, errno)
		}
		return 0, fmt.Errorf("sandbox: landlock_create_ruleset(VERSION): %w", errno)
	}
	return int(ret), nil
}

// abiAccessFS returns the union of all FS access bits supported at a given
// landlock ABI version. Requesting bits beyond the ABI causes EINVAL.
func abiAccessFS(abi int) uint64 {
	switch {
	case abi >= 3:
		return fsAccessV3
	case abi == 2:
		return fsAccessV2
	default:
		return fsAccessV1
	}
}

// Apply installs the policy as a landlock ruleset on the current process (inherited by all descendants).
// Returns ErrUnsupported when landlock is unavailable (kernel <5.13 or LSM disabled).
// Restriction is permanent and cannot be loosened; must be called before spell code or subprocesses start.
func Apply(p *Policy) error {
	if p == nil {
		return nil
	}

	// PR_SET_NO_NEW_PRIVS is mandatory for unprivileged landlock_restrict_self.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("%w: sandbox: prctl(PR_SET_NO_NEW_PRIVS): %w", ErrUnsupported, err)
	}
	_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) // best-effort

	abi, err := probeABI()
	if err != nil {
		return err
	}
	supportedFS := abiAccessFS(abi)

	attr := unix.LandlockRulesetAttr{
		Access_fs: supportedFS,
	}
	fd, _, errno := syscall.Syscall(
		unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)),
		unsafe.Sizeof(attr),
		0,
	)
	if errno != 0 {
		if errors.Is(errno, syscall.ENOSYS) || errors.Is(errno, syscall.EOPNOTSUPP) {
			return fmt.Errorf("%w: sandbox: landlock_create_ruleset: %w", ErrUnsupported, errno)
		}
		return fmt.Errorf("sandbox: landlock_create_ruleset: %w", errno)
	}
	rulesetFD := int(fd)
	defer unix.Close(rulesetFD)

	for _, r := range p.FS.Rules {
		if err := addPathRule(rulesetFD, r, supportedFS); err != nil {
			// Missing paths on the host are not an error — a Rust toolchain
			// allowlist may include $CARGO_HOME even when the user is
			// running a Go-only project. The kernel only denies what is
			// not listed; a never-seen path is implicitly denied either way.
			if errors.Is(err, syscall.ENOENT) {
				continue
			}
			return fmt.Errorf("sandbox: landlock_add_rule %s: %w", r.Path, err)
		}
	}

	if _, _, errno := syscall.Syscall(
		unix.SYS_LANDLOCK_RESTRICT_SELF,
		uintptr(rulesetFD),
		0,
		0,
	); errno != 0 {
		return fmt.Errorf("sandbox: landlock_restrict_self: %w", errno)
	}
	return nil
}

// addPathRule attaches one Rule to the ruleset by opening the path as
// O_PATH | O_CLOEXEC and calling landlock_add_rule.
// supportedFS is the ABI-probed bitmask; rule access bits are masked against
// it so we never request rights the kernel does not understand.
func addPathRule(rulesetFD int, r filesystem.Rule, supportedFS uint64) error {
	if !r.Read && !r.Write && !r.Exec {
		return nil
	}
	dirFD, err := unix.Open(r.Path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(dirFD)

	var access uint64
	if r.Read {
		access |= fsAccessReadOnly & supportedFS
	}
	if r.Exec {
		access |= unix.LANDLOCK_ACCESS_FS_EXECUTE & supportedFS
	}
	if r.Write {
		access |= fsAccessWrite & supportedFS
	}
	if access == 0 {
		return nil
	}
	pba := unix.LandlockPathBeneathAttr{
		Allowed_access: access,
		Parent_fd:      int32(dirFD),
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

// Supported reports whether a kernel-level sandbox can be installed on this
// host. It does not modify any process state. A true return does not
// guarantee Apply will succeed (the LSM may be present but disabled), but a
// false return guarantees Apply will fail with ErrUnsupported.
func Supported() bool {
	_, err := os.Stat("/sys/kernel/security/landlock")
	return err == nil
}
