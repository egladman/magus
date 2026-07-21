package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings"
	buzzengine "github.com/egladman/magus/internal/interp/engine/buzz"
	"github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
)

// buzzCmd runs Buzz source from a file, stdin, or an inline snippet using the
// Buzz interpreter with the full magus module surface (Buzz stdlib plus every
// magus host module: fs, os, http, markdown, template, ...). The magus.* namespace
// (targets, needs) is not installed, since it needs a magusfile; for that use
// `magus repl --engine buzz` or run a magusfile target.
//
// This is the in-binary form of the former standalone magus-buzz tool, folded into
// the main command (like `kubectl kustomize`) so a clean Buzz runner is always
// present wherever magus is. The buzz spell's `run` op forks `magus buzz`, so the
// spell needs no separately-installed binary.
//
// With no arguments on an interactive terminal it opens a REPL, matching upstream
// `buzz`. A piped or redirected stdin still runs as a script (so `cat x | magus
// buzz` and heredocs keep working), and `magus buzz -` forces stdin.
func buzzCmd(ctx context.Context, args []string) error {
	// `magus buzz lsp` is the Buzz language server (stdio LSP). It is a noun
	// subcommand of buzz, grouped with the rest of the Buzz-language tooling, rather
	// than a top-level `magus lsp`, so serving other languages later needs no new
	// top-level subcommand contract. Intercept it before flag parsing, which would
	// otherwise read "lsp" as a script filename.
	if len(args) > 0 && args[0] == "lsp" {
		return lspCmd(ctx, args[1:])
	}

	var eval string
	var test bool
	var embedded bool
	rest, err := cmdParse("buzz", args, func(fs *flag.FlagSet) {
		fs.StringVar(&eval, "e", "", "execute `code` given on the command line instead of a file")
		fs.BoolVar(&test, "t", false, "run the file's `test \"...\" {}` blocks and report pass/fail")
		fs.BoolVar(&test, "test", false, "alias for -t")
		fs.BoolVar(&embedded, "embedded", false, "relax upstream strictness (top-level statements, optional arg labels) to match the magusfile engine")
		fs.Usage = buzzUsage
	})
	if err != nil {
		return err
	}

	// No code, no file/stdin argument, and an interactive terminal: open a REPL.
	// A non-terminal stdin (pipe, redirect, heredoc) falls through to script mode.
	// --embedded is a no-op on this path: a REPL is top-level statements by nature,
	// so buzzRepl is always embedded regardless of the flag.
	if eval == "" && !test && len(rest) == 0 && stdinIsTerminal() {
		return buzzRepl(ctx)
	}

	code, name, err := buzzSource(eval, rest)
	if err != nil {
		return err
	}

	// Default is strict (upstream Buzz parity, what the buzz spell's `run` op forks).
	// --embedded opts into the relaxations the magusfile engine uses, so a magus
	// module like the docs generator (render) can be run or tested here.
	var opts []buzz.Option
	if embedded {
		opts = append(opts, buzz.WithEmbedded())
	}
	sess := buzz.NewSession(ctx, opts...)
	defer func() { _ = sess.Close() }()
	// Install the full magus module surface (Buzz stdlib + assert/suite + every
	// magus host module), the same one the magusfile engine uses minus the magus.*
	// namespace, which needs a magusfile's targets. Sharing one registration keeps
	// `magus buzz` and magusfile execution in lock-step: any module a script or test
	// imports resolves the same way in both, with no per-surface module list.
	bindings.RegisterModuleSurface(ctx, sess)
	// Install the magus/target and magus/charm source modules too, so a spell file
	// (which imports them) and its `test "..." {}` blocks run here: `magus buzz -t`
	// is the spell test harness.
	bindings.RegisterSpellSourceModules(sess)

	if err := sess.Exec(ctx, code); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if test {
		return runBuzzTests(ctx, sess, name)
	}
	return nil
}

// buzzRepl opens an interactive Buzz REPL with the same module surface as
// `magus buzz` scripts (Buzz stdlib plus every magus host module), and without the
// magus.* namespace (that needs a magusfile - use `magus repl` for it). It reuses
// the shared REPL driver (internal/interp.Repl), the same one `magus repl` runs, so
// line editing, multi-line input, and the .commands behave identically. The session
// is embedded: a REPL is top-level statements by nature, so upstream strict mode
// (which forbids them) never applies here.
func buzzRepl(ctx context.Context) error {
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	bindings.RegisterModuleSurface(ctx, sess)
	return interp.Repl(ctx, buzzengine.Wrap(sess), interp.ReplOptions{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Banner: "magus buzz - Buzz REPL (.help for commands)",
	})
}

// stdinIsTerminal reports whether stdin is an interactive terminal (a character
// device) rather than a pipe, file, or heredoc. It gates the no-argument REPL so a
// piped script still runs as a whole.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// runBuzzTests executes every `test "..." {}` block registered while executing the
// file, printing one line per test and returning an error (non-zero exit) if any
// failed. A test fails when its body raises, typically an unmet std.assert.
func runBuzzTests(ctx context.Context, sess *buzz.Session, name string) error {
	tests := sess.Tests()
	if len(tests) == 0 {
		fmt.Printf("%s: no tests\n", name)
		return nil
	}
	failed := 0
	skipped := 0
	for _, tc := range tests {
		_, err := sess.CallValue(ctx, tc.Fn, []vm.Value(nil))
		if err == nil {
			fmt.Printf("ok    test %q\n", tc.Name)
			continue
		}
		if reason, ok := buzzstd.SkipMessage(err); ok {
			skipped++
			fmt.Printf("skip  test %q%s\n", tc.Name, skipReason(reason))
			continue
		}
		failed++
		fmt.Printf("FAIL  test %q\n      %v\n", tc.Name, err)
	}
	fmt.Printf("---\n%d passed, %d failed, %d skipped\n", len(tests)-failed-skipped, failed, skipped)
	if failed > 0 {
		return fmt.Errorf("%d of %d tests failed", failed, len(tests))
	}
	return nil
}

// skipReason formats a non-empty skip reason as " (reason)" for the test line.
func skipReason(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

// buzzSource resolves the program text (and a name for diagnostics) from the -e
// flag and positional args. Exactly one input is allowed: -e, a single file path,
// or stdin (no args, or "-").
//
// When a bare filename is given (no directory separator), BUZZ_INCLUDE_PATH is
// searched if the file is not found in the working directory, matching the
// upstream Buzz toolchain convention.
func buzzSource(eval string, args []string) (code, name string, err error) {
	switch {
	case eval != "":
		if len(args) > 0 {
			return "", "", fmt.Errorf("cannot combine -e with a file argument")
		}
		return eval, "-e", nil
	case len(args) > 1:
		return "", "", fmt.Errorf("expected at most one file argument, got %d", len(args))
	case len(args) == 1 && args[0] != "-":
		resolved := buzzResolveFile(args[0])
		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", "", err
		}
		return string(data), resolved, nil
	default: // no args, or "-": read stdin
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", "", fmt.Errorf("read stdin: %w", err)
		}
		return string(data), "<stdin>", nil
	}
}

// buzzResolveFile returns the path to use for reading a script. If the path
// contains a separator it is used as-is. Otherwise BUZZ_INCLUDE_PATH
// (colon-separated) is searched for the first match, falling back to the original
// path (which produces a clear "no such file" error).
func buzzResolveFile(path string) string {
	if filepath.Base(path) != path {
		return path
	}
	includePath := os.Getenv("BUZZ_INCLUDE_PATH")
	if includePath == "" {
		return path
	}
	for _, dir := range filepath.SplitList(includePath) {
		candidate := filepath.Join(dir, path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return path
}

func buzzUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus buzz              # open a REPL (interactive terminal)")
	fmt.Fprintln(os.Stderr, "       magus buzz <file>       # run a script")
	fmt.Fprintln(os.Stderr, "       magus buzz -            # run a script from stdin")
	fmt.Fprintln(os.Stderr, "       magus buzz -e <code>    # run an inline snippet")
	fmt.Fprintln(os.Stderr, "       magus buzz -t <file>    # run its test \"...\" {} blocks")
	fmt.Fprintln(os.Stderr, "       magus buzz lsp          # language server over stdio (LSP)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Run Buzz source from a REPL, file, stdin, or an inline snippet. With no")
	fmt.Fprintln(os.Stderr, "argument on a terminal it opens a REPL; a piped or redirected stdin runs")
	fmt.Fprintln(os.Stderr, "as a script. The Buzz stdlib plus every magus host module (fs, os, http,")
	fmt.Fprintln(os.Stderr, "markdown, ...) are available; the magus.* namespace is not (use `magus repl`).")
}
