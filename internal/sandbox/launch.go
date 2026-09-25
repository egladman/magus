package sandbox

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
)

// RequiredABI is the lowest landlock ABI a required sandbox accepts. Below v3 the
// kernel cannot deny truncation, so a confined child could empty any file its user
// owns, rules or not (https://docs.kernel.org/userspace-api/landlock.html#file-truncation-abi-3).
const RequiredABI = 3

// LauncherFiles is how many ExtraFiles a Cmd from Command carries ahead of any a
// caller appends: the ruleset.
const LauncherFiles = 1

// launcherName is argv[0] of a process started by Command. argv[0] is never parsed
// as a magus verb, so the launcher adds nothing a user can type.
const launcherName = "magus-sandbox-launch"

// launchFailedExit is the launcher's exit status when it could not confine itself
// or exec the command: the shell's "found but could not execute".
const launchFailedExit = 126

// MaybeLaunch returns at once unless this process was started by Command. Then it
// confines itself and execs the requested command, never returning: on failure it
// writes the reason to stderr and exits 126 without running the command. Call it
// first in main, before any configuration, logging or connection is set up.
func MaybeLaunch() {
	if len(os.Args) == 0 || os.Args[0] != launcherName {
		return
	}
	err := launch(os.Args[1:])
	fmt.Fprintf(os.Stderr, "magus: confined launch failed: %v\n", err)
	os.Exit(launchFailedExit)
}

// procSelfRules splits rules into those on process pid's own procfs entries, rewritten
// under /proc/self, and the rest. Rule paths are resolved when the policy is built, so
// /proc/self and /dev/fd name the pid of the magus that built it. A ruleset built there
// would grant a child magus's own entries and none of the child's, which do not exist
// yet; the launcher grants those for itself.
func procSelfRules(rules []filesystem.Rule, pid int) (self, rest []filesystem.Rule) {
	own := "/proc/" + strconv.Itoa(pid)
	for _, r := range rules {
		if tail, ok := strings.CutPrefix(r.Path, own); ok && (tail == "" || tail[0] == '/') {
			r.Path = "/proc/self" + tail
			self = append(self, r)
			continue
		}
		rest = append(rest, r)
	}
	return self, rest
}

// encodeRules renders rules for the launcher's argv as "rwx:/path" words joined by
// commas, a dash for an access not granted.
func encodeRules(rules []filesystem.Rule) string {
	words := make([]string, 0, len(rules))
	for _, r := range rules {
		flag := func(on bool, c string) string {
			if on {
				return c
			}
			return "-"
		}
		words = append(words, flag(r.Read, "r")+flag(r.Write, "w")+flag(r.Exec, "x")+":"+r.Path)
	}
	return strings.Join(words, ",")
}

// decodeRules is encodeRules undone.
func decodeRules(s string) ([]filesystem.Rule, error) {
	if s == "" {
		return nil, nil
	}
	var rules []filesystem.Rule
	for _, w := range strings.Split(s, ",") {
		flags, path, ok := strings.Cut(w, ":")
		if !ok || len(flags) != 3 || !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("sandbox: launcher: malformed rule %q", w)
		}
		rules = append(rules, filesystem.Rule{Path: path, Read: flags[0] == 'r', Write: flags[1] == 'w', Exec: flags[2] == 'x'})
	}
	return rules, nil
}

// kernelABI is ABI asked once: the running kernel's answer cannot change.
var kernelABI = sync.OnceValues(ABI)

// KernelConfines reports whether p's children run under the kernel layer, which they
// do wherever the host has landlock. It is false with a nil error for a nil p and for
// a best-effort p on a host without landlock, whose children then run under the
// binding checks and the env allowlist alone. A required p on a host below
// RequiredABI is an MGS2012 error.
func (p *Policy) KernelConfines() (bool, error) {
	if p == nil {
		return false, nil
	}
	abi, err := kernelABI()
	if p.Mode.Resolved() != types.SandboxModeRequired {
		return err == nil && abi >= 1, nil
	}
	switch {
	case err != nil:
		return false, ErrRequired(p.Workspace, "the kernel cannot confine its children: "+err.Error())
	case abi < RequiredABI:
		return false, ErrRequired(p.Workspace, fmt.Sprintf("the kernel's landlock ABI is %d, below the %d it needs", abi, RequiredABI))
	}
	return true, nil
}

// ErrRequired returns the MGS2012 refusal of a required sandbox for the workspace at
// root, why saying what stands in the way. root may be empty.
func ErrRequired(root, why string) error {
	subject := "sandbox mode is required"
	if root != "" {
		subject += " for " + root
	}
	return types.DiagnosticErrorf(types.SandboxRequired,
		"%s, and %s; run it on Linux 6.2 or newer with landlock enabled (/sys/kernel/security/landlock)", subject, why)
}
