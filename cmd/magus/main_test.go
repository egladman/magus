package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/libs/testkit"
)

// TestMain lets the test binary act as the `magus` command inside testscript
// scripts: `exec magus ...` in a .txtar file runs the real CLI in process (via
// run), so behavior tests exercise the actual command, not a mock. It keeps the one
// variable a helper re-exec of this binary is instructed with.
func TestMain(m *testing.M) {
	// A sandboxed child starts as this binary re-run as its launcher, as main does.
	magus.MaybeLaunchSandbox()
	// A spawned broker is this test binary re-run as `broker`, which testscript.Main
	// does not dispatch, so it would run the whole suite again, detached. Not when this
	// binary is a script's `exec magus`, which is the real CLI.
	if filepath.Base(os.Args[0]) != "magus" {
		_ = os.Setenv(server.BrokerSpawnTrapEnv, os.DevNull)
	}
	testscript.Main(testkit.Isolated(m, bootstrapExecIntoHelperTargetVar), map[string]func(){
		"magus": func() { os.Exit(runCLI()) },
	})
}

// scriptCoverage returns the directory TestScripts makes each script's GOCOVERDIR in,
// or "" when the test binary collects no coverage. At the end of t, after every
// parallel script, it merges them into the shared GOCOVERDIR the coverage report reads.
func scriptCoverage(t *testing.T) string {
	shared := os.Getenv("GOCOVERDIR")
	if shared == "" {
		return ""
	}
	stage := t.TempDir()
	t.Cleanup(func() {
		if err := mergeScriptCoverage(stage, shared); err != nil {
			t.Errorf("coverage: the scripts' coverage is missing from the report: %v", err)
		}
	})
	return stage
}

// mergeScriptCoverage combines the coverage in every directory under stage into shared
// with one `go tool covdata merge`. It writes into shared directly: its counter file
// carries pid 0, which no process has, and its meta-data file holds the same bytes the
// test binary writes under that name, so no file already there is lost.
func mergeScriptCoverage(stage, shared string) error {
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(stage, e.Name()))
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = filepath.Join(os.Getenv("GOROOT"), "bin", "go")
	}
	cmd := exec.Command(goBin, "tool", "covdata", "merge", "-i="+strings.Join(dirs, ","), "-o="+shared)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go tool covdata merge: %w: %s", err, out)
	}
	return nil
}

// TestScripts replays every testdata/script/*.txtar as a black-box CLI behavior
// test: readable command-plus-expected-output scenarios that catch any observable
// change to the CLI. Each script runs in its own temp dir with the server off, so
// tests are hermetic and never touch a real workspace or socket.
func TestScripts(t *testing.T) {
	coverStage := scriptCoverage(t)
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(e *testscript.Env) error {
			// Go's coverage runtime writes meta-data at exit through a temp file it names
			// by the wall clock alone (openMetaFile's os.Create in
			// internal/coverage/cfile/emit.go), so processes exiting in the same
			// microsecond into one GOCOVERDIR collide and print an error a script asserts
			// on. A dir per script exists only to avoid that.
			if coverStage != "" {
				dir := filepath.Join(coverStage, filepath.Base(e.WorkDir))
				if err := os.Mkdir(dir, 0o755); err != nil {
					return err
				}
				e.Setenv("GOCOVERDIR", dir)
			}
			e.Setenv("MAGUS_SERVER_ENABLED", "false")
			// A run starts a broker otherwise, and a script must leave nothing running.
			e.Setenv("MAGUS_BROKER", "off")
			e.Setenv("MAGUS_HINTS_ENABLED", "false")
			// The cache store lives under the user cache dir, which HOME=/no-home cannot hold.
			// Beside $WORK rather than in it, where a script's VCS status would list it.
			e.Setenv("XDG_CACHE_HOME", e.WorkDir+"-cache")
			// The machine's graph build lock lives under the state dir.
			// One per script, so scripts never queue on each other's build.
			e.Setenv("XDG_STATE_HOME", e.WorkDir+"-state")
			// The shipped guard templates, by ABSOLUTE PATH to the real files.
			// A script that copied them into its own archive would be testing a
			// copy, and a copy of the artifact readers download is exactly what
			// dogfood_test.go exists to prevent.
			templates, err := filepath.Abs(filepath.Join("..", "..", "docs", "guides", "integrations", "agents"))
			if err != nil {
				return err
			}
			e.Setenv("__MAGUS_TEMPLATES", templates)
			miseFile, err := filepath.Abs(filepath.Join("..", "..", "mise.toml"))
			if err != nil {
				return err
			}
			e.Setenv("__MAGUS_MISE_CONFIG", miseFile)
			// testscript starts a script at HOME=/no-home, where a mise shim on PATH finds
			// no installed tools and goes to the network for each pin it cannot resolve.
			for _, name := range []string{"MISE_DATA_DIR", "MISE_OFFLINE"} {
				if v := os.Getenv(name); v != "" {
					e.Setenv(name, v)
				}
			}
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"jsonl-records": jsonlRecords,
		},
	})
}

// jsonlRecords is `jsonl-records [type...]`: every line the last exec wrote to stdout
// and to stderr is a report envelope, a JSON object carrying a schema and a type, and
// each named type is among them. A regex cannot say that; `{` at the start of a line is
// not a parse.
func jsonlRecords(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("usage: jsonl-records [type...]")
	}
	seen := map[string]bool{}
	for _, stream := range []string{"stdout", "stderr"} {
		for i, line := range strings.Split(ts.ReadFile(stream), "\n") {
			if line == "" {
				continue
			}
			var head struct {
				Schema *int   `json:"schema"`
				Type   string `json:"type"`
			}
			if err := json.Unmarshal([]byte(line), &head); err != nil || head.Schema == nil || head.Type == "" {
				ts.Fatalf("%s line %d is not a record: %q", stream, i+1, line)
			}
			seen[head.Type] = true
		}
	}
	for _, typ := range args {
		if !seen[typ] {
			ts.Fatalf("no %s record on either stream", typ)
		}
	}
}

// In-process benchmarks for magus.Open itself, separate from the cmd-level
// startup() pipeline benchmarks in startup_bench_test.go.
//
// covering a file, it is run by name (-bench) rather than with the suite, and the methodology
// notes below are the point of the file. Folding it into the tests of whichever source file it
// happens to call would bury them.
//
// Hypothesis B (cross-run magusfile parse-table cache) needs to score the
// residual cost of Open after the Teal→Lua bytecode cache in
// internal/runtime/compile.go is fully warm. BenchmarkStartupLs already
// measures whole-startup cost; the benchmarks here isolate the Open path
// so hypothesis-B work can be evaluated against a cleaner baseline.
// openBenchJSWorkspace creates nProjects JS project directories (package.json
// marker only, no magusfile.tl) so project.Discover auto-detects them as JS
// projects. Used to measure Open on a pure JS workspace where preloadMagusfiles
// is a no-op and all cost is borne by Inspect + cache replay.
func openBenchJSWorkspace(tb testing.TB, nProjects int) string {
	tb.Helper()
	w := testkit.NewWorkspace(tb)
	w.Write("go.mod", "module openbench\n")
	for i := 0; i < nProjects; i++ {
		w.Write(fmt.Sprintf("pkg-%03d/package.json", i), `{"name":"pkg"}`)
	}
	return w.Root()
}

func openBenchWorkspace(tb testing.TB, numTargets int) string {
	tb.Helper()
	w := testkit.NewWorkspace(tb)
	var sb strings.Builder
	for i := 0; i < numTargets; i++ {
		fmt.Fprintf(&sb, "global function t_%d(args: {string}) end\n", i)
	}
	w.Write("svc/magusfile.tl", sb.String())
	w.Write("go.mod", "module openbench\n")
	return w.Root()
}

// BenchmarkMagusOpenCold measures magus.Open on a fresh workspace each
// iteration. Every iteration pays full Teal compile + binding registration.
func BenchmarkMagusOpenCold(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		root := openBenchWorkspace(b, 10)
		b.StartTimer()

		m, err := magus.Open(ctx, root, magus.WithSpawnRule(magus.TierCeiling))
		if err != nil {
			b.Fatal(err)
		}
		_ = m.Close()
	}
}

// BenchmarkMagusOpenWarmGraphCache measures magus.Open on a 50-project pure JS
// workspace where the workspace.cache.json is already warm (disk cache valid).
// On a warm cache, project.Discover replays the graph from disk (no FS walk),
// preloadMagusfiles is a no-op (no .tl files), and registry.Apply resolves
// spell names. This is the cold-process / warm-disk hot path for JS monorepos.
//
// optimization: workspace.cache.json fast path (project.Discover restoreFromCache)
//
//	skips the full directory walk + autoDetect goroutine pool on warm disk.
//	For a 50-project JS workspace, the cache hit collapses Inspect from
//	O(D) WalkDir syscalls to a single ReadFile + N Lstat calls (one per dir
//	in DirMtimes). Combined with preloadMagusfiles being a no-op for
//	pure JS workspaces, total Open cost drops by ~80% vs cold.
//	measured: BenchmarkMagusOpenWarmGraphCache vs BenchmarkMagusOpenCold.
//	trade-off: cache is invalidated on any directory mtime change; over-
//	  invalidates on dir-only touches but never serves stale projects.
//	assumes: spell registry is process-deterministic — restoreFromCache
//	  binds spells by name from DefaultSpellRegistry().
func BenchmarkMagusOpenWarmGraphCache(b *testing.B) {
	ctx := context.Background()
	root := openBenchJSWorkspace(b, 50)

	// Warm the on-disk workspace cache with one full Open.
	warm, err := magus.Open(ctx, root, magus.WithSpawnRule(magus.TierCeiling))
	if err != nil {
		b.Fatal(err)
	}
	_ = warm.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, err := magus.Open(ctx, root, magus.WithSpawnRule(magus.TierCeiling))
		if err != nil {
			b.Fatal(err)
		}
		_ = m.Close()
	}
	b.Logf("target: <5 ms/op (informational; vs ~50 ms cold 50-project walk)")
}

// BenchmarkMagusOpenWarmAOT measures magus.Open after one previous Open
// against the same workspace — the in-process compile cache is fully warm
// so this isolates the per-Open residual that hypothesis B targets.
func BenchmarkMagusOpenWarmAOT(b *testing.B) {
	ctx := context.Background()
	root := openBenchWorkspace(b, 10)

	warm, err := magus.Open(ctx, root, magus.WithSpawnRule(magus.TierCeiling))
	if err != nil {
		b.Fatal(err)
	}
	_ = warm.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, err := magus.Open(ctx, root, magus.WithSpawnRule(magus.TierCeiling))
		if err != nil {
			b.Fatal(err)
		}
		_ = m.Close()
	}
}

// In-process benchmarks for the magus startup path. These exercise
// startup(ctx, args) without subprocess overhead so benchstat can compare
// before/after across the optimization roadmap.
//
// a path, is run by name, and carries methodology a fold would bury.
//
// Caveat on package-init costs: init() functions in dependent packages
// (e.g. internal/config.init's reflection walk, internal/interp/engine/lua/teal/spell
// .init's JSON unmarshal) fire ONCE per `go test` binary, not per b.N
// iteration. For those, see the spawn-based ground-truth measurement in
// hack/bench/time-startup.buzz — it builds a fresh release binary and times
// real cold starts. The in-process benchmarks below still pick up the
// per-call cost of FindRoot, config decode, server-socket lookup, flag
// parse, and (when applicable) magus.Open.
//
// Caveat on singletons: cmd/magus uses sync.Once-backed singletons
// (magusOnce, inspectOnce) so a second loadMagus call short-circuits.
// resetStartupSingletons() restores them between iterations so each call
// measures fresh work.
//
// Capture baseline:
//
//	go test -run=^$ -bench=^BenchmarkStartup$ -benchmem -benchtime=2s \
//	  -count=10 ./cmd/magus > bench.before.txt
//
// Compare after a change:
//
//	go test -run=^$ -bench=^BenchmarkStartup$ -benchmem -benchtime=2s \
//	  -count=10 ./cmd/magus > bench.after.txt
//	benchstat bench.before.txt bench.after.txt
//
// resetStartupSingletons restores package-level state mutated by
// startup() so a benchmark loop can measure each iteration as a cold
// start. Not safe for concurrent use; benchmarks calling it MUST be
// serial.
func resetStartupSingletons() {
	magusOnce = sync.Once{}
	magusLoaded.Store(false)
	magusValue = nil
	magusErr = nil
	magusOpened = openedRoot{}

	inspectOnce = sync.Once{}
	inspectValue = nil
	inspectErr = nil
	inspectOpened = openedRoot{}

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
// startup(): a real server socket on the developer machine, an inherited
// trace log level that would print the startup table, etc. Restored by
// t.Setenv at bench end.
func quarantineBenchEnv(b *testing.B) {
	b.Helper()
	b.Setenv(proc.SocketEnv, "")
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
		_, res, _ := startup(context.Background(), args)
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

// BenchmarkStartupCompletionBash measures `magus self completion bash`, which
// needs no workspace.
func BenchmarkStartupCompletionBash(b *testing.B) {
	benchStartup(b, []string{"self", "completion", "bash"})
}

// BenchmarkStartupLs is the workspace-aware fast case — exercises
// loadMagus + proc server start. Establishes the floor for any
// subcommand that actually needs the workspace open.
func BenchmarkStartupLs(b *testing.B) {
	benchStartup(b, []string{"ls"})
}
