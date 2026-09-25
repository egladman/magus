package sandbox

import (
	"fmt"
	"os"
)

// RequiredABI is the lowest landlock ABI a required sandbox accepts. Below v3 the
// kernel cannot deny truncation, so a confined child could empty any file its user
// owns, rules or not (https://docs.kernel.org/userspace-api/landlock.html#file-truncation-abi-3).
const RequiredABI = 3

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
