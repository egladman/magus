package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/egladman/magus/internal/agent"
)

// agentHookCmd implements `magus agent hook --host <host>`. The user-owned
// harness descriptor supplies the host response format; session hook remains
// the host-neutral policy boundary. The adapter only supplies attribution and
// avoids copied shell wrappers that rediscover the workspace and reimplement
// error handling.
//
// A structured deny is a successful hook delivery. session hook returns status
// 2 for the shell-wrapper contract, but these hosts enforce the response body;
// returning that status would make a correctly delivered deny look like a
// hook-program failure.
func agentHookCmd(ctx context.Context, root string, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("agent hook", flag.ContinueOnError)
	bindDisplayFlags(fset)
	host := fset.String("host", "", "harness descriptor receiving the guard response")
	observe := fset.Bool("observe", false, "record the input as a read observation instead of judging it")
	fset.Usage = func() { agentHookUsage(fset.Output()) }
	if err := fset.Parse(args); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus agent hook: takes no positional arguments (the host event arrives on stdin)")
	}
	root = resolveRootOrEmpty(root)
	if root == "" {
		return fmt.Errorf("magus agent hook: no workspace here: run it from inside one or pass --root <path>")
	}
	response, err := agent.HarnessResponseTemplate(root, *host)
	if err != nil {
		return fmt.Errorf("magus agent hook: %w", err)
	}

	hookArgs := []string{
		"--agent-name", *host,
		"-o", "template=" + response,
	}
	if *observe {
		hookArgs = append(hookArgs, "--observe")
	}
	err = hookCmdWithErrorWriter(ctx, in, out, io.Discard, hookArgs)
	return agentHookDeliveryResult(err)
}

func agentHookDeliveryResult(err error) error {
	var silent errSilent
	if errors.As(err, &silent) && silent.exitCode == guardDenyExitCode {
		return nil
	}
	return err
}

func agentHookUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent hook --host <harness-id>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Read one PreToolUse event from stdin and emit the selected harness's deny or advisory reply.")
	fmt.Fprintln(w, "--observe records the input as a read and emits no policy verdict.")
	fmt.Fprintln(w, "Harnesses are user-owned descriptors under harnesses/, .magus/harnesses/, or $XDG_CONFIG_HOME/magus/harnesses.")
}
