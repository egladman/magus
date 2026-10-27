package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/cmdline/exit"
)

// delegatedEnv marks a guard call one magus handed to the checkout's own ./magus, so that
// one judges instead of handing it on again.
const delegatedEnv = "__MAGUS_DELEGATED_BY"

// hookEvent is the part of a host's event that says where the call lands.
type hookEvent struct {
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Workdir      string `json:"workdir"`
		FilePath     string `json:"file_path"`
		Path         string `json:"path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

// callDir is the directory a host event's call lands in: the file a write names, else the
// shell tool's workdir, else the session's cwd. "" when the input is a bare command or names
// none.
func callDir(input string) string {
	if !strings.HasPrefix(strings.TrimSpace(input), "{") {
		return ""
	}
	var ev hookEvent
	if json.Unmarshal([]byte(input), &ev) != nil {
		return ""
	}
	at := func(p string) string {
		if p == "" || filepath.IsAbs(p) || ev.Cwd == "" {
			return p
		}
		return filepath.Join(ev.Cwd, p)
	}
	for _, p := range []string{ev.ToolInput.FilePath, ev.ToolInput.NotebookPath, ev.ToolInput.Path} {
		if p != "" {
			return filepath.Dir(at(p))
		}
	}
	if ev.ToolInput.Workdir != "" {
		return at(ev.ToolInput.Workdir)
	}
	return ev.Cwd
}

// checkoutBinary is the ./magus of the workspace dir lies in, when it is executable and is
// not the binary at running; "" otherwise. A directory not created yet is judged by the
// nearest one that exists.
func checkoutBinary(dir, running string) string {
	for dir != "" {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		if parent := filepath.Dir(dir); parent != dir {
			dir = parent
			continue
		}
		return ""
	}
	if dir == "" {
		return ""
	}
	root, err := workspace.FindRoot(dir)
	if err != nil {
		return ""
	}
	own := filepath.Join(root, "magus")
	info, err := os.Stat(own)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	if mine, err := os.Stat(running); err == nil && os.SameFile(info, mine) {
		return ""
	}
	return own
}

// delegateToCheckout hands a hook call to the ./magus of the checkout it lands in when that
// is not this binary, which may predate or postdate that tree. false means this binary
// judges, including when the delegate will not start, which is logged.
func delegateToCheckout(ctx context.Context, input string, out, errOut io.Writer) (bool, error) {
	if os.Getenv(delegatedEnv) != "" {
		return false, nil
	}
	running, err := runningBinaryPath()
	if err != nil {
		return false, nil
	}
	own := checkoutBinary(callDir(input), running)
	if own == "" {
		return false, nil
	}
	cmd := exec.CommandContext(ctx, own, os.Args[1:]...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout, cmd.Stderr = out, errOut
	cmd.Env = append(os.Environ(), delegatedEnv+"="+running)
	err = cmd.Run()
	var exited *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exited):
		return true, exit.Reported(exited.ExitCode())
	}
	slog.WarnContext(ctx, "the checkout's own ./magus did not start, so this binary judges the call instead",
		attr.Error(err), attr.Component("magus shell"))
	return false, nil
}
