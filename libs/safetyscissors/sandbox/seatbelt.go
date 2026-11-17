package sandbox

import (
	"path/filepath"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

// seatbeltBase is the part of every macOS profile no rule spells: deny by default,
// then what any process needs to start and what Linux leaves unconfined.
//
// Metadata reads are open everywhere because landlock does not confine stat either,
// and path walking, getcwd and realpath fail without them. dyld reads /usr/lib/dyld
// and "/" itself before main runs. The network is open to match Linux, where the
// policy handles no network right: denying it on one platform alone would make a run
// pass on Linux and fail on macOS. Signals and process info are held to the sandbox,
// as landlock's signal scope holds them to the domain.
//
// dyld also maps the shared cache from /System/Volumes/Preboot/Cryptexes and
// /private/var/db/dyld, and libSystem's initializers read /System/Library and the
// random and terminal devices; without them a confined process aborts before main.
// /dev/null is read-write, since a shell redirects to it, and /dev/fd is how a shell
// names a process substitution.
//
// The mach services are the ones a toolchain reaches through libSystem: user and
// group lookup, notifications, logging, DNS, certificate trust (Go's crypto/x509 on
// darwin asks trustd), preferences and FSEvents.
const seatbeltBase = `(version 1)
(deny default)
(allow process-fork)
(allow signal (target same-sandbox))
(allow process-info* (target same-sandbox))
(allow sysctl-read)
(allow ipc-posix-sem)
(allow ipc-posix-shm*)
(allow pseudo-tty)
(allow user-preference-read)
(allow network*)
(allow file-read-metadata)
(allow file-read-data (literal "/"))
(allow file-read* (subpath "/usr/lib"))
(allow file-read* (subpath "/System"))
(allow file-read* (subpath "/private/var/db/dyld"))
(allow file-read* file-write* (literal "/dev/null"))
(allow file-read* (literal "/dev/urandom") (literal "/dev/tty"))
(allow file-read* (subpath "/dev/fd"))
(allow mach-lookup
  (global-name "com.apple.system.opendirectoryd.libinfo")
  (global-name "com.apple.system.opendirectoryd.membership")
  (global-name "com.apple.system.notification_center")
  (global-name "com.apple.system.logger")
  (global-name "com.apple.logd")
  (global-name "com.apple.diagnosticd")
  (global-name "com.apple.mDNSResponder")
  (global-name "com.apple.dnssd.service")
  (global-name "com.apple.trustd")
  (global-name "com.apple.trustd.agent")
  (global-name "com.apple.SecurityServer")
  (global-name "com.apple.cfprefsd.daemon")
  (global-name "com.apple.cfprefsd.agent")
  (global-name "com.apple.FSEvents"))
`

// seatbeltProfile renders rules as a sandbox-exec profile (SBPL): seatbeltBase, then
// per rule file-read* for Read, file-write* and file-ioctl for Write (ioctls ride with
// write, as on Linux), and process-exec for Exec, each on the rule's subpath. A subpath
// matches the path itself, so a rule on a file grants that file.
//
// exec is the command sandbox-exec starts, empty for none. Rule paths are resolved
// through their symlinks, so when exec is a link, a rule grants the file it resolves
// to and not the link sandbox-exec is asked to run; the profile then also grants exec
// of the link itself, but only when an exec rule covers its target, so the link reaches
// nothing the rules do not.
//
// It is platform-independent so its output is tested on every host, not only macOS.
func seatbeltProfile(rules []filesystem.Rule, exec string) string {
	var b strings.Builder
	b.WriteString(seatbeltBase)
	for _, r := range rules {
		path := seatbeltString(r.Path)
		if r.Read {
			b.WriteString("(allow file-read* (subpath " + path + "))\n")
		}
		if r.Write {
			b.WriteString("(allow file-write* file-ioctl (subpath " + path + "))\n")
		}
		if r.Exec {
			b.WriteString("(allow process-exec (subpath " + path + "))\n")
		}
	}
	if link, ok := execLink(rules, exec); ok {
		b.WriteString("(allow process-exec (literal ")
		b.WriteString(seatbeltString(link))
		b.WriteString("))\n")
	}
	return b.String()
}

// execLink is exec, cleaned, when it is a symlink whose target an exec rule covers.
// False when exec is empty, resolves to itself or does not resolve: an exec of a
// missing file fails on its own, naming it.
func execLink(rules []filesystem.Rule, exec string) (string, bool) {
	if exec == "" {
		return "", false
	}
	link := filepath.Clean(exec)
	target, err := filepath.EvalSymlinks(link)
	if err != nil || target == link {
		return "", false
	}
	for _, r := range rules {
		if r.Exec && filesystem.Under(target, r.Path) {
			return link, true
		}
	}
	return "", false
}

// seatbeltString quotes s as an SBPL string literal. Backslash and double quote are
// escaped, so a path holding `") (allow default` stays one path.
func seatbeltString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
