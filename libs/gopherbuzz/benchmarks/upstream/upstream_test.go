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
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
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
// conformance_test.go: GOPHERBUZZ_UPSTREAM_DIR if set, else ~/Repos/buzz.
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

// engine is one runnable Buzz implementation.
type engine struct {
	name string
	bin  string
	env  []string // extra environment, beyond the process's own
}

// run executes one program and fails the benchmark unless it exits 0. The programs
// check their own answers, so a non-zero exit means the engine computed the wrong
// result and its timing is meaningless.
func (e engine) run(b *testing.B, program string) {
	b.Helper()
	cmd := exec.Command(e.bin, program)
	if len(e.env) > 0 {
		cmd.Env = append(os.Environ(), e.env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.Fatalf("%s %s: %v\n%s", e.name, filepath.Base(program), err, out)
	}
}

// BenchmarkUpstream times every program in programs/ on both engines, producing
// names like BenchmarkUpstream/fib/Gopherbuzz and BenchmarkUpstream/fib/Upstream.
// Compare a single program with `-bench 'Upstream/fib/'`.
//
// Startup is a program that does nothing, reporting the fixed per-run cost both
// engines pay so the others can be read net of it.
func BenchmarkUpstream(b *testing.B) {
	gb, ok := gopherbuzzBin()
	if !ok {
		b.Skip("no gopherbuzz runner: run `magus run buzz-build libs/gopherbuzz`, or set GOPHERBUZZ_BIN")
	}
	up, buzzPath, ok := upstreamBin()
	if !ok {
		b.Skip("no upstream buzz binary: build github.com/buzz-language/buzz into ~/Repos/buzz/zig-out, or set GOPHERBUZZ_UPSTREAM_DIR")
	}

	programs, err := filepath.Glob(filepath.Join("programs", "*.buzz"))
	if err != nil {
		b.Fatalf("glob programs: %v", err)
	}
	if len(programs) == 0 {
		b.Fatal("no programs found in programs/")
	}

	engines := []engine{
		{name: "Gopherbuzz", bin: gb},
		{name: "Upstream", bin: up, env: []string{"BUZZ_PATH=" + buzzPath}},
	}

	for _, program := range programs {
		abs, err := filepath.Abs(program)
		if err != nil {
			b.Fatalf("abs %s: %v", program, err)
		}
		name := filepath.Base(program)
		name = name[:len(name)-len(".buzz")]
		b.Run(name, func(b *testing.B) {
			for _, e := range engines {
				b.Run(e.name, func(b *testing.B) {
					// One process per iteration: that is the unit being compared.
					for i := 0; i < b.N; i++ {
						e.run(b, abs)
					}
				})
			}
		})
	}
}

// TestProgramsAgree runs every program on both engines once and requires each to
// exit 0. It is the gate the benchmark depends on: a program that has drifted out
// of one engine's dialect, or whose expected value is wrong, fails here with a
// readable message instead of inside a benchmark loop.
func TestProgramsAgree(t *testing.T) {
	gb, ok := gopherbuzzBin()
	if !ok {
		t.Skip("no gopherbuzz runner: run `magus run buzz-build libs/gopherbuzz`, or set GOPHERBUZZ_BIN")
	}
	up, buzzPath, ok := upstreamBin()
	if !ok {
		t.Skip("no upstream buzz binary: build github.com/buzz-language/buzz into ~/Repos/buzz/zig-out, or set GOPHERBUZZ_UPSTREAM_DIR")
	}

	programs, err := filepath.Glob(filepath.Join("programs", "*.buzz"))
	if err != nil {
		t.Fatalf("glob programs: %v", err)
	}
	if len(programs) == 0 {
		t.Fatal("no programs found in programs/")
	}

	for _, program := range programs {
		t.Run(filepath.Base(program), func(t *testing.T) {
			for _, e := range []engine{
				{name: "gopherbuzz", bin: gb},
				{name: "upstream", bin: up, env: []string{"BUZZ_PATH=" + buzzPath}},
			} {
				cmd := exec.Command(e.bin, program)
				if len(e.env) > 0 {
					cmd.Env = append(os.Environ(), e.env...)
				}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("%s: %v\n%s", e.name, err, out)
				}
			}
		})
	}
}

// TestMain guards the one assumption the timings rest on: that both binaries are
// for this machine. A cross-built binary would still run under emulation and post
// numbers that mean nothing next to a native one.
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		// Upstream ships no Windows build this harness can locate.
		os.Exit(0)
	}
	os.Exit(m.Run())
}
