package main

// In-process benchmarks for the magus startup path. These exercise
// startup(ctx, args) without subprocess overhead so benchstat can compare
// before/after across the optimization roadmap.
//
// Caveat on package-init costs: init() functions in dependent packages
// (e.g. internal/config.init's reflection walk, internal/interp/engine/lua/teal/spell
// .init's JSON unmarshal) fire ONCE per `go test` binary, not per b.N
// iteration. For those, see the spawn-based ground-truth measurement in
// hack/bench_startup.sh — it builds a fresh release binary and times
// real cold starts. The in-process benchmarks below still pick up the
// per-call cost of FindRoot, config decode, daemon-socket lookup, flag
// parse, and (when applicable) magus.Open.
//
// Caveat on singletons: cmd/magus uses sync.Once-backed singletons
// (magusOnce, inspectOnce) so a second loadMagus call short-circuits.
// resetStartupSingletons() restores them between iterations so each call
// measures fresh work.
//
// Capture baseline:
//   go test -run=^$ -bench=^BenchmarkStartup$ -benchmem -benchtime=2s \
//     -count=10 ./cmd/magus > bench.before.txt
//
// Compare after a change:
//   go test -run=^$ -bench=^BenchmarkStartup$ -benchmem -benchtime=2s \
//     -count=10 ./cmd/magus > bench.after.txt
//   benchstat bench.before.txt bench.after.txt

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/config"
)

// resetStartupSingletons restores package-level state mutated by
// startup() so a benchmark loop can measure each iteration as a cold
// start. Not safe for concurrent use; benchmarks calling it MUST be
// serial.
func resetStartupSingletons() {
	magusOnce = sync.Once{}
	magusValue = nil
	magusErr = nil
	magusRootOverride = ""

	inspectOnce = sync.Once{}
	inspectValue = nil
	inspectErr = nil
	inspectRootOverride = ""

	globalCfg = config.Config{}
	global = globalFlags{}
}

// setupBenchWorkspace creates a synthetic workspace in b.TempDir() with
// the minimal markers FindRoot looks for (go.mod, empty magusfile.tl)
// and chdirs into it. Returns the workspace root.
func setupBenchWorkspace(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module benchstartup\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "magusfile.tl"), nil, 0o644); err != nil {
		b.Fatal(err)
	}
	b.Chdir(dir)
	return dir
}

// quarantineBenchEnv suppresses host state that would otherwise leak into
// startup(): a real daemon socket on the developer machine, an inherited
// trace log level that would print the startup table, etc. Restored by
// t.Setenv at bench end.
func quarantineBenchEnv(b *testing.B) {
	b.Helper()
	b.Setenv("MAGUS_DAEMON_SOCKET", "")
	b.Setenv("MAGUS_LOG_LEVEL", "")
	// startup() calls log.Print on errors; silence it during benches.
	prevOut := log.Writer()
	log.SetOutput(io.Discard)
	b.Cleanup(func() { log.SetOutput(prevOut) })
}

func benchStartup(b *testing.B, args []string) {
	b.Helper()
	setupBenchWorkspace(b)
	quarantineBenchEnv(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		resetStartupSingletons()
		b.StartTimer()
		res, _ := startup(context.Background(), args)
		if res.cleanup != nil {
			res.cleanup()
		}
	}
}

// BenchmarkStartupHelp measures the unconditional pre-dispatch path for
// `magus help` inside a workspace. Today this pays for a full magus.Open
// (cache + Teal magusfile parse) before the help switch fires in main();
// the fast-path skip lands in a follow-up PR.
func BenchmarkStartupHelp(b *testing.B) {
	benchStartup(b, []string{"help"})
}

// BenchmarkStartupVersion measures `magus version` startup — same waste
// as help, no workspace state is actually consulted.
func BenchmarkStartupVersion(b *testing.B) {
	benchStartup(b, []string{"version"})
}

// BenchmarkStartupCompletionBash measures `magus completion bash`. Needs
// config (the completion script embeds config-driven hints) but does not
// need a workspace.
func BenchmarkStartupCompletionBash(b *testing.B) {
	benchStartup(b, []string{"completion", "bash"})
}

// BenchmarkStartupLs is the workspace-aware fast case — exercises
// loadMagus + proc server start. Establishes the floor for any
// subcommand that actually needs the workspace open.
func BenchmarkStartupLs(b *testing.B) {
	benchStartup(b, []string{"ls"})
}
