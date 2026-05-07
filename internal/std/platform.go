package std

import (
	"context"
	"fmt"
	"os"
	goruntime "runtime"
	"strings"
)

//go:generate go run ../../cmd/magus-bindings-gen -module platform -lang lua -out gen/lua/platform.go
//go:generate go run ../../cmd/magus-bindings-gen -module platform -lang buzz -out gen/buzz/platform.go

// HostPlatform returns the Docker/OCI platform triple (GOOS, OCI arch, ARM variant).
// variant is "v6"/"v7"/"v8" for ARM, "" otherwise; arm reads /proc/cpuinfo on Linux.
func HostPlatform() (osName, arch, variant string) {
	osName = goruntime.GOOS
	switch goruntime.GOARCH {
	case "amd64":
		arch = "amd64"
	case "arm64":
		arch, variant = "arm64", "v8"
	case "arm":
		arch = "arm"
		variant = armVariant()
	case "386":
		arch = "386"
	case "ppc64le":
		arch = "ppc64le"
	case "s390x":
		arch = "s390x"
	case "mips64le":
		arch = "mips64le"
	case "riscv64":
		arch = "riscv64"
	default:
		arch = goruntime.GOARCH
	}
	return
}

// armVariant detects the ARM CPU sub-variant from /proc/cpuinfo on Linux.
// Returns "v6", "v7", "v8", or "" if undetermined.
func armVariant() string {
	if goruntime.GOOS != "linux" {
		return ""
	}
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "cpu architecture") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				v := strings.TrimSpace(parts[1])
				switch v {
				case "6":
					return "v6"
				case "7":
					return "v7"
				case "8", "AArch64":
					return "v8"
				default:
					if len(v) > 0 && v[0] >= '1' && v[0] <= '9' {
						return "v" + string(v[0])
					}
				}
			}
		}
	}
	return ""
}

func init() { Register(Platform) }

// Platform is the "platform" host module: it coheres the many OS/architecture
// spellings open-source projects use (aarch64 vs arm64, Darwin vs macOS vs mac)
// onto canonical Go GOOS/GOARCH values, and renders them back out in a chosen
// convention.
//
// Matching is a deterministic, case-insensitive alias table rather than fuzzy
// matching: architecture identifiers are a small closed set where a near-miss
// (arm vs arm64, 386 vs amd64) must never be silently coerced to the wrong
// answer, so every accepted spelling is enumerated.
var Platform = Module{
	Name: "platform",
	Doc:  "Normalize OS/architecture identifiers across naming conventions (aarch64↔arm64, Darwin↔darwin).",
	Methods: []Method{
		{
			Name: "arch",
			Doc:  "Normalize an architecture identifier (x86_64, aarch64, armv7l, …) to canonical Go GOARCH (amd64, arm64, arm). With style, render that result in a convention (go|uname); raises on an unknown style. Returns \"\" when the identifier is unrecognized.",
			Args: []Arg{
				{Name: "name", Type: TypeString},
				{Name: "style", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeString}},
			Impl:    PlatformArch,
		},
		{
			Name: "os",
			Doc:  "Normalize an OS identifier (Darwin, macOS, win, …) to canonical Go GOOS (darwin, windows). With style, render that result in a convention (go|uname); raises on an unknown style. Returns \"\" when the identifier is unrecognized.",
			Args: []Arg{
				{Name: "name", Type: TypeString},
				{Name: "style", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeString}},
			Impl:    PlatformOS,
		},
	},
}

// archCanonical maps a normalized architecture alias to its canonical Go GOARCH.
var archCanonical = map[string]string{
	"amd64": "amd64", "x86_64": "amd64", "x86-64": "amd64", "x64": "amd64",
	"arm64": "arm64", "aarch64": "arm64", "aarch64_be": "arm64", "armv8": "arm64", "armv8b": "arm64", "armv8l": "arm64",
	"386": "386", "i386": "386", "i486": "386", "i586": "386", "i686": "386", "x86": "386",
	"arm": "arm", "armv7": "arm", "armv7l": "arm", "armv6": "arm", "armv6l": "arm", "armhf": "arm", "armel": "arm",
	"ppc64": "ppc64", "ppc64le": "ppc64le", "ppc64el": "ppc64le",
	"riscv64": "riscv64", "riscv": "riscv64",
	"s390x": "s390x",
	"mips":  "mips", "mipsle": "mipsle", "mips64": "mips64", "mips64le": "mips64le",
	"loong64": "loong64", "loongarch64": "loong64",
	"wasm": "wasm",
}

// archUname renders a canonical GOARCH the way `uname -m` would; canonical
// values absent here use the Go form unchanged.
var archUname = map[string]string{
	"amd64": "x86_64", "arm64": "aarch64", "386": "i686", "arm": "armv7l",
}

// osCanonical maps a normalized OS alias to its canonical Go GOOS.
var osCanonical = map[string]string{
	"darwin": "darwin", "macos": "darwin", "mac": "darwin", "osx": "darwin", "macosx": "darwin", "mac os x": "darwin", "apple-darwin": "darwin",
	"linux": "linux", "gnu/linux": "linux",
	"windows": "windows", "win": "windows", "win32": "windows", "win64": "windows", "mingw": "windows", "msys": "windows", "cygwin": "windows",
	"freebsd":   "freebsd",
	"netbsd":    "netbsd",
	"openbsd":   "openbsd",
	"dragonfly": "dragonfly",
	"solaris":   "solaris", "sunos": "solaris",
	"illumos": "illumos",
	"android": "android",
	"ios":     "ios", "iphoneos": "ios",
	"plan9":  "plan9",
	"aix":    "aix",
	"js":     "js",
	"wasip1": "wasip1", "wasi": "wasip1",
}

// osUname renders a canonical GOOS the way `uname -s` would; canonical values
// absent here use the Go form unchanged.
var osUname = map[string]string{
	"darwin": "Darwin", "linux": "Linux", "windows": "Windows",
	"freebsd": "FreeBSD", "netbsd": "NetBSD", "openbsd": "OpenBSD",
}

// normIdent lowercases and trims an identifier for alias lookup.
func normIdent(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// renderPlatform returns canon in the requested style, or an error for an
// unknown style. An empty style means the canonical (go) form.
func renderPlatform(what, canon, style string, uname map[string]string) (string, error) {
	switch style {
	case "", "go":
		return canon, nil
	case "uname":
		if v, ok := uname[canon]; ok {
			return v, nil
		}
		return canon, nil
	default:
		return "", fmt.Errorf("platform.%s: unknown style %q (want go|uname)", what, style)
	}
}

// PlatformArch normalizes an architecture identifier to canonical Go GOARCH and
// renders it in the requested style. Returns "" for an unrecognized identifier.
func PlatformArch(_ context.Context, name, style string) (string, error) {
	canon, ok := archCanonical[normIdent(name)]
	if !ok {
		return "", nil
	}
	return renderPlatform("arch", canon, style, archUname)
}

// PlatformOS normalizes an OS identifier to canonical Go GOOS and renders it in
// the requested style. Returns "" for an unrecognized identifier.
func PlatformOS(_ context.Context, name, style string) (string, error) {
	canon, ok := osCanonical[normIdent(name)]
	if !ok {
		return "", nil
	}
	return renderPlatform("os", canon, style, osUname)
}
