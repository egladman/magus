//go:build !linux && !darwin && !windows

package env

// platformEnvAllow supplements commonEnvAllow with POSIX-typical variables
// for BSDs, Plan 9, Solaris, and other platforms.
var platformEnvAllow = []string{
	"LOGNAME",
	"SHELL",
	"COLORTERM",
	"PWD",
	"TMPDIR",
}
