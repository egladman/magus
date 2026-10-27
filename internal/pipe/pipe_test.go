//go:build linux || darwin

package pipe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file/record"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// TestMain keeps PIPETEST_*, which configures the stages this binary re-executes as, past
// the isolation that drops every other variable.
func TestMain(m *testing.M) { testkit.Main(m, "PIPETEST_*", spawn.AncestorsEnvVar) }

// testRoot is the workspace root every stage of these tests records under.
const testRoot = "/ws"

// TestHelperPipeStage is not a test: it is one stage of a shell pipe, re-executed from
// this binary so it runs the same executable a real magus stage would. PIPETEST_*
// configures it: sleep PRE_MS, touch READY, write WRITE_HOLDING and WRITE_BYTES, hold
// HOLD_MS, write WRITE_AFTER. EXIT, when set, is the status it records and exits with.
// FORK_UNTIL, when set, first starts a copy of this stage with its executable, argv and
// stdout, which is what a fork of it looks like until it execs, holding stdout until the
// file FORK_UNTIL names exists; the stage waits for it before exiting.
func TestHelperPipeStage(t *testing.T) {
	if os.Getenv("PIPETEST_HELPER") != "1" {
		t.Skip("subprocess helper; not run directly")
	}
	env := func(k string) string { return os.Getenv("PIPETEST_" + k) }
	ms := func(k string) time.Duration {
		n, _ := strconv.Atoi(env(k))
		return time.Duration(n) * time.Millisecond
	}
	// Checked before FORK_UNTIL, which the copy inherits.
	if release := env("FORK_HOLDS_UNTIL"); release != "" {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if _, err := os.Stat(release); err == nil {
				break
			}
		}
		os.Exit(0)
	}
	awaitFork := func() {}
	if release := env("FORK_UNTIL"); release != "" {
		fork := exec.Command(os.Args[0])
		fork.Args = slices.Clone(os.Args)
		fork.Env = append(os.Environ(), "PIPETEST_FORK_HOLDS_UNTIL="+release)
		fork.Stdout = os.Stdout
		if err := fork.Start(); err != nil {
			t.Fatalf("start the fork: %v", err)
		}
		awaitFork = func() { _ = fork.Wait() }
	}
	time.Sleep(ms("PRE_MS"))
	if r := env("READY"); r != "" {
		_ = os.WriteFile(r, []byte("1"), 0o644)
	}
	_, _ = os.Stdout.WriteString(env("WRITE_HOLDING"))
	if n, _ := strconv.Atoi(env("WRITE_BYTES")); n > 0 {
		_, _ = os.Stdout.Write(pattern(n))
	}
	time.Sleep(ms("HOLD_MS"))
	_, _ = os.Stdout.WriteString(env("WRITE_AFTER"))
	awaitFork()
	if e := env("EXIT"); e != "" {
		code, _ := strconv.Atoi(e)
		writeExitRecord(Dir(env("CACHE_DIR"), testRoot), exitRecord{PID: os.Getpid(), Command: "helper", Status: code, Ended: time.Now()})
		os.Exit(code)
	}
	// os.Exit rather than returning: the test framework would print PASS onto the pipe.
	os.Exit(0)
}

func writeRecord(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := record.Write(path, v); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

// pipeStage configures a helper stage; extra args land in its argv, where a classifier
// reads them.
func pipeStage(t *testing.T, cacheDir string, env map[string]string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestHelperPipeStage$"}, args...)...)
	cmd.Env = append(os.Environ(), "PIPETEST_HELPER=1", "PIPETEST_CACHE_DIR="+cacheDir, spawn.AncestorsEnvVar+"=")
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PIPETEST_%s=%s", k, v))
	}
	cmd.Stderr = os.Stderr
	return cmd
}

func startStage(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stage: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
}

// shellPipe is a pipe as a shell hands it to two stages: blocking, close-on-exec.
func shellPipe(t *testing.T) (r, w *os.File) {
	t.Helper()
	var p [2]int
	syscall.ForkLock.RLock()
	err := syscall.Pipe(p[:])
	if err == nil {
		syscall.CloseOnExec(p[0])
		syscall.CloseOnExec(p[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	r, w = os.NewFile(uintptr(p[0]), "stdin"), os.NewFile(uintptr(p[1]), "stdout")
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}

// startUpstream starts a helper stage whose stdout is w, and closes this process's copy of
// w so the stage is the pipe's only writer.
func startUpstream(t *testing.T, cmd *exec.Cmd, w *os.File) {
	t.Helper()
	cmd.Stdout = w
	startStage(t, cmd)
	_ = w.Close()
}

func readyFile(t *testing.T) string { return filepath.Join(t.TempDir(), "ready") }

func waitForFile(t *testing.T, path string, within time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
	}
	t.Fatalf("%s did not appear within %s", path, within)
}

// provedStdio is this test process as a reading stage that proved its upstream at start.
func provedStdio(r *os.File) *Stdio {
	s := &Stdio{Stdin: r}
	s.ProveUpstream(context.Background())
	return s
}

func wantUpstreamFailed(t *testing.T, err error, status int, upstream int) {
	t.Helper()
	var coded interface{ ExitCode() int }
	if !errors.Is(err, types.PipeUpstreamFailed) || !errors.As(err, &coded) || coded.ExitCode() != status {
		t.Fatalf("err = %v, want MGS3030 exiting %d", err, status)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("pid %d (", upstream)) {
		t.Fatalf("err = %q, want it to name upstream pid %d", err, upstream)
	}
}

// WithStdio hands a run the streams StdioFromContext reads back, and a ctx without them has
// none.
func TestStdioRidesTheContext(t *testing.T) {
	if _, ok := StdioFromContext(context.Background()); ok {
		t.Fatalf("StdioFromContext found streams on a bare context")
	}
	r, w := shellPipe(t)
	got, ok := StdioFromContext(WithStdio(context.Background(), Stdio{Stdin: r, Stdout: w}))
	if !ok || got.Stdin != r || got.Stdout != w {
		t.Fatalf("StdioFromContext = %+v, %v; want the streams WithStdio set", got, ok)
	}
}

// ReadRecords takes the records off stdin and leaves every other byte where a target
// reads it, in order, with the end of the stream intact.
func TestReadRecordsRelaysWhatIsNotARecord(t *testing.T) {
	r, w := shellPipe(t)
	go func() {
		_, _ = w.WriteString(`{"schema":5,"type":"run.scope","label":"a","projects":["a"]}` + "\npayload-1\n\n" +
			`{"schema":5,"type":"run.target.result","project":"a","target":"emit","status":"ok","cache_hit":false}` + "\npayload-2")
		_ = w.Close()
	}()
	in, err := ReadRecords(r)
	if err != nil {
		t.Fatalf("ReadRecords: %v", err)
	}
	lines, err := in.All(context.Background())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	var got []string
	for _, l := range lines {
		got = append(got, l.Type)
	}
	if want := []string{report.TypeRunScope, report.TypeTargetResult}; !slices.Equal(got, want) {
		t.Fatalf("records = %q, want %q", got, want)
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read the relayed stdin: %v", err)
	}
	if string(rest) != "payload-1\n\npayload-2" {
		t.Fatalf("relayed stdin = %q, want the upstream's other bytes", rest)
	}
}

// TestReadByMagus proves the reading end of stdout: a process running this executable
// reads it, anything else does not, and a stream that is no pipe proves nothing.
func TestReadByMagus(t *testing.T) {
	ctx := context.Background()

	r, w := shellPipe(t)
	reader := pipeStage(t, t.TempDir(), map[string]string{"HOLD_MS": "3000"})
	reader.Stdin = r
	startStage(t, reader)
	_ = r.Close()
	if !ReadByMagus(ctx, w) {
		t.Errorf("a stage running this executable reads the pipe, but ReadByMagus = false")
	}

	r2, w2 := shellPipe(t)
	other := exec.Command("sleep", "3")
	other.Stdin = r2
	startStage(t, other)
	_ = r2.Close()
	if ReadByMagus(ctx, w2) {
		t.Errorf("sleep reads the pipe, but ReadByMagus = true")
	}

	file, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if ReadByMagus(ctx, file) || ReadByMagus(ctx, nil) {
		t.Errorf("ReadByMagus proved a reader of a file")
	}
}

// TestPipeReaderProofLetsABlockedForkGo: a shell stage that never execs, like a `while
// read` loop, sits blocked on the pipe, and the writer stops waiting for it at once
// rather than at execWait.
func TestPipeReaderProofLetsABlockedForkGo(t *testing.T) {
	r, w := shellPipe(t)
	reader := exec.Command("bash", "-c", "while read -r l; do :; done | cat")
	reader.Stdin = r
	startStage(t, reader)
	_ = r.Close()
	start := time.Now()
	if ReadByMagus(context.Background(), w) {
		t.Errorf("a while-read loop reads the pipe, but ReadByMagus = true")
	}
	if took := time.Since(start); took >= execWait/2 {
		t.Errorf("ReadByMagus took %v to let a blocked fork go, want well under %v", took, execWait)
	}
}

// TestRecordUpstream proves the writing end of stdin: a stage of this executable whose
// argv writes records, and never one whose argv does not or a stage of another tool.
func TestRecordUpstream(t *testing.T) {
	ctx := context.Background()
	writes := func(argv []string) bool { return slices.Contains(argv, "records") }

	r, w := shellPipe(t)
	up := pipeStage(t, t.TempDir(), map[string]string{"HOLD_MS": "3000"}, "records")
	startUpstream(t, up, w)
	pid, ok := RecordUpstream(ctx, r, writes)
	if !ok || pid != up.Process.Pid {
		t.Errorf("RecordUpstream = %d, %v; want the record stage %d", pid, ok, up.Process.Pid)
	}

	r2, w2 := shellPipe(t)
	startUpstream(t, pipeStage(t, t.TempDir(), map[string]string{"HOLD_MS": "3000"}), w2)
	if _, ok := RecordUpstream(ctx, r2, writes); ok {
		t.Errorf("proved a stage whose argv writes no records")
	}

	r3, w3 := shellPipe(t)
	startUpstream(t, exec.Command("sleep", "3"), w3)
	if _, ok := RecordUpstream(ctx, r3, func([]string) bool { return true }); ok {
		t.Errorf("proved sleep as a record stage")
	}
}

func TestRelayPreservesBytesAndEOF(t *testing.T) {
	r, w := shellPipe(t)
	sp, err := StartSpool(r, t.TempDir())
	if err != nil {
		t.Fatalf("StartSpool: %v", err)
	}
	_, _ = w.WriteString("ab")
	time.Sleep(50 * time.Millisecond)
	if err := sp.HandOff(r); err != nil {
		t.Fatalf("HandOff: %v", err)
	}
	_, _ = w.WriteString("cd")
	_ = w.Close()
	got, err := io.ReadAll(r)
	if err != nil || string(got) != "abcd" {
		t.Fatalf("relayed %q (%v), want abcd then EOF", got, err)
	}
	sp.Wait()
}

// TestPipeSettleDrainsSoAnUpstreamStillWritingFinishes: this stage never read its stdin,
// and the upstream is blocked on a full pipe. Settling must not wait on it forever.
func TestPipeSettleDrainsSoAnUpstreamStillWritingFinishes(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	up := pipeStage(t, cacheDir, map[string]string{"WRITE_BYTES": strconv.Itoa(4 << 20), "EXIT": "0"})
	startUpstream(t, up, w)
	s := provedStdio(r)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := s.pipeline.settle(ctx, r, Dir(cacheDir, testRoot), notices{out: &out}); err != nil || out.Len() != 0 {
		t.Fatalf("settle = %v, notices %q; want a green pipeline", err, out.String())
	}
}

// TestPipeForkPendingExecIsNoStage is `magus affected generate | magus run consume`,
// where generate's target runs a command. Until it execs, generate's fork runs magus with
// generate's argv and holds generate's stdout, but it is no stage and leaves no exit
// status: counting one reported an upstream of unknown status on a green pipeline.
func TestPipeForkPendingExecIsNoStage(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	ready, forkRelease := readyFile(t), filepath.Join(t.TempDir(), "fork-release")
	// Registered first, so it runs after the stage is killed, if the test stops early.
	t.Cleanup(func() { _ = os.WriteFile(forkRelease, nil, 0o644) })
	up := pipeStage(t, cacheDir, map[string]string{"FORK_UNTIL": forkRelease, "READY": ready, "EXIT": "0"})
	startUpstream(t, up, w)
	waitForFile(t, ready, 5*time.Second)

	s := provedStdio(r)
	if got := s.pipeline.all(); len(got) != 1 || got[0].PID != up.Process.Pid {
		t.Fatalf("proven stages = %+v, want only pid %d", got, up.Process.Pid)
	}
	if err := os.WriteFile(forkRelease, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := s.pipeline.settle(ctx, r, Dir(cacheDir, testRoot), notices{out: &out}); err != nil || out.Len() != 0 {
		t.Fatalf("settle = %v, notices %q; want a green pipeline", err, out.String())
	}
}

// TestPipeNoticesStatusUnknown: the notice is a line of prose, or under -o jsonl a
// run.notice record whose attrs name the stage, never free text on a parsed stream.
func TestPipeNoticesStatusUnknown(t *testing.T) {
	u := Stage{PID: 4242, Argv: []string{"magus", "affected", "generate"}}
	var prose bytes.Buffer
	notices{out: &prose}.statusUnknown(u)
	if want := "magus: pid 4242 (magus affected generate), upstream of this run in a pipe, ended without leaving its exit status, so whether it failed is unknown.\n"; prose.String() != want {
		t.Errorf("prose = %q, want %q", prose.String(), want)
	}

	var records, beside bytes.Buffer
	notices{out: &beside, records: report.NewLineEncoder(&records)}.statusUnknown(u)
	if beside.Len() != 0 {
		t.Errorf("a record notice also wrote prose: %q", beside.String())
	}
	type notice struct {
		Type  string         `json:"type"`
		Level string         `json:"level"`
		Attrs map[string]any `json:"attrs"`
	}
	var rec notice
	if err := json.Unmarshal(records.Bytes(), &rec); err != nil {
		t.Fatalf("record %q: %v", records.String(), err)
	}
	want := notice{
		Type:  report.TypeNotice,
		Level: "warn",
		Attrs: map[string]any{"upstream_pid": float64(4242), "upstream_command": "magus affected generate"},
	}
	require.Equal(t, want, rec)
}

// TestPipeSettleCountsAVanishedUpstreamAsUnknown: a stage killed before it could record its
// status is reported, not waited on, and not counted as a failure.
func TestPipeSettleCountsAVanishedUpstreamAsUnknown(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	up := pipeStage(t, cacheDir, map[string]string{"HOLD_MS": "60000", "EXIT": "0"})
	startUpstream(t, up, w)
	s := provedStdio(r)

	time.AfterFunc(200*time.Millisecond, func() { _ = up.Process.Kill() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := s.pipeline.settle(ctx, r, Dir(cacheDir, testRoot), notices{out: &out}); err != nil {
		t.Fatalf("settle = %v, want nil for an upstream whose status is unknown", err)
	}
	if want := fmt.Sprintf("pid %d (", up.Process.Pid); !strings.Contains(out.String(), want) || !strings.Contains(out.String(), "unknown") {
		t.Fatalf("notices = %q, want the vanished upstream named as unknown", out.String())
	}
}

func TestPipeSettleHonorsContextCancel(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	up := pipeStage(t, cacheDir, map[string]string{"HOLD_MS": "60000", "EXIT": "0"})
	startUpstream(t, up, w)
	s := provedStdio(r)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.pipeline.settle(ctx, r, Dir(cacheDir, testRoot), notices{out: io.Discard})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("settle = %v after %s, want the context's error", err, time.Since(start))
	}
}

func TestPipeSettleWithoutProofIsNil(t *testing.T) {
	r, _ := shellPipe(t)
	if err := (&Stdio{Stdin: r}).SettlePipeline(context.Background(), testRoot, config.Config{}); err != nil {
		t.Fatalf("SettlePipeline without ProveUpstream = %v", err)
	}
	s := provedStdio(r)
	if err := s.pipeline.settle(context.Background(), r, t.TempDir(), notices{out: io.Discard}); err != nil {
		t.Fatalf("settle with no magus upstream = %v", err)
	}
}

// TestPipeAwaitUpstreamRefusesARedUpstream is `magus affected generate | magus affected
// ci --plan`: the plan stage takes no lock, so only the wait keeps it from printing a
// plan behind a failed generate.
func TestPipeAwaitUpstreamRefusesARedUpstream(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	up := pipeStage(t, cacheDir, map[string]string{"PRE_MS": "300", "EXIT": "4"})
	startUpstream(t, up, w)
	s := provedStdio(r)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := s.AwaitUpstream(ctx, testRoot, config.Config{Cache: config.Cache{Dir: cacheDir}})
	wantUpstreamFailed(t, err, 4, up.Process.Pid)
	if !strings.Contains(err.Error(), "printed nothing") {
		t.Fatalf("err = %q, want it to say this stage printed nothing", err)
	}
}

func TestPipeAwaitUpstreamGreenLeavesNothingToSettle(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	up := pipeStage(t, cacheDir, map[string]string{"PRE_MS": "300", "EXIT": "0"})
	startUpstream(t, up, w)
	s := provedStdio(r)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.AwaitUpstream(ctx, testRoot, config.Config{Cache: config.Cache{Dir: cacheDir}}); err != nil {
		t.Fatalf("AwaitUpstream after a green upstream = %v", err)
	}
	if left := s.pipeline.all(); len(left) != 0 {
		t.Fatalf("pipeline still holds %d stage(s) after a green wait", len(left))
	}
}

// TestFailedUpstreamNamesTheFirstRedStage: a stage that already exited non-zero is
// refused with its status, and the one furthest upstream is named.
func TestFailedUpstreamNamesTheFirstRedStage(t *testing.T) {
	dir := t.TempDir()
	proved := time.Now()
	near := Stage{PID: 515151, Argv: []string{"magus", "run", "b"}, Depth: 0, Proved: proved}
	far := Stage{PID: 525252, Argv: []string{"magus", "run", "a"}, Depth: 1, Proved: proved}
	if err := FailedUpstream(dir, []Stage{near, far}); err != nil {
		t.Fatalf("FailedUpstream with no exit records = %v, want nil", err)
	}
	writeExitRecord(dir, exitRecord{PID: near.PID, Status: 3, Ended: proved})
	writeExitRecord(dir, exitRecord{PID: far.PID, Status: 6, Ended: proved})
	wantUpstreamFailed(t, FailedUpstream(dir, []Stage{near, far}), 6, far.PID)
}

func TestPipeExitRecordLifecycle(t *testing.T) {
	dir := t.TempDir()
	proved := time.Now()
	u := Stage{PID: 424242, Proved: proved}

	writeExitRecord(dir, exitRecord{PID: u.PID, Status: 2, Ended: proved.Add(-time.Hour)})
	if _, ok := readExitRecord(dir, u); ok {
		t.Fatalf("read a record left before the stage was proved alive: an earlier process with its pid")
	}
	writeExitRecord(dir, exitRecord{PID: u.PID, Status: 2, Ended: proved})
	if rec, ok := readExitRecord(dir, u); !ok || rec.exitCode() != 2 {
		t.Fatalf("readExitRecord = %+v, %v; want status 2", rec, ok)
	}
	if got := (exitRecord{Status: 130, Signal: "interrupt"}); got.exitCode() != 1 || got.ending() != "was stopped by a signal (interrupt)" {
		t.Fatalf("a signalled stage exits %d, %q; want 1, stopped by a signal", got.exitCode(), got.ending())
	}

	// The writes above stamp a scan. Age it so this write is due to scan again:
	// a due scan removes a dead record past exitRecordKeep.
	past := time.Now().Add(-exitSweepEvery - time.Second)
	if err := os.Chtimes(filepath.Join(dir, sweepName), past, past); err != nil {
		t.Fatalf("age sweep stamp: %v", err)
	}
	stale := filepath.Join(dir, "999999"+exitSuffix)
	writeRecord(t, stale, exitRecord{PID: 999999, Status: 1, Ended: time.Now().Add(-exitRecordKeep - time.Hour)})
	writeExitRecord(dir, exitRecord{PID: 1, Ended: time.Now()})
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("a dead writer's day-old record was not swept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, strconv.Itoa(u.PID)+exitSuffix)); err != nil {
		t.Fatalf("a fresh record was swept: %v", err)
	}

	// A stamp from that scan is fresh, so the next write must not scan. A day-old
	// record planted after the stamp stays until a scan is due again.
	held := filepath.Join(dir, "888888"+exitSuffix)
	writeRecord(t, held, exitRecord{PID: 888888, Status: 1, Ended: time.Now().Add(-exitRecordKeep - time.Hour)})
	writeExitRecord(dir, exitRecord{PID: 2, Ended: time.Now()})
	if _, err := os.Stat(held); err != nil {
		t.Fatalf("a fresh sweep stamp rescanned and removed a stale record: %v", err)
	}
}

// A directory that cannot record the scan must not be walked. Recording
// happens before the walk, so a failed record is not "never scanned": the
// next exit would otherwise pay the full directory scan again.
func TestExitSweepSkipsWhenTheRecordCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	past := time.Now().Add(-exitSweepEvery - time.Second)
	sweep := filepath.Join(dir, sweepName)
	if err := os.WriteFile(sweep, nil, 0o644); err != nil {
		t.Fatalf("write sweep stamp: %v", err)
	}
	if err := os.Chtimes(sweep, past, past); err != nil {
		t.Fatalf("age sweep stamp: %v", err)
	}
	stale := filepath.Join(dir, "999999"+exitSuffix)
	writeRecord(t, stale, exitRecord{PID: 999999, Status: 1, Ended: time.Now().Add(-exitRecordKeep - time.Hour)})
	stampSweep = func(string, time.Time, time.Time) error { return os.ErrPermission }
	t.Cleanup(func() { stampSweep = os.Chtimes })
	writeExitRecord(dir, exitRecord{PID: 1, Ended: time.Now()})
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("an unrecordable sweep was walked and removed a record: %v", err)
	}
}

// startSleeper runs a process to wait on, killed and reaped when the test ends.
func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

func awaitExitAsync(ctx context.Context, pids ...int) <-chan error {
	done := make(chan error, 1)
	go func() { done <- awaitExit(ctx, pids) }()
	return done
}

// TestAwaitExitWakesOnTheExitOfAnyStage: the wait blocks in the kernel on the stages, not
// on a timer, and the first exit among them ends it.
func TestAwaitExitWakesOnTheExitOfAnyStage(t *testing.T) {
	still, ending := startSleeper(t), startSleeper(t)
	done := awaitExitAsync(t.Context(), still.Process.Pid, ending.Process.Pid)
	select {
	case err := <-done:
		t.Fatalf("awaitExit returned (%v) while both stages ran", err)
	case <-time.After(150 * time.Millisecond):
	}
	require.NoError(t, ending.Process.Kill())
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("awaitExit never woke on the exit of one of its stages")
	}
}

func TestAwaitExitCountsAGonePidAsExited(t *testing.T) {
	gone := exec.Command("true")
	require.NoError(t, gone.Run())
	require.NoError(t, awaitExit(t.Context(), []int{gone.Process.Pid}))
}

func TestAwaitExitStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := awaitExitAsync(ctx, startSleeper(t).Process.Pid)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("awaitExit outlived its context")
	}
}
