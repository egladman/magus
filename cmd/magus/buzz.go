package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/gopherbuzz"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/termkit"
)

// buzzLoadWorkspace opens the workspace a script reads. A variable so a test can count
// opens without the process singleton loadMagus memoizes.
var buzzLoadWorkspace = loadMagus

// buzzCommand runs Buzz source from a file, stdin, or an inline snippet using the
// Buzz interpreter with the full set of magus modules (Buzz stdlib plus every
// magus host module: fs, os, http, markdown, template, ...) and the magus.*
// namespace. The magus.* members that declare into a workspace being loaded
// (magus\project and the provider selections) raise MGS1022 here, since a script
// has no magusfile to declare into. The REPL is the mode that does load one.
//
// This is the in-binary form of the former standalone magus-buzz tool, folded into
// the main command (like `kubectl kustomize`) so a clean Buzz runner is always
// present wherever magus is. The buzz spell's `run` op forks `magus buzz`, so the
// spell needs no separately-installed binary.
//
// With no arguments on an interactive terminal it opens a REPL, matching upstream
// `buzz`. A piped or redirected stdin still runs as a script (so `cat x | magus
// buzz` and heredocs keep working), and `magus buzz -` forces stdin.
func buzzCommand(ctx context.Context, root string, args []string) (retErr error) {
	// A relative --root names a directory from where magus started, so it is pinned
	// before -C moves the process.
	if root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return fmt.Errorf("magus buzz: --root: %w", err)
		}
		root = abs
	}
	// A leading -C is taken here so it reaches `lsp`, which is intercepted before the
	// flag parse; anywhere else it arrives through bf.C below.
	if dir, rest, ok := cutBuzzWorkDir(args); ok {
		if err := buzzChdir(dir); err != nil {
			return err
		}
		args = rest
	}

	// `magus buzz lsp` is the Buzz language server (stdio LSP). It is a noun
	// subcommand of buzz, grouped with the rest of the Buzz-language tooling, rather
	// than a top-level `magus lsp`, so serving other languages later needs no new
	// top-level subcommand contract. Intercept it before flag parsing, which would
	// otherwise read "lsp" as a script filename.
	if len(args) > 0 && args[0] == "lsp" {
		return lspCommand(ctx, args[1:])
	}

	// Everything after `--` is the SCRIPT's argv, never magus's. The boundary is
	// needed because commandParse reorders flags ahead of positionals, so a bare
	// `script.buzz --raw` would have magus parsing --raw and failing; it is also the
	// separator this CLI already uses to forward args to a tool (`run go::go-test . --
	// -run X`). A script reads them as main's [str], the way cmd/buzz and upstream do.
	args, sep, forwarded := interp.SplitScriptArgs(args)

	// Bound from the command registry rather than declared here. The -t/--test pair
	// is ONE switch, which a generated binder can only express because the registry
	// marks the second AliasOf the first; modeled as two flags they would get two
	// destinations and the shorthand would parse and then do nothing.
	var bf *flaggen.BuzzFlags
	rest, err := commandParse("buzz", args, func(fs *flag.FlagSet) {
		bf = flaggen.BindBuzz(fs)
		fs.Usage = func() { commands.WriteBuzzUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}
	if bf.C != "" {
		if err := buzzChdir(bf.C); err != nil {
			return err
		}
	}
	runner := newBuzzRunner(ctx)
	isRepl := bf.E == "" && !bf.Test && !bf.Check && len(rest) == 0
	if !isRepl && !bf.Autoload {
		return exit.Usagef("magus buzz: --%s applies to the REPL, not to a script, -%s, or -%s",
			"no-"+flaggen.FlagBuzzAutoload, flaggen.FlagBuzzE, flaggen.FlagBuzzT)
	}
	if bf.Coverprofile != "" && !bf.Test {
		return exit.Usagef("magus buzz: --%s requires -%s", flaggen.FlagBuzzCoverprofile, flaggen.FlagBuzzT)
	}
	// Refused rather than ordered, because either order is a defensible reading and
	// picking one silently gives back an answer to a question nobody asked: -t runs the
	// file, --check is the mode that does not.
	if bf.Check && bf.Test {
		return exit.Usagef("magus buzz: --%s and -%s are different modes; --%s does not run the file",
			flaggen.FlagBuzzCheck, flaggen.FlagBuzzT, flaggen.FlagBuzzCheck)
	}
	// A trace (-vvv) already carries a profile and prints it when the process
	// exits. --profile is the switch for a run that is not otherwise traced, and
	// it prints once, here, so the two do not each emit the same report.
	if bf.Profile && buzz.ProfileFromContext(ctx) == nil {
		p := buzz.NewProfile()
		ctx = buzz.WithProfile(ctx, p)
		defer func() {
			if rep := p.Report(); rep != "" {
				slog.InfoContext(ctx, strings.TrimSuffix(rep, "\n"), attr.Notice(""))
			}
		}()
	}
	if bf.Check {
		// -e and stdin are deliberately absent: a check reports positions, and both
		// name a source no reader can open at the position reported. `magus buzz -e`
		// already surfaces a compile error on its own, since it compiles before it runs.
		if bf.E != "" || len(rest) == 0 || (len(rest) == 1 && rest[0] == "-") {
			return exit.Usagef("magus buzz: --%s takes one or more file paths", flaggen.FlagBuzzCheck)
		}
		ctx, cerr := runner.Context(ctx, root)
		if cerr != nil {
			return cerr
		}
		return runner.Check(ctx, rest, bf.Embedded)
	}

	// No code, no file/stdin argument, and an interactive terminal: open the REPL,
	// which loads the magusfile at cwd when there is one: magus reads its context
	// rather than being told about it, and a REPL opened in a workspace is a REPL on
	// that workspace. Outside one it simply has nothing to autoload.
	// A non-terminal stdin (pipe, redirect, heredoc) falls through to script mode.
	// --embedded is a no-op on this path: a REPL is top-level statements by nature,
	// so the session is always embedded regardless of the flag.
	if isRepl && termkit.StdinIsTerminal() {
		return buzzRepl(ctx, !bf.Autoload)
	}

	opts := interp.ScriptOptions{Embedded: bf.Embedded, Inline: bf.E != "", Test: bf.Test, Record: bf.Record}
	if bf.Test {
		return runner.Test(ctx, root, opts, bf.Coverprofile, bf.E, rest, forwarded)
	}
	return runner.Run(ctx, root, opts, bf.E, rest, forwarded, sep)
}

// newBuzzRunner wires the script runner to this process: the workspace opener, the
// config's sandbox switch, the quiet flags, the startup trace and the recorder.
func newBuzzRunner(ctx context.Context) *interp.ScriptRunner {
	return &interp.ScriptRunner{
		Open:    openScriptWorkspace,
		Adopted: func(ctx context.Context) bool { _, ok := magusFromContext(ctx); return ok },
		Sandbox: globalCfg.Sandbox.Mode.Enabled(),
		Quiet:   global.quiet || global.silent,
		Host: interp.ScriptHost{
			RegisterModules: func(ctx context.Context, sess *buzz.Session, out io.Writer) {
				bindings.RegisterModules(ctx, sess, bindings.WithScriptOutput(out))
			},
			RegisterSpellDecls:     bindings.RegisterSpellDecls,
			RegisterMagusNamespace: bindings.RegisterMagusNamespace,
			DeclareMagusTypes:      bindings.DeclareMagusTypes,
			// No workspace is open, so no magus.yaml override applies.
			RegisterSpellImports: func(sess *buzz.Session) { bindings.RegisterSpellImports(sess, nil) },
		},
		Phase: observability.TraceFromContext(ctx).Phase,
		Record: func(ctx context.Context, root string, rec interp.ScriptRecord) error {
			return recordBuzzRun(ctx, root, rec.Name, rec.Code, rec.Args, rec.Test, rec.Out, rec.Started, rec.Err)
		},
	}
}

// openScriptWorkspace opens the workspace a script reads, as the three reads the
// script runner takes.
func openScriptWorkspace(ctx context.Context, root string) (*interp.ScriptWorkspace, error) {
	m, err := buzzLoadWorkspace(ctx, root)
	if err != nil || m == nil {
		return nil, err
	}
	return &interp.ScriptWorkspace{Repo: viewInternals(m), CacheDir: m.CacheDir(), ApplySandbox: m.ApplySandbox}, nil
}

// recordBuzzRun keeps what a recorded run printed in the output store and prints its ref
// on stderr, so a plan, a review or a job's notes can cite the run instead of describing it.
// A test run is stored as `buzz -t <script>`, apart from the script's main.
// The record holds the args and the lease, so a check binds to both.
// A failure to record is returned. The ref skips slog, which silent mode drops.
func recordBuzzRun(ctx context.Context, root, name, code string, args []string, test bool, out []byte, started time.Time, runErr error) error {
	if root == "" {
		root = "."
	}
	dir, err := magus.ResolveCacheDir(root, cliOptions()...)
	if err != nil {
		return fmt.Errorf("magus buzz: --record: no output store here: %w", err)
	}
	lease, err := recordLease(ctx, root, name)
	if err != nil {
		return fmt.Errorf("magus buzz: --record: %w", err)
	}
	target, key := "buzz "+name, interp.ScriptRunKey(name, code, lease, args)
	if test {
		target, key = "buzz -t "+name, interp.ScriptRunKey(name, code, lease, append([]string{"-t"}, args...))
	}
	d := cache.OutputDescriptor{
		Project:     ".",
		Target:      target,
		Failed:      runErr != nil,
		TimestampMs: started.UnixMilli(),
		DurationMs:  time.Since(started).Milliseconds(),
		ExtraArgs:   slices.Clone(args),
		Lease:       lease,
	}
	if runErr != nil {
		d.ErrMsg = runErr.Error()
	}
	stored, err := cache.NewOutputStore(dir).Persist(ctx, key, out, d)
	if err != nil {
		return fmt.Errorf("magus buzz: --record: store the run: %w", err)
	}
	_, err = fmt.Fprintln(os.Stderr, "ref  "+stored.Ref)
	return err
}

// recordLease is the lease a recorded run of script acts under.
func recordLease(ctx context.Context, root, script string) (string, error) {
	here, err := resolvedDir(root)
	if err != nil {
		return "", err
	}
	lease, _, err := guard.CheckoutLease(ctx, here, jobPlace)
	if err != nil || lease != "" {
		return lease, err
	}
	rows, err := readJobRows(ctx, here)
	if err != nil {
		// The rows only bind the run to a lease. A store this magus cannot read
		// is no reason to lose the run's record.
		fmt.Fprintf(os.Stderr, "magus buzz: --record: the job store cannot be read (%v); the run is recorded without a lease\n", err)
		return "", nil
	}
	holder, err := scriptCheckHolder(rows, here, script)
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus buzz: --record: no job row could be matched to the run (%v); it is recorded without a lease\n", err)
		return "", nil
	}
	return holder, nil
}

// scriptCheckHolder names the one live row here whose goal checks script.
// A CLI process cannot see the host session a hook bound its worker by.
// Two such rows leave it empty: the run cannot say whose it was.
// A goal whose check will not read is an error, since it could be the one.
func scriptCheckHolder(rows []jobtypes.Job, root, script string) (string, error) {
	here, err := resolvedDir(root)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", root, err)
	}
	holder := ""
	for _, row := range rows {
		there, err := resolvedDir(row.CheckoutRoot)
		if err != nil || there != here || !job.Editing(row) {
			continue
		}
		checks, err := goalChecksScript(row, script)
		if err != nil {
			return "", err
		}
		if !checks {
			continue
		}
		if holder != "" {
			return "", nil
		}
		holder = row.ID
	}
	return holder, nil
}

// goalChecksScript reports whether one of row's goals is a check that runs script.
func goalChecksScript(row jobtypes.Job, script string) (bool, error) {
	for _, g := range row.EffectiveGoals() {
		if !jobtypes.HasCheck(g.Check) {
			continue
		}
		check, err := job.GoalCheck(g)
		if err != nil {
			return false, fmt.Errorf("job %s goal %s: %w", row.ID, g.ID, err)
		}
		if check.Script != "" && path.Clean(check.Script) == path.Clean(script) {
			return true, nil
		}
	}
	return false, nil
}

// resolvedDir is dir absolute with symlinks followed: exec records the
// resolved checkout, and a shell's cwd may name it through a link.
func resolvedDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// cutBuzzWorkDir takes a -C that leads args, in any spelling the flag package accepts.
func cutBuzzWorkDir(args []string) (dir string, rest []string, ok bool) {
	if len(args) == 0 {
		return "", args, false
	}
	switch a := args[0]; {
	case a == "-"+flaggen.FlagBuzzC || a == "--"+flaggen.FlagBuzzC:
		if len(args) < 2 {
			return "", args, false
		}
		return args[1], args[2:], true
	case strings.HasPrefix(a, "-"+flaggen.FlagBuzzC+"="), strings.HasPrefix(a, "--"+flaggen.FlagBuzzC+"="):
		_, v, _ := strings.Cut(a, "=")
		return v, args[1:], true
	}
	return "", args, false
}

// buzzChdir moves the process to dir. `magus buzz` is never forwarded to a server, so
// the directory it changes is its own.
func buzzChdir(dir string) error {
	if err := os.Chdir(dir); err != nil {
		return exit.Usagef("magus buzz: -%s %s: %v", flaggen.FlagBuzzC, dir, errors.Unwrap(err))
	}
	return nil
}
