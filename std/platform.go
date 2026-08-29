package std

import (
	"context"
	"fmt"
	"math"
	"os"
	goruntime "runtime"

	"strings"

	"github.com/egladman/magus/internal/sys/mem"
)

//go:generate go run ../cmd/magus-utils bindings -module platform -lang buzz -out ../internal/interp/bindings/gen/platform.go

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
	WASM: true,
	Doc:  "Normalize OS/architecture identifiers across naming conventions (aarch64<->arm64, Darwin<->darwin).",
	Methods: []Method{
		{
			Name: "arch",
			Doc:  "Normalize an architecture identifier (x86_64, aarch64, armv7l, ...) to canonical Go GOARCH (amd64, arm64, arm). With style, render that result in a convention (go|uname); raises on an unknown style. Returns \"\" when the identifier is unrecognized.",
			Args: []Arg{
				{Name: "name", Type: TypeString},
				{Name: "style", Type: TypeString, Optional: true, Enum: "PlatformStyle"},
			},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    PlatformArch,
		},
		{
			Name: "os",
			Doc:  "Normalize an OS identifier (Darwin, macOS, win, ...) to canonical Go GOOS (darwin, windows). With style, render that result in a convention (go|uname); raises on an unknown style. Returns \"\" when the identifier is unrecognized.",
			Args: []Arg{
				{Name: "name", Type: TypeString},
				{Name: "style", Type: TypeString, Optional: true, Enum: "PlatformStyle"},
			},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    PlatformOS,
		},
		{
			Name:    "memory_bytes",
			Doc:     "How much memory this process may commit, in BYTES, or 0 when it cannot be determined (any host other than Linux or macOS). Narrowed by a container's memory ceiling where there is one, the way cpus() honors a CPU quota. Note magus.project targets take memory_mb in MEGABYTES. Size work that scales on memory rather than cores with this: `go test` defaults its package parallelism to the CPU count, which is the wrong axis under -race, where each test binary carries the race detector's shadow memory. Branch on 0 rather than treating it as \"no memory\".",
			Returns: []Ret{{Type: TypeInt}},
			Impl:    PlatformMemory,
		},
		{
			Name:    "cpus",
			Doc:     "How many CPUs this process may use (Go's GOMAXPROCS, which honors a container quota where the OS-visible core count does not). Pair with memory_bytes() when sizing parallel work: the smaller of the two limits is the one that matters.",
			Returns: []Ret{{Type: TypeInt}},
			Impl:    PlatformCPUs,
		},
	},
}

// PlatformMemory returns the memory this process may commit in bytes, or 0 when
// it cannot be determined.
//
// Narrowed by a container's memory ceiling where there is one, for the reason
// PlatformCPUs below reports GOMAXPROCS rather than NumCPU: inside a container the
// machine's figure and the one that actually bounds the work disagree, and a
// magusfile sizing its parallelism wants the second.
//
// Zero is UNKNOWN, not "none". Every caller has to branch on it, which is the
// honest shape: guessing a size here would make a magusfile's parallelism depend
// on a number magus invented.
//
// The boundary carries a Go int, so a machine with more memory than an int can
// hold reports UNKNOWN rather than a truncated figure. That is only reachable on
// a 32-bit host with over 2GB, where a silently wrapped number would size a
// magusfile's parallelism off nonsense - see the deferred 32-bit plan.
func PlatformMemory(ctx context.Context) (int, error) {
	b := mem.UsableBytes(ctx)
	if b <= 0 || b > int64(math.MaxInt) {
		return 0, nil
	}
	return int(b), nil
}

// PlatformCPUs returns GOMAXPROCS rather than NumCPU: inside a container with a
// CPU quota the two disagree, and the quota is what actually bounds the work. Go
// 1.25 derives GOMAXPROCS from the cgroup limit, so this follows the runtime
// instead of second-guessing it.
func PlatformCPUs(_ context.Context) (int, error) { return goruntime.GOMAXPROCS(0), nil }

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
