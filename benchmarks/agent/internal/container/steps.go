package container

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// magusPath is where copyMagus puts the binary, on every image's PATH.
const magusPath = "/usr/local/bin/magus"

// elfMachine is the ELF machine each container architecture runs.
var elfMachine = map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}

// copyMagus copies the magus binary at bin into the container at magusPath. It
// refuses anything but a linux ELF for the container's architecture: a host build
// copied in would fail only when the agent first ran it, minutes into the trial.
func copyMagus(ctx context.Context, c *instance, bin string) error {
	arch := c.Image.Platform.Architecture
	if err := checkELF(bin, elfMachine[arch]); err != nil {
		return fmt.Errorf("copy magus: a %s container needs the linux/%s build of magus "+
			"(`magus run release-build . -- linux %s`, then pass the magus from its tarball): %w", c.Image.Platform, arch, arch, err)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		return err
	}
	return c.WriteFile(ctx, magusPath, data, 0o755)
}

func checkELF(bin string, want elf.Machine) error {
	f, err := elf.Open(bin)
	if err != nil {
		return fmt.Errorf("%s is not an ELF binary: %w", bin, err)
	}
	defer f.Close()
	if f.Machine != want {
		return fmt.Errorf("%s is built for %s, not %s", bin, f.Machine, want)
	}
	return nil
}

// errPatchFailed is applyPatch's answer when no rung of the ladder applies the patch.
var errPatchFailed = errors.New("container: no rung of the apply ladder applied the patch")

// applyLadder is the reference harness's swebench.harness.constants.GIT_APPLY_CMDS.
var applyLadder = [][]string{
	{"git", "apply", "--verbose"},
	{"git", "apply", "--verbose", "--3way"},
	{"git", "apply", "--verbose", "--reject"},
	{"patch", "--batch", "--forward", "--fuzz=5", "-p1", "-i"},
}

// patchPath is where applyPatch stages a patch, outside the checkout so it never
// appears in a diff.
const patchPath = "/tmp/magus-bench/agent.patch"

// applyPatch applies patch to testbedDir the way the reference harness does, trying each
// rung of its ladder in turn, and writes the harness's markers and every rung's
// output to log, which is the log the grader reads. It returns the rung that applied,
// joined by spaces, or errPatchFailed. An empty patch applies nothing and returns "",
// which is the null control.
func applyPatch(ctx context.Context, c *instance, patch []byte, log io.Writer) (string, error) {
	if err := c.run(ctx, []string{"git", "config", "--global", "--add", "safe.directory", testbedDir}, log); err != nil {
		return "", err
	}
	if len(patch) == 0 {
		return "", nil
	}
	if err := c.WriteFile(ctx, patchPath, patch, 0o644); err != nil {
		return "", err
	}
	for i, rung := range applyLadder {
		// A rejected rung's partial hunks would poison the next one, but the first rung
		// runs on the tree as the image built it: git clean would also take the image's
		// untracked build products.
		if i > 0 {
			if err := c.run(ctx, []string{"git", "checkout", "--", "."}, log); err != nil {
				return "", err
			}
			if err := c.run(ctx, []string{"git", "clean", "-fd"}, log); err != nil {
				return "", err
			}
		}
		argv := append(append([]string{}, rung...), patchPath)
		code, err := c.Exec(ctx, argv, log, log)
		if err != nil {
			return "", err
		}
		if code == 0 {
			fmt.Fprintln(log, ">>>>> Applied Patch")
			return strings.Join(rung, " "), nil
		}
	}
	fmt.Fprintln(log, ">>>>> Patch Apply Failed")
	return "", errPatchFailed
}

// evalResult is what running a task's eval script produced. ExitCode is meaningless when
// TimedOut is set.
type evalResult struct {
	ExitCode int  `json:"eval_exit"`
	TimedOut bool `json:"eval_timed_out"`
}

const evalPath = "/tmp/magus-bench/eval.sh"

// runEval runs a task's eval script with bash in testbedDir, copying its output to log.
// The script runs directly, never through magus run: a cache hit or a narrowed
// target would change what is graded. When timeout passes, the container is killed
// and log gets the harness's timeout marker; the container is then unusable.
func runEval(ctx context.Context, c *instance, script []byte, timeout time.Duration, log io.Writer) (evalResult, error) {
	if timeout <= 0 {
		return evalResult{}, fmt.Errorf("container: eval in %s: set a timeout", c.Name)
	}
	if err := c.WriteFile(ctx, evalPath, script, 0o755); err != nil {
		return evalResult{}, err
	}
	evalCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	code, err := c.Exec(evalCtx, []string{"bash", evalPath}, log, log)
	if err != nil && ctx.Err() == nil && errors.Is(evalCtx.Err(), context.DeadlineExceeded) {
		fmt.Fprintln(log, "\n>>>>> Tests Timed Out")
		return evalResult{TimedOut: true}, c.engine.kill(ctx, c.id)
	}
	if err != nil {
		return evalResult{}, err
	}
	return evalResult{ExitCode: code}, nil
}

// collectDiff is every change to testbedDir's tracked and untracked files, as a patch
// applyPatch takes. Paths the seed excluded are not in it.
func collectDiff(ctx context.Context, c *instance) ([]byte, error) {
	var stderr bytes.Buffer
	if err := c.run(ctx, []string{"git", "add", "--all", "--intent-to-add"}, &stderr); err != nil {
		return nil, fmt.Errorf("%w: %s", err, stderr.Bytes())
	}
	var out bytes.Buffer
	code, err := c.Exec(ctx, []string{"git", "diff"}, &out, &stderr)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("container: git diff exited %d: %s", code, stderr.Bytes())
	}
	return out.Bytes(), nil
}
