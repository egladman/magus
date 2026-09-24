//go:build linux || darwin

package magus

import (
	"bytes"
	"context"
	"crypto/sha256"
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

	"golang.org/x/sys/unix"

	"github.com/egladman/magus/internal/file/record"
	procrun "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
)

// TestHelperPipeStage is not a test: it is one stage of a shell pipe, re-executed from
// this binary so it runs the same executable a real magus stage would. PIPETEST_*
// configures it: sleep PRE_MS, take PROJECTS through takeRunLocks, touch READY, write
// WRITE_HOLDING and WRITE_BYTES while holding, hold HOLD_MS, release, write WRITE_AFTER.
// RESULT, when set, receives "acquired" or the acquisition's error.
func TestHelperPipeStage(t *testing.T) {
	if os.Getenv("PIPETEST_HELPER") != "1" {
		t.Skip("subprocess helper; not run directly")
	}
	env := func(k string) string { return os.Getenv("PIPETEST_" + k) }
	ms := func(k string) time.Duration {
		n, _ := strconv.Atoi(env(k))
		return time.Duration(n) * time.Millisecond
	}
	l := newProjectLocker(env("CACHE_DIR"), testWorkspaceRoot, withStdio(&ProcessStdio{Stdin: os.Stdin, Stdout: os.Stdout}))
	time.Sleep(ms("PRE_MS"))
	release := func() {}
	if p := env("PROJECTS"); p != "" {
		rel, _, err := l.takeRunLocks(context.Background(), strings.Split(p, ","))
		if r := env("RESULT"); r != "" {
			msg := "acquired"
			if err != nil {
				msg = err.Error()
			}
			_ = os.WriteFile(r, []byte(msg), 0o644)
		}
		if err != nil {
			os.Exit(3)
		}
		release = rel
	}
	if r := env("READY"); r != "" {
		_ = os.WriteFile(r, []byte("1"), 0o644)
	}
	_, _ = os.Stdout.WriteString(env("WRITE_HOLDING"))
	if n, _ := strconv.Atoi(env("WRITE_BYTES")); n > 0 {
		_, _ = os.Stdout.Write(pattern(n))
	}
	time.Sleep(ms("HOLD_MS"))
	release()
	_, _ = os.Stdout.WriteString(env("WRITE_AFTER"))
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

// pipeStage configures a helper stage; extra args land in its argv, where a TakesLocks
// classifier reads them.
func pipeStage(t *testing.T, cacheDir string, env map[string]string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestHelperPipeStage$"}, args...)...)
	cmd.Env = append(os.Environ(), "PIPETEST_HELPER=1", "PIPETEST_CACHE_DIR="+cacheDir, procrun.AncestorsEnvVar+"=")
	for k, v := range env {
		cmd.Env = append(cmd.Env, "PIPETEST_"+k+"="+v)
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

// downstream is this test process as the reading stage: its "stdin" is r.
func downstream(cacheDir string, r *os.File, opts ...lockerOption) *projectLocker {
	return newProjectLocker(cacheDir, testWorkspaceRoot,
		append([]lockerOption{withStdio(&ProcessStdio{Stdin: r}), writingTo(io.Discard)}, opts...)...)
}

// upstreamOf starts a helper stage whose stdout is w, and closes this process's copy of
// w so the stage is the pipe's only writer.
func upstreamOf(t *testing.T, cmd *exec.Cmd, w *os.File) {
	t.Helper()
	cmd.Stdout = w
	startStage(t, cmd)
	_ = w.Close()
}

func readyFile(t *testing.T) string { return filepath.Join(t.TempDir(), "ready") }

func TestAcquireDefersToAPredecessorThatHolds(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	ready := readyFile(t)
	up := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "READY": ready, "HOLD_MS": "500", "WRITE_AFTER": "plan"})
	upstreamOf(t, up, w)
	waitForFile(t, ready, 5*time.Second)

	start := time.Now()
	release, sp, err := downstream(cacheDir, r).takeRunLocks(context.Background(), []string{"p"})
	if err != nil {
		t.Fatalf("takeRunLocks: %v", err)
	}
	defer release()
	if sp == nil {
		t.Fatalf("did not wait on the upstream holding p")
	}
	if waited := time.Since(start); waited < 300*time.Millisecond {
		t.Fatalf("took p after %s, before the upstream released it", waited)
	}
	got, _ := io.ReadAll(r)
	sp.wait()
	if string(got) != "plan" {
		t.Fatalf("stdin after the wait = %q, want %q", got, "plan")
	}
}

// TestAcquireDefersBeforeThePredecessorLocks is the order a plain fail-fast lost
// silently: the downstream got to the lock first, blocked reading stdin while holding
// it, and the upstream was refused.
func TestAcquireDefersBeforeThePredecessorLocks(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	result := filepath.Join(t.TempDir(), "result")
	up := pipeStage(t, cacheDir, map[string]string{"PRE_MS": "300", "PROJECTS": "p", "RESULT": result, "HOLD_MS": "300", "WRITE_AFTER": "plan"})
	upstreamOf(t, up, w)

	release, sp, err := downstream(cacheDir, r).takeRunLocks(context.Background(), []string{"p"})
	if err != nil {
		t.Fatalf("takeRunLocks: %v", err)
	}
	defer release()
	if sp == nil {
		t.Fatalf("took p without waiting for the upstream that had not locked yet")
	}
	got, _ := io.ReadAll(r)
	sp.wait()
	if err := up.Wait(); err != nil {
		t.Fatalf("upstream: %v (it must never be refused by the stage reading it)", err)
	}
	if b, _ := os.ReadFile(result); string(b) != "acquired" {
		t.Fatalf("upstream acquisition = %q, want acquired", b)
	}
	if string(got) != "plan" {
		t.Fatalf("stdin = %q, want plan", got)
	}
}

func TestDeferralDrainsSoTheUpstreamNeverBlocks(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	const n = 4 << 20
	up := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "WRITE_BYTES": strconv.Itoa(n)})
	upstreamOf(t, up, w)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	release, sp, err := downstream(cacheDir, r).takeRunLocks(ctx, []string{"p"})
	if err != nil {
		t.Fatalf("takeRunLocks: %v (an upstream blocked on a full pipe never releases)", err)
	}
	defer release()
	got, _ := io.ReadAll(r)
	if sp != nil {
		sp.wait()
	}
	if len(got) != n || sha256.Sum256(got) != sha256.Sum256(pattern(n)) {
		t.Fatalf("relayed %d bytes, want the upstream's %d byte-for-byte", len(got), n)
	}
}

func TestDeferralEndsWhenThePredecessorIsKilled(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	ready := readyFile(t)
	up := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "READY": ready, "WRITE_HOLDING": "partial", "HOLD_MS": "60000"})
	upstreamOf(t, up, w)
	waitForFile(t, ready, 5*time.Second)
	time.AfterFunc(300*time.Millisecond, func() { _ = up.Process.Kill() })

	release, sp, err := downstream(cacheDir, r).takeRunLocks(context.Background(), []string{"p"})
	if err != nil {
		t.Fatalf("takeRunLocks after the upstream was killed: %v", err)
	}
	got, _ := io.ReadAll(r)
	sp.wait()
	if string(got) != "partial" {
		t.Fatalf("stdin = %q, want what the upstream wrote before it died", got)
	}
	release()
	if locks := heldLocks(cacheDir, testWorkspaceRoot); len(locks) != 0 {
		t.Fatalf("heldLocks = %+v, want none left by the killed upstream", locks)
	}
}

func TestDeferralHonorsContextCancel(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	ready := readyFile(t)
	up := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "READY": ready, "HOLD_MS": "60000"})
	upstreamOf(t, up, w)
	waitForFile(t, ready, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, sp, err := downstream(cacheDir, r).takeRunLocks(ctx, []string{"p"})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "gave up waiting") {
		t.Fatalf("takeRunLocks = %v, want the context's error", err)
	}
	if sp != nil {
		t.Fatalf("a cancelled wait left a relay in stdin's place")
	}
	flags, err := unix.FcntlInt(r.Fd(), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_NONBLOCK != 0 {
		t.Fatalf("stdin left non-blocking (flags %#x, %v); a later read would fail with EAGAIN", flags, err)
	}
}

func TestDeferralHoldsNothingWhileWaiting(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	ready := readyFile(t)
	up := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "READY": ready, "HOLD_MS": "800"})
	upstreamOf(t, up, w)
	waitForFile(t, ready, 5*time.Second)

	done := make(chan error, 1)
	go func() {
		rel, sp, err := downstream(cacheDir, r).takeRunLocks(context.Background(), []string{"a", "p"})
		if err == nil {
			rel()
			_, _ = io.ReadAll(r)
			sp.wait()
		}
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	// "a" sorts before "p": a waiter that took locks in order would hold it by now.
	probe := newProjectLocker(cacheDir, testWorkspaceRoot)
	rel, err := probe.acquire(context.Background(), "a")
	if err != nil {
		t.Fatalf("a is held while the downstream waits (%v): hold-and-wait is how a pipe deadlocks", err)
	}
	rel()
	if err := <-done; err != nil {
		t.Fatalf("downstream: %v", err)
	}
}

func TestNoDeferralWithoutStdio(t *testing.T) {
	cacheDir := t.TempDir()
	_, w := shellPipe(t)
	ready := readyFile(t)
	upstreamOf(t, pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "READY": ready, "HOLD_MS": "5000"}), w)
	waitForFile(t, ready, 5*time.Second)

	start := time.Now()
	_, _, err := newProjectLocker(cacheDir, testWorkspaceRoot).takeRunLocks(context.Background(), []string{"p"})
	var c *lockContendedError
	if !errors.As(err, &c) || time.Since(start) > time.Second {
		t.Fatalf("takeRunLocks = %v after %s, want an immediate refusal", err, time.Since(start))
	}
}

func TestNoDeferralForANonMagusWriter(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	writer := exec.Command("sh", "-c", "sleep 5")
	writer.Stdout = w
	startStage(t, writer)
	_ = w.Close()
	ready := readyFile(t)
	holder := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "READY": ready, "HOLD_MS": "5000"})
	startStage(t, holder)
	waitForFile(t, ready, 5*time.Second)

	start := time.Now()
	_, _, err := downstream(cacheDir, r).takeRunLocks(context.Background(), []string{"p"})
	var c *lockContendedError
	if !errors.As(err, &c) || time.Since(start) > time.Second {
		t.Fatalf("takeRunLocks = %v after %s, want an immediate refusal", err, time.Since(start))
	}
	if !strings.Contains(c.Error(), fmt.Sprintf("pid %d", holder.Process.Pid)) || c.ExitCode() != 75 {
		t.Fatalf("refusal %q (exit %d) does not name the holder with exit 75", c.Error(), c.ExitCode())
	}
}

func TestDeferralDecisionLine(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	ready := readyFile(t)
	up := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "READY": ready, "HOLD_MS": "300"})
	upstreamOf(t, up, w)
	waitForFile(t, ready, 5*time.Second)

	var out bytes.Buffer
	release, sp, err := downstream(cacheDir, r, writingTo(&out)).takeRunLocks(context.Background(), []string{"p"})
	if err != nil {
		t.Fatalf("takeRunLocks: %v", err)
	}
	release()
	_, _ = io.ReadAll(r)
	sp.wait()
	want := fmt.Sprintf("magus: waiting for pid %d (", up.Process.Pid)
	if strings.Count(out.String(), want) != 1 || !strings.Contains(out.String(), "TestHelperPipeStage") {
		t.Fatalf("decision output = %q, want exactly one line naming pid %d and its command", out.String(), up.Process.Pid)
	}
}

func TestDeferralDecisionRecordUnderJSONL(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	ready := readyFile(t)
	up := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "READY": ready, "HOLD_MS": "300"})
	upstreamOf(t, up, w)
	waitForFile(t, ready, 5*time.Second)

	var prose, records bytes.Buffer
	rw := report.NewWriter(&records, report.WithBlockOnFull())
	release, sp, err := downstream(cacheDir, r, writingTo(&prose), withReportWriter(rw)).takeRunLocks(context.Background(), []string{"p"})
	if err != nil {
		t.Fatalf("takeRunLocks: %v", err)
	}
	release()
	_, _ = io.ReadAll(r)
	sp.wait()
	if err := rw.Close(); err != nil {
		t.Fatalf("close records: %v", err)
	}
	if prose.Len() != 0 {
		t.Fatalf("prose %q written beside the record stream", prose.String())
	}
	want := fmt.Sprintf(`"type":"lock.pipe_wait","upstream_pid":%d`, up.Process.Pid)
	if !strings.Contains(records.String(), want) {
		t.Fatalf("records = %q, want %s", records.String(), want)
	}
}

func TestRelayPreservesBytesAndEOF(t *testing.T) {
	r, w := shellPipe(t)
	sp, err := startSpool(r, t.TempDir())
	if err != nil {
		t.Fatalf("startSpool: %v", err)
	}
	_, _ = w.WriteString("ab")
	time.Sleep(50 * time.Millisecond)
	if err := sp.handOff(r); err != nil {
		t.Fatalf("handOff: %v", err)
	}
	_, _ = w.WriteString("cd")
	_ = w.Close()
	got, err := io.ReadAll(r)
	if err != nil || string(got) != "abcd" {
		t.Fatalf("relayed %q (%v), want abcd then EOF", got, err)
	}
	sp.wait()
}

func TestPipeWaitRecordIsWrittenAndSwept(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	ready := readyFile(t)
	up := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "READY": ready, "HOLD_MS": "600"})
	upstreamOf(t, up, w)
	waitForFile(t, ready, 5*time.Second)

	done := make(chan struct{})
	go func() {
		defer close(done)
		rel, sp, err := downstream(cacheDir, r).takeRunLocks(context.Background(), []string{"p"})
		if err == nil {
			rel()
			_, _ = io.ReadAll(r)
			sp.wait()
		}
	}()
	time.Sleep(250 * time.Millisecond)
	waits := pipeWaits(cacheDir, testWorkspaceRoot)
	if len(waits) != 1 || waits[0].PID != os.Getpid() || waits[0].UpstreamPID != up.Process.Pid || waits[0].WaitTime.IsZero() {
		t.Fatalf("pipeWaits during the wait = %+v, want this pid waiting on %d", waits, up.Process.Pid)
	}
	<-done
	if waits := pipeWaits(cacheDir, testWorkspaceRoot); len(waits) != 0 {
		t.Fatalf("pipeWaits after = %+v, want none", waits)
	}

	// A waiter that died mid-wait leaves its record; the next reader sweeps it.
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	l := newProjectLocker(cacheDir, testWorkspaceRoot)
	stale := filepath.Join(l.pipeDir(), strconv.Itoa(dead.Process.Pid)+pipeWaitSuffix)
	writeRecord(t, stale, pipeWaitRecord{PID: dead.Process.Pid, Command: "magus run x", UpstreamPID: 1, Started: time.Now()})
	if waits := pipeWaits(cacheDir, testWorkspaceRoot); len(waits) != 0 {
		t.Fatalf("pipeWaits = %+v, want the dead waiter dropped", waits)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("dead waiter's record not swept: %v", err)
	}
}

// TestDisjointUpstreamDoesNotDelay is the streaming case: a producer settled on other
// projects runs alongside, so its records reach the consumer as they are written.
func TestDisjointUpstreamDoesNotDelay(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	ready := readyFile(t)
	up := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "a", "READY": ready, "WRITE_HOLDING": "record-1\n", "HOLD_MS": "5000"})
	upstreamOf(t, up, w)
	waitForFile(t, ready, 5*time.Second)

	start := time.Now()
	release, sp, err := downstream(cacheDir, r).takeRunLocks(context.Background(), []string{"b"})
	if err != nil {
		t.Fatalf("takeRunLocks: %v", err)
	}
	defer release()
	if sp != nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("waited %s on an upstream holding only a", time.Since(start))
	}
	line := make([]byte, len("record-1\n"))
	if _, err := io.ReadFull(r, line); err != nil || string(line) != "record-1\n" {
		t.Fatalf("stream read %q (%v) while the upstream still runs, want record-1", line, err)
	}
}

func TestUnsettledUpstreamDelaysUntilSettled(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	up := pipeStage(t, cacheDir, map[string]string{"PRE_MS": "500", "PROJECTS": "a", "WRITE_HOLDING": "x", "HOLD_MS": "5000"})
	upstreamOf(t, up, w)

	start := time.Now()
	release, sp, err := downstream(cacheDir, r).takeRunLocks(context.Background(), []string{"b"})
	if err != nil {
		t.Fatalf("takeRunLocks: %v", err)
	}
	defer release()
	waited := time.Since(start)
	if sp == nil || waited < 300*time.Millisecond || waited > 3*time.Second {
		t.Fatalf("waited %s, want until the upstream settled on a (~500ms), not until it exited", waited)
	}
	b := make([]byte, 1)
	if _, err := io.ReadFull(r, b); err != nil || string(b) != "x" {
		t.Fatalf("relay read %q (%v), want x", b, err)
	}
}

func TestReadOnlyUpstreamNeverDelays(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	up := pipeStage(t, cacheDir, map[string]string{"HOLD_MS": "5000", "WRITE_HOLDING": "status"}, "readonly")
	upstreamOf(t, up, w)
	time.Sleep(100 * time.Millisecond)

	l := newProjectLocker(cacheDir, testWorkspaceRoot, writingTo(io.Discard), withStdio(&ProcessStdio{
		Stdin:      r,
		TakesLocks: func(argv []string) bool { return !slices.Contains(argv, "readonly") },
	}))
	start := time.Now()
	release, sp, err := l.takeRunLocks(context.Background(), []string{"p"})
	if err != nil {
		t.Fatalf("takeRunLocks: %v", err)
	}
	defer release()
	if sp != nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("waited %s on a producer that takes no locks", time.Since(start))
	}
}

// TestTransitiveUpstreamIsWeighed: in a | b | c the stage two back holds c's project,
// and b between them takes none; c still waits for a.
func TestTransitiveUpstreamIsWeighed(t *testing.T) {
	cacheDir := t.TempDir()
	r1, w1 := shellPipe(t)
	r2, w2 := shellPipe(t)
	ready := readyFile(t)
	a := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "READY": ready, "HOLD_MS": "600"})
	upstreamOf(t, a, w1)
	b := pipeStage(t, cacheDir, map[string]string{"HOLD_MS": "5000"}, "readonly")
	b.Stdin = r1
	upstreamOf(t, b, w2)
	_ = r1.Close()
	waitForFile(t, ready, 5*time.Second)

	l := newProjectLocker(cacheDir, testWorkspaceRoot, writingTo(io.Discard), withStdio(&ProcessStdio{
		Stdin:      r2,
		TakesLocks: func(argv []string) bool { return !slices.Contains(argv, "readonly") },
	}))
	start := time.Now()
	release, sp, err := l.takeRunLocks(context.Background(), []string{"p"})
	if err != nil {
		t.Fatalf("takeRunLocks: %v (a holds p two stages back)", err)
	}
	defer release()
	if sp == nil || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("took p after %s without waiting on a", time.Since(start))
	}
}

// TestPredecessorExcludesAncestors: a parent writes its child's stdin (magus.run's
// stdin option) and waits for it, so the child must not wait on the parent in turn.
func TestPredecessorExcludesAncestors(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	rel, err := newProjectLocker(cacheDir, testWorkspaceRoot).acquire(context.Background(), "p")
	if err != nil {
		t.Fatalf("parent acquire: %v", err)
	}
	defer rel()
	result := filepath.Join(t.TempDir(), "result")
	child := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "RESULT": result})
	child.Stdin = r
	startStage(t, child)
	_ = r.Close()
	defer w.Close()

	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("child is waiting on its own parent")
	}
	if b, _ := os.ReadFile(result); !strings.Contains(string(b), "locked by another magus process") {
		t.Fatalf("child result = %q, want the ordinary refusal", b)
	}
}

func TestPredecessorExcludesAncestryRefs(t *testing.T) {
	cacheDir := t.TempDir()
	r, w := shellPipe(t)
	ready := readyFile(t)
	up := pipeStage(t, cacheDir, map[string]string{"PROJECTS": "p", "READY": ready, "HOLD_MS": "5000"})
	upstreamOf(t, up, w)
	waitForFile(t, ready, 5*time.Second)

	ctx := types.WithInvocationAncestors(context.Background(), []string{strconv.Itoa(up.Process.Pid) + ":inv-outer"})
	start := time.Now()
	_, _, err := downstream(cacheDir, r).takeRunLocks(ctx, []string{"p"})
	var c *lockContendedError
	if !errors.As(err, &c) || time.Since(start) > time.Second {
		t.Fatalf("takeRunLocks = %v after %s, want no wait on a recorded ancestor", err, time.Since(start))
	}
}

func TestPipeCycleIsRefused(t *testing.T) {
	cacheDir := t.TempDir()
	r1, w1 := shellPipe(t)
	r2, w2 := shellPipe(t)
	defer w2.Close()
	h := pipeStage(t, cacheDir, map[string]string{"HOLD_MS": "5000"})
	h.Stdin = r2
	upstreamOf(t, h, w1)
	_ = r2.Close()
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	_, _, err := downstream(cacheDir, r1).takeRunLocks(context.Background(), []string{"p"})
	if !errors.Is(err, types.PipeCycle) || time.Since(start) > time.Second {
		t.Fatalf("takeRunLocks = %v after %s, want MGS3023 at once", err, time.Since(start))
	}
	if locks := heldLocks(cacheDir, testWorkspaceRoot); len(locks) != 0 {
		t.Fatalf("heldLocks = %+v after a refused cycle", locks)
	}
}

func TestStdioOnAFileKeepsTheFailFast(t *testing.T) {
	cacheDir := t.TempDir()
	lockDir := filepath.Join(cacheDir, "locks", workspaceLockKey(testWorkspaceRoot))
	cmd := helperHold(t, cacheDir, "p", 5_000)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForFile(t, filepath.Join(lockDir, "p", "ready"), 3*time.Second)

	f, err := os.Create(filepath.Join(t.TempDir(), "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	start := time.Now()
	_, _, err = downstream(cacheDir, f).takeRunLocks(context.Background(), []string{"p"})
	var c *lockContendedError
	if !errors.As(err, &c) || time.Since(start) > time.Second || c.ExitCode() != 75 {
		t.Fatalf("takeRunLocks = %v after %s, want the fail-fast unchanged", err, time.Since(start))
	}
}

func TestHoldsRecordLifecycle(t *testing.T) {
	cacheDir := t.TempDir()
	_, w := shellPipe(t)
	l := newProjectLocker(cacheDir, testWorkspaceRoot, withStdio(&ProcessStdio{Stdout: w}))
	release, _, err := l.takeRunLocks(context.Background(), []string{"b", "", "a"})
	if err != nil {
		t.Fatalf("takeRunLocks: %v", err)
	}
	got, settled := l.settledProjects(os.Getpid())
	if !settled || !slices.Equal(got, []string{".", "a", "b"}) {
		t.Fatalf("settledProjects = %v, %v; want [. a b] settled", got, settled)
	}
	release()
	if got, settled := l.settledProjects(os.Getpid()); settled || len(got) != 0 {
		t.Fatalf("settledProjects after release = %v, %v; want nothing", got, settled)
	}
	if entries, _ := os.ReadDir(l.pipeDir()); len(entries) != 0 {
		t.Fatalf("pipe dir after release holds %v", entries)
	}

	// A record whose flock nobody holds is a corpse: never trusted, and swept.
	corpse := filepath.Join(l.pipeDir(), "999999-1"+holdsSuffix)
	writeRecord(t, corpse, holdsRecord{PID: 999999, Projects: "a"})
	if _, settled := l.settledProjects(999999); settled {
		t.Fatalf("a corpse's holds record read as settled")
	}
	if _, err := os.Stat(corpse); !os.IsNotExist(err) {
		t.Fatalf("corpse not swept: %v", err)
	}

	// Without a piped stdout nothing downstream can be reading, so nothing is published.
	quiet := newProjectLocker(cacheDir, testWorkspaceRoot)
	rel, _, err := quiet.takeRunLocks(context.Background(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	if _, settled := quiet.settledProjects(os.Getpid()); settled {
		t.Fatalf("published a lock set with no pipe on stdout")
	}
}
