package eval

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// PassAtK is the unbiased pass@k estimator of Chen et al. (2021) for one task: n samples
// drawn, c of them correct, the chance that at least one of k drawn without replacement
// is. It is 1 when fewer than k samples are wrong, and 0 for k > n or c = 0.
func PassAtK(n, c, k int) float64 {
	if k < 1 || k > n || c < 0 || c > n {
		return 0
	}
	if n-c < k {
		return 1
	}
	miss := 1.0
	for i := n - c + 1; i <= n; i++ {
		miss *= 1 - float64(k)/float64(i)
	}
	return 1 - miss
}

// PassedTests is the set of test names `magus buzz -t` reported as ok.
func PassedTests(out string) map[string]bool {
	ok := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if rest, found := strings.CutPrefix(line, `ok    test "`); found {
			ok[strings.TrimSuffix(rest, `"`)] = true
		}
	}
	return ok
}

// AllPass is true when every wanted test is among those that passed.
func AllPass(out string, want []string) bool {
	ok := PassedTests(out)
	for _, name := range want {
		if !ok[name] {
			return false
		}
	}
	return len(want) > 0
}

// Seatbelt is the macOS sandbox profile generated code runs under: no network, and no
// write outside the scratch directory, the temporary directory and magus's bytecode
// cache. magus's own sandbox confines spell subprocesses and needs Linux landlock to
// bind a script, so on this host the profile is the one that holds.
func Seatbelt(scratch, home string) string {
	return fmt.Sprintf(`(version 1)
(allow default)
(deny network*)
(deny file-write*)
(allow file-write* (subpath %q) (subpath "/private/var/folders") (subpath %q) (literal "/dev/null") (literal "/dev/dtracehelper"))
`, scratch, filepath.Join(home, ".cache", "magus", "buzz-bytecode"))
}

// RunTests runs `magus buzz -t` on a file of the tree under the seatbelt profile at
// profile, with a deadline that kills the whole process group, and returns what it
// printed. A run that times out returns that output and timedOut.
func RunTests(ctx context.Context, magus, profile, tree, path string, timeout time.Duration) (out string, timedOut bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command("sandbox-exec", "-f", profile, magus, "buzz", "-t", "-C", tree, path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return "", false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return buf.String(), false, nil
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return buf.String(), true, nil
	}
}

// WriteSeatbelt writes the profile for scratch into dir and returns its path.
func WriteSeatbelt(dir, scratch string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "seatbelt.sb")
	return path, os.WriteFile(path, []byte(Seatbelt(scratch, home)), 0o644)
}
