//go:build linux

package run

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ptsname returns the slave device path for a pty master.
//
// Linux is the simpler of the two: TIOCGPTN reports the pty NUMBER and the path is
// /dev/pts/<n> by convention. TIOCSPTLCK with a zero value is unlockpt(3); there is
// no grantpt equivalent to call because devpts assigns ownership on open.
func ptsname(ptmx *os.File) (string, error) {
	n, err := unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPTN)
	if err != nil {
		return "", fmt.Errorf("TIOCGPTN: %w", err)
	}
	if err := unix.IoctlSetPointerInt(int(ptmx.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		return "", fmt.Errorf("unlockpt: %w", err)
	}
	return fmt.Sprintf("/dev/pts/%d", n), nil
}
