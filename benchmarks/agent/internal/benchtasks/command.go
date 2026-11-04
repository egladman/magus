package benchtasks

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// command runs name in dir and returns its stdout; a failure carries stderr. A nil env
// inherits the process environment.
func command(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s %s (%s): %w", name, strings.Join(args, " "), lastLines(stderr.String(), 20), err)
	}
	return string(out), nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
