// Package upstream benchmarks gopherbuzz against the reference Buzz implementation
// on identical source.
//
// This is deliberately NOT the protocol used by benchmarks/comparison. That suite
// compares embedded VMs in one Go process, where a warm VM can be reused across
// iterations. Upstream Buzz is a Zig binary that this repo can only run as a
// subprocess, so the only measurement available for BOTH engines is whole-process
// wall clock: fork, load, compile, run, exit. That is what this package measures,
// for gopherbuzz too, which is what keeps it apples-to-apples.
//
// The consequence is that every number here carries process startup and
// compilation. The programs in programs/ are sized so the work dominates it; the
// Startup row below reports the floor so a reader can subtract it. Allocations are
// not reported: Go's -benchmem sees only this process's heap, and neither engine
// runs in it.
//
// Each program is written to run UNMODIFIED on both engines and to check its own
// answer, exiting non-zero on a wrong one, so a fast-but-wrong run fails the
// benchmark rather than posting a good time. Dialect notes that cost real time to
// discover are recorded in README.md.
//
// Run:
//
//	magus run buzz-build libs/gopherbuzz   # produces the ./buzz this harness needs
//	go test ./benchmarks/upstream/ -bench . -benchtime 10x
package upstream

import (
	"os"
	"path/filepath"
)

// gopherbuzzBin resolves the standalone gopherbuzz runner: GOPHERBUZZ_BIN if set,
// else the ./buzz that `magus run buzz-build libs/gopherbuzz` writes at the module
// root. Reports ok=false rather than failing so the benchmark can skip on a machine
// that has not built it.
func gopherbuzzBin() (string, bool) {
	path := os.Getenv("GOPHERBUZZ_BIN")
	if path == "" {
		path = filepath.Join("..", "..", "buzz")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	if info, err := os.Stat(abs); err != nil || info.IsDir() {
		return "", false
	}
	return abs, true
}

// upstreamBin resolves the upstream buzz binary and the BUZZ_PATH prefix it needs
// to find its own stdlib, following the same checkout convention as
// gopherbuzz's UpstreamCheckout: GOPHERBUZZ_UPSTREAM_DIR if set, else ~/Repos/buzz.
func upstreamBin() (bin, buzzPath string, ok bool) {
	dir := os.Getenv("GOPHERBUZZ_UPSTREAM_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", false
		}
		dir = filepath.Join(home, "Repos", "buzz")
	}
	// zig-out is both where the binary lands and the prefix upstream appends
	// "lib/buzz" to when resolving `import "buzz:std"`.
	prefix := filepath.Join(dir, "zig-out")
	bin = filepath.Join(prefix, "bin", "buzz")
	if info, err := os.Stat(bin); err != nil || info.IsDir() {
		return "", "", false
	}
	return bin, prefix, true
}
