//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/sandbox"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// runMagusSub fixes the subcommand instead of accepting it from user arguments.
func runMagusSub(ctx context.Context, sub string, args []string, opts types.MagusCommandOptions) (types.ExecResult, error) {
	return runMagus(ctx, sub, append([]string{sub}, args...), opts)
}

func resolveRunDir(ctx context.Context, opts types.MagusCommandOptions) string {
	dir, _ := CurrentWorkingDirectoryFromContext(ctx)
	sub := opts.Dir
	if sub == "" {
		return dir
	}
	if filepath.IsAbs(sub) || dir == "" {
		return sub
	}
	return filepath.Join(dir, sub)
}

// nestedExecOptions keeps credentials on stdin rather than in argv.
func nestedExecOptions(ctx context.Context, opts types.MagusCommandOptions, env []string) spawn.ExecOptions {
	return spawn.ExecOptions{
		Dir:     resolveRunDir(ctx, opts),
		Env:     env,
		Capture: true,
		Quiet:   opts.Quiet,
		Stdin:   opts.Stdin,
	}
}

func runMagus(ctx context.Context, label string, args []string, opts types.MagusCommandOptions) (types.ExecResult, error) {
	self, err := magusExecutable(ctx, opts)
	if err != nil {
		return types.ExecResult{}, fmt.Errorf("magus.%s: executable: %w", label, err)
	}

	full := nestedArgv(args, opts)

	// childEnv withholds server sockets from subprocesses, while a recursive
	// Magus
	// call must retain both that trusted transport and its captured lease.
	env := recursiveMagusEnv(ctx)

	lim := cache.LimiterFromContext(ctx)
	var rec types.ExecResult
	var cmdErr error
	runFn := func() error {
		res, err := spawn.Exec(nestedMagusContext(ctx), self, full, nestedExecOptions(ctx, opts, env))
		rec, cmdErr = nestedResult(label, full, res, err, opts)
		return nil
	}
	if err := proc.RunChildSync(ctx, lim, runFn); err != nil {
		return types.ExecResult{}, fmt.Errorf("magus.%s: %w", label, err)
	}
	return rec, cmdErr
}

func magusExecutable(ctx context.Context, opts types.MagusCommandOptions) (string, error) {
	if opts.Binary == "" {
		return os.Executable()
	}
	if filepath.IsAbs(opts.Binary) {
		return opts.Binary, nil
	}
	return filepath.Abs(filepath.Join(resolveRunDir(ctx, opts), opts.Binary))
}

// nestedArgv is a nested magus's argv: global flags (--root) precede the
// subcommand and its args.
func nestedArgv(args []string, opts types.MagusCommandOptions) []string {
	var full []string
	if opts.Root != "" {
		full = append(full, "--root", opts.Root)
	}
	return append(full, args...)
}

// nestedResult preserves failed commands' output for callers that read reports.
func nestedResult(label string, full []string, res spawn.ExecResult, err error, opts types.MagusCommandOptions) (types.ExecResult, error) {
	if err != nil && errors.Is(err, types.ExecDenied) {
		return types.ExecResult{}, err
	}
	var rec types.ExecResult
	if res.Started || opts.AllowFailure {
		// Doctor's nonzero exit can still carry the report its caller
		// requested.
		rec = types.ExecResult{
			Stdout: strings.TrimSpace(res.Stdout),
			Stderr: strings.TrimSpace(res.Stderr),
			Code:   res.Code,
			OK:     res.Code == 0,
		}
	}
	if res.Code == 0 || opts.AllowFailure {
		return rec, nil
	}
	if !res.Started {
		// A launch failure has no child exit code.
		return rec, fmt.Errorf("magus.%s: %s: %w", label, strings.Join(full, " "), err)
	}
	// Quiet captures stderr, so the returned error carries it.
	cmdErr := fmt.Errorf("magus.%s: %s exited with code %d", label, strings.Join(full, " "), res.Code)
	if opts.Quiet {
		if msg := strings.TrimSpace(res.Stderr); msg != "" {
			cmdErr = fmt.Errorf("%w: %s", cmdErr, msg)
		}
	}
	return rec, cmdErr
}

// nestedMagusContext confines a nested magus under every target's layer.
// It runs targets, and a landlock domain grants them nothing it lacks.
// The caller's own children keep ctx's policy.
func nestedMagusContext(ctx context.Context) context.Context {
	return sandbox.WithPolicy(ctx, sandbox.PolicyFromContext(ctx).ForNestedMagus())
}

// recursiveMagusEnv rebuilds BAGGAGE from the context so a script cannot
// shed its bound lease before a nested command.
func recursiveMagusEnv(ctx context.Context) []string {
	env := leaseEnv(ctx)
	for _, k := range spawn.ProcForwardVars {
		if v := os.Getenv(k); v != "" {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
			slog.DebugContext(ctx, types.FormatDiagnostic(types.ProcSocketWithheld,
				"magus socket injected into recursive magus invocation"), "var", k)
		}
	}
	return env
}

func leaseEnv(ctx context.Context) []string {
	if lease := jobtypes.LeaseFromContext(ctx); lease != "" {
		return []string{fmt.Sprintf("%s=%s=%s", recorder.EnvBaggage, recorder.BaggageLease, lease)}
	}
	return nil
}

// batchCommandEnv withholds the pool socket: the command holds its step's slot.
func batchCommandEnv(ctx context.Context) []string {
	for _, k := range spawn.ProcForwardVars {
		if os.Getenv(k) != "" {
			slog.DebugContext(ctx, types.FormatDiagnostic(types.ProcSocketWithheld,
				`magus socket withheld from a magus\cmdAll command`), "var", k)
		}
	}
	return leaseEnv(ctx)
}
