//go:build linux || darwin

package pipepeer

import (
	"bufio"
	"errors"
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
)

// TestHelperSleep is not a test: re-executed with PIPEPEER_HELPER set it is a process
// running this same executable, which is what SameExecutable has to recognize.
func TestHelperSleep(t *testing.T) {
	if os.Getenv("PIPEPEER_HELPER") == "" {
		t.Skip("helper process only")
	}
	time.Sleep(5 * time.Second)
	os.Exit(0)
}

// blockingPipe returns a pipe whose ends are plain blocking descriptors, as a shell
// hands them to a pipeline stage. Close-on-exec, so a child only holds what it is given.
func blockingPipe(t *testing.T) (r, w *os.File) {
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
	r, w = os.NewFile(uintptr(p[0]), "r"), os.NewFile(uintptr(p[1]), "w")
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}

func start(t *testing.T, cmd *exec.Cmd) *exec.Cmd {
	t.Helper()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %v: %v", cmd.Args, err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

func helper(t *testing.T, exe string) *exec.Cmd {
	cmd := exec.Command(exe, "-test.run=^TestHelperSleep$")
	cmd.Env = append(os.Environ(), "PIPEPEER_HELPER=1")
	return cmd
}

func readEnd(t *testing.T, r *os.File) Pipe {
	t.Helper()
	p, err := ReadEnd(os.Getpid(), int(r.Fd()))
	if err != nil {
		t.Fatalf("ReadEnd: %v", err)
	}
	return p
}

func TestPipeWritersFindsTheProcessWritingStdin(t *testing.T) {
	r, w := blockingPipe(t)
	cmd := exec.Command("sleep", "5")
	cmd.Stdout = w
	start(t, cmd)
	_ = w.Close()

	got, err := readEnd(t, r).Writers()
	if err != nil {
		t.Fatalf("Writers: %v", err)
	}
	if !slices.Equal(got, []int{cmd.Process.Pid}) {
		t.Fatalf("Writers() = %v, want [%d]", got, cmd.Process.Pid)
	}
}

func TestPipeWritersFindsEveryWriter(t *testing.T) {
	r, w := blockingPipe(t)
	er, ew := blockingPipe(t)
	cmd := exec.Command("sh", "-c", "sleep 5 & echo $! >&2; wait")
	cmd.Stdout, cmd.Stderr = w, ew
	start(t, cmd)
	_ = w.Close()
	_ = ew.Close()
	line, err := bufio.NewReader(er).ReadString('\n')
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("child pid %q: %v", line, err)
	}

	got, err := readEnd(t, r).Writers()
	if err != nil {
		t.Fatalf("Writers: %v", err)
	}
	slices.Sort(got)
	want := []int{cmd.Process.Pid, child}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("Writers() = %v, want the shell and its background child %v", got, want)
	}
}

func TestPipeWritersIgnoresReaders(t *testing.T) {
	r, w := blockingPipe(t)
	writer := exec.Command("sleep", "5")
	writer.Stdout = w
	start(t, writer)
	reader := exec.Command("sleep", "5")
	reader.Stdin = r
	start(t, reader)
	_ = w.Close()

	p := readEnd(t, r)
	got, err := p.Writers()
	if err != nil {
		t.Fatalf("Writers: %v", err)
	}
	if slices.Contains(got, reader.Process.Pid) || !slices.Contains(got, writer.Process.Pid) {
		t.Fatalf("Writers() = %v, want writer %d and never reader %d", got, writer.Process.Pid, reader.Process.Pid)
	}
	if p.WrittenBy(reader.Process.Pid) {
		t.Fatalf("WrittenBy(reader) = true")
	}
}

func TestReadEndRejectsNonPipes(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	sock := os.NewFile(uintptr(fds[0]), "sock")
	defer sock.Close()
	defer syscall.Close(fds[1])

	for name, f := range map[string]*os.File{"file": file, "devnull": devnull, "socket": sock} {
		if _, err := ReadEnd(os.Getpid(), int(f.Fd())); !errors.Is(err, ErrNotPipe) {
			t.Errorf("ReadEnd(%s) error = %v, want ErrNotPipe", name, err)
		}
	}
}

func TestSameExecutable(t *testing.T) {
	if !SameExecutable(os.Getpid()) {
		t.Fatalf("SameExecutable(self) = false")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	same := start(t, helper(t, exe))
	sleeper := start(t, exec.Command("sleep", "5"))

	// A copy is the same bytes at another path: a different file, so not proof.
	copyPath := filepath.Join(t.TempDir(), "copy.test")
	src, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.OpenFile(copyPath, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	_ = src.Close()
	_ = dst.Close()
	copied := start(t, helper(t, copyPath))

	time.Sleep(100 * time.Millisecond)
	if !SameExecutable(same.Process.Pid) {
		t.Errorf("SameExecutable(re-exec of this binary) = false")
	}
	if SameExecutable(sleeper.Process.Pid) {
		t.Errorf("SameExecutable(sleep) = true")
	}
	if SameExecutable(copied.Process.Pid) {
		t.Errorf("SameExecutable(copy at another path) = true")
	}
}

func TestArgsAndParent(t *testing.T) {
	cmd := start(t, exec.Command("sleep", "5"))
	time.Sleep(50 * time.Millisecond)
	args, err := Args(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	if !slices.Equal(args, []string{"sleep", "5"}) {
		t.Fatalf("Args() = %q, want [sleep 5]", args)
	}
	parent, err := Parent(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("Parent: %v", err)
	}
	if parent != os.Getpid() {
		t.Fatalf("Parent() = %d, want %d", parent, os.Getpid())
	}
}

// TestPipeProofSurvivesWriterExitRace scans while writers exit underneath it: a vanished
// process is simply not a writer, never an error.
func TestPipeProofSurvivesWriterExitRace(t *testing.T) {
	for range 100 {
		r, w := blockingPipe(t)
		cmd := exec.Command("true")
		cmd.Stdout = w
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		_ = w.Close()
		p := readEnd(t, r)
		got, err := p.Writers()
		if err != nil {
			t.Fatalf("Writers during exit: %v", err)
		}
		_ = cmd.Wait()
		for _, pid := range got {
			if pid != cmd.Process.Pid {
				t.Fatalf("Writers() = %v names a process that never held the pipe", got)
			}
		}
		if p.WrittenBy(cmd.Process.Pid) {
			t.Fatalf("WrittenBy(exited) = true")
		}
		_ = r.Close()
	}
}
