package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/termkit"
)

func runBuzzScript(ctx context.Context, root string, runner *interp.ScriptRunner, opts interp.ScriptOptions, inline string, rest, forwarded []string, separated bool) error {
	if inline == "" && len(rest) > 0 {
		if name, managed := embeddedHookName(rest[0]); managed {
			args := slices.Clone(rest[1:])
			if separated {
				args = append(args, forwarded...)
			}
			return runEmbeddedHook(ctx, root, opts, rest[0], name, args)
		}
	}
	return runner.Run(ctx, root, opts, inline, rest, forwarded, separated)
}

// Managed paths select this binary's glue without following source symlinks.
func embeddedHookName(filename string) (string, bool) {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return "", false
	}
	const dir = "/.magus/harness/hooks/"
	path := filepath.ToSlash(abs)
	at := strings.LastIndex(path, dir)
	if at < 0 {
		return "", false
	}
	return path[at+len(dir):], true
}

func runEmbeddedHook(ctx context.Context, root string, opts interp.ScriptOptions, filename, name string, args []string) error {
	code, err := agent.GlueAsset(agent.GlueSource + name)
	if err != nil {
		return fmt.Errorf("hook %s: %w", filename, err)
	}
	helper, err := agent.GlueAsset(agent.GlueSource + "lib/hook.buzz")
	if err != nil {
		return fmt.Errorf("hook helper: %w", err)
	}
	runner := newBuzzRunner(ctx)
	register := runner.Host.RegisterModules
	runner.Host.RegisterModules = func(ctx context.Context, sess *buzz.Session, out io.Writer) {
		register(ctx, sess, out)
		sess.SetModuleDecls("lib/hook", string(helper))
	}
	return runner.RunSource(ctx, root, opts, filename, string(code), args)
}

// hookGlueShaped reports `magus buzz -s` with stdin not a terminal: the shape every
// shipped hook wiring runs.
func hookGlueShaped(sub string, subArgs []string) bool {
	if sub != "buzz" || termkit.StdinIsTerminal() {
		return false
	}
	own, _ := splitForwarded(subArgs)
	return slices.Contains(own, "-s")
}

// splitForwarded splits buzz's own args from the script's, after `--`.
func splitForwarded(subArgs []string) (own, forwarded []string) {
	at := slices.Index(subArgs, "--")
	if at < 0 {
		return subArgs, nil
	}
	return subArgs[:at], subArgs[at+1:]
}

// answerFailedGlue turns the exit of a hook glue that failed with cause into the
// guard's answer to the event it was handed, since a host runs the call of a hook that
// exits anything but 2, and the reason to print. A script that was handed no hook event
// keeps code and has no reason.
func answerFailedGlue(stdin *guard.HookStdin, rootFlag string, subArgs []string, cause error, code int) (int, string) {
	_, forwarded := splitForwarded(subArgs)
	root, _ := workspace.FindRoot(rootFlag) // a root not found names the release as the repair
	answer, reason, isHook := guard.FailedGlue(root, stdin.Event(), slices.Contains(forwarded, "--agent-name"), cause)
	if !isHook {
		return code, ""
	}
	return answer.ExitCode(), reason
}
