//go:build !windows && !wasm

package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Eight half-second jobs. Each records how many jobs are running as it starts: a job's
// marker exists only while it runs, so the figure can miss an overlap but never
// invent one.
const jobserverMakefile = `JOBS := a b c d e f g h
all: $(JOBS)
$(JOBS):
	@touch running/$@; ls running | wc -l | tr -d ' ' >> counts; sleep 0.5; rm running/$@
.PHONY: all $(JOBS)
`

// gnuMakes is every distinct GNU make on PATH. macOS ships 3.81 as make and Homebrew
// adds 4.x as gmake, and the two read different MAKEFLAGS spellings.
func gnuMakes(t *testing.T) []string {
	t.Helper()
	var found, seen []string
	for _, name := range []string{"make", "gmake"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		if slices.Contains(seen, path) {
			continue
		}
		seen = append(seen, path)
		out, err := exec.Command(path, "--version").Output()
		if err == nil && strings.Contains(string(out), "GNU Make") {
			found = append(found, path)
		}
	}
	return found
}

func TestJobserverHoldsGNUMakeToTheGrant(t *testing.T) {
	makes := gnuMakes(t)
	if len(makes) == 0 {
		t.Skip("no GNU make on PATH: nothing here speaks the jobserver protocol to hold to a grant")
	}
	const slots = 3
	for _, mk := range makes {
		t.Run(filepath.Base(mk), func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte(jobserverMakefile), 0o644))
			require.NoError(t, os.Mkdir(filepath.Join(dir, "running"), 0o755))

			j, err := OpenJobserver(slots)
			require.NoError(t, err)
			ctx := WithJobserver(t.Context(), j)

			// No -j: a -jN on make's command line starts a pool of its own.
			res, err := Exec(ctx, mk, []string{"-s"}, ExecOptions{Dir: dir, Capture: true, Quiet: true})
			require.NoError(t, err, "stdout=%s stderr=%s", res.Stdout, res.Stderr)
			require.Equal(t, 0, res.Code, "stderr=%s", res.Stderr)
			assert.NotContains(t, res.Stderr, "jobserver", "make rejected the pool")

			raw, err := os.ReadFile(filepath.Join(dir, "counts"))
			require.NoError(t, err)
			peak := 0
			for _, line := range strings.Fields(string(raw)) {
				n, err := strconv.Atoi(line)
				require.NoError(t, err)
				peak = max(peak, n)
			}
			assert.LessOrEqual(t, peak, slots, "make ran more jobs than the step was granted")
			assert.Greater(t, peak, 1, "make ran serially, so it never joined the pool")
			assert.Equal(t, strings.Repeat("+", slots-1), drainJobserver(t, j), "make kept a token")
		})
	}
}
