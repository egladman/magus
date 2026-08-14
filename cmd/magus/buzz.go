package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// buzzCmd runs Buzz source from a file, stdin, or an inline snippet using the
// Buzz interpreter with the full magus module surface (Buzz stdlib plus every
// magus host module: fs, os, http, markdown, template, ...) and the magus.*
// namespace. The magus.* members that declare into a workspace being loaded
// (magus\project and the provider selections) raise MGS1022 here, since a script
// has no magusfile to declare into. The REPL is the surface that does load one.
//
// This is the in-binary form of the former standalone magus-buzz tool, folded into
// the main command (like `kubectl kustomize`) so a clean Buzz runner is always
// present wherever magus is. The buzz spell's `run` op forks `magus buzz`, so the
// spell needs no separately-installed binary.
//
// With no arguments on an interactive terminal it opens a REPL, matching upstream
// `buzz`. A piped or redirected stdin still runs as a script (so `cat x | magus
// buzz` and heredocs keep working), and `magus buzz -` forces stdin.
func buzzCmd(ctx context.Context, root string, args []string) error {
	// `magus buzz lsp` is the Buzz language server (stdio LSP). It is a noun
	// subcommand of buzz, grouped with the rest of the Buzz-language tooling, rather
	// than a top-level `magus lsp`, so serving other languages later needs no new
	// top-level subcommand contract. Intercept it before flag parsing, which would
	// otherwise read "lsp" as a script filename.
	if len(args) > 0 && args[0] == "lsp" {
		return lspCmd(ctx, args[1:])
	}

	// Bound from the command registry rather than declared here. The -t/--test pair
	// is ONE switch, which a generated binder can only express because the registry
	// marks the second AliasOf the first; modelled as two flags they would get two
	// destinations and the shorthand would parse and then do nothing.
	var bf *gen.BuzzFlags
	rest, err := cmdParse("buzz", args, func(fs *flag.FlagSet) {
		bf = gen.BindBuzz(fs)
		fs.Usage = buzzUsage
	})
	if err != nil {
		return err
	}
	isRepl := bf.E == "" && !bf.Test && len(rest) == 0
	if !isRepl && (bf.NoAutoload || bf.C != "") {
		return usagef("magus buzz: --%s and -%s apply to the REPL, not to a script, -%s, or -%s",
			gen.FlagBuzzNoAutoload, gen.FlagBuzzC, gen.FlagBuzzE, gen.FlagBuzzT)
	}

	// No code, no file/stdin argument, and an interactive terminal: open the REPL,
	// which loads the magusfile at cwd when there is one - magus reads its context
	// rather than being told about it, and a REPL opened in a workspace is a REPL on
	// that workspace. Outside one it simply has nothing to autoload.
	// A non-terminal stdin (pipe, redirect, heredoc) falls through to script mode.
	// --embedded is a no-op on this path: a REPL is top-level statements by nature,
	// so the session is always embedded regardless of the flag.
	if isRepl && stdinIsTerminal() {
		return buzzRepl(ctx, bf.C, bf.NoAutoload)
	}

	code, name, err := buzzSource(bf.E, rest)
	if err != nil {
		return err
	}

	// Put the workspace on the script's context when there is one.
	//
	// Without this every workspace-reading member of the magus module raised MGS1022 from
	// a script - `magus\ls`, `affected`, `projectGraph`, `where`, `insight` - and the
	// error told the reader to reach for the forking `magus\cmd` instead. That advice was
	// sound only because nothing had put the workspace here: the process had already loaded
	// one (loadMagus is a sync.Once singleton, so this is the same instance the dispatcher
	// built, not a second load), and the script simply never saw it. A `magus buzz` script
	// run inside a workspace is a script ON that workspace, the same way the REPL below
	// already autoloads the magusfile at cwd.
	//
	// Best-effort: outside a workspace, or when one fails to load, the script still runs
	// and the workspace-reading members raise as before. A standalone script that touches
	// none of them must not be blocked by a magusfile it never asked about.
	//
	// The load error is LOGGED rather than discarded. Silently dropping it made the two
	// absences indistinguishable at the point a reader sees them: MGS1022 says "no
	// workspace on the context" either way, so a script inside a workspace that simply
	// failed to load reads as a script that was never in one, and the advice it gives
	// ("fork instead") is then wrong. This is not hypothetical - it is what a green
	// local run and a red CI run of the same script looked like, with nothing in
	// between to tell them apart.
	if m, lerr := loadMagus(ctx, root); lerr == nil && m != nil {
		ctx = types.WithWorkspace(ctx, m)
	} else if lerr != nil {
		slog.Warn("workspace not attached to this script; its workspace-reading members will raise MGS1022",
			slog.String("error", lerr.Error()))
	}

	// Default is strict (upstream Buzz parity, what the buzz spell's `run` op forks).
	// --embedded opts into the relaxations the magusfile engine uses, so a magus
	// module like the docs generator (render) can be run or tested here.
	var opts []buzz.Option
	if bf.Embedded {
		opts = append(opts, buzz.WithEmbedded())
	}
	sess := buzz.NewSession(ctx, opts...)
	defer func() { _ = sess.Close() }()
	// Install the full magus module surface (Buzz stdlib + assert/suite + every
	// magus host module), the same one the magusfile engine uses. Sharing one
	// registration keeps `magus buzz` and magusfile execution in lock-step: any
	// module a script or test imports resolves the same way in both, with no
	// per-surface module list.
	bindings.RegisterModuleSurface(ctx, sess, bindings.WithScriptOutput(os.Stdout))
	// The magus.* namespace on top, so `import "magus"` resolves here too. The
	// members that declare into a workspace being loaded (magus\project,
	// magus\cache.remote, magus\ci.provider) raise MGS1022 on this surface; the rest
	// (magus\describe, magus\cmd, magus\run, ...) work.
	bindings.RegisterMagusNamespace(ctx, sess)
	// Install the magus/spell and magus/charm source modules too, so a spell file
	// (which imports them) and its `test "..." {}` blocks run here: `magus buzz -t`
	// is the spell test harness.
	bindings.RegisterSpellSourceModules(sess)

	if err := sess.Exec(ctx, code); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	// Warnings (e.g. BZZ3001 unused imports) never fail Exec, so they only reach
	// the user if something prints them after the fact - print to stderr, matching
	// how every other magus diagnostic (and the -t failure lines below) stays off
	// stdout, which carries structured output only. -q/-s suppress them like any
	// other non-error progress output.
	if !global.quiet && !global.silent {
		for _, w := range sess.Warnings() {
			fmt.Fprintln(os.Stderr, w)
		}
	}
	if bf.Test {
		return runBuzzTests(ctx, sess, name)
	}
	// Like upstream's Run flavor and cmd/buzz, an entry script's `main` runs once
	// its top level has. Without it a script had to call its own main, which strict
	// mode cannot wrap: a top-level `try` is rejected and a bare call is BZZ1006.
	if mainFn := sess.GetGlobal("main"); mainFn.IsFun() {
		// Empty: buzzSource rejects a second positional, so there is no script argv
		// to pass yet. main still takes the parameter, as upstream declares it.
		var items []vm.Value
		ret, err := sess.CallValue(ctx, mainFn, []vm.Value{vm.ListValue(items)})
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		// `fun main() > int` is upstream's exit-status convention, and the checker
		// permits it; discarding the value made such a script always exit 0.
		if ret.IsInt() && ret.AsInt() != 0 {
			return errSilent{exitCode: int(ret.AsInt())}
		}
	}
	return nil
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
	fmt.Fprintln(os.Stderr, "Usage: magus buzz              # open a REPL with the magusfile loaded")
	fmt.Fprintln(os.Stderr, "       magus buzz <file>       # run a script")
	fmt.Fprintln(os.Stderr, "       magus buzz -            # run a script from stdin")
	fmt.Fprintln(os.Stderr, "       magus buzz -e <code>    # run an inline snippet")
	fmt.Fprintln(os.Stderr, "       magus buzz -t <file>    # run its test \"...\" {} blocks")
	fmt.Fprintln(os.Stderr, "       magus buzz lsp          # language server over stdio (LSP)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Run Buzz source from a REPL, file, stdin, or an inline snippet. With no")
	fmt.Fprintln(os.Stderr, "argument on a terminal it opens a REPL with the magusfile at cwd loaded,")
	fmt.Fprintln(os.Stderr, "its targets and bindings ready; a piped or redirected stdin runs as a")
	fmt.Fprintln(os.Stderr, "script. The Buzz stdlib, every magus host module (fs, os, http, markdown,")
	fmt.Fprintln(os.Stderr, "...), and the magus.* namespace are available in both.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  -e <code>   execute code given on the command line instead of a file")
	fmt.Fprintln(os.Stderr, "  -t, -test   run the file's test \"...\" {} blocks and report pass/fail")
	fmt.Fprintln(os.Stderr, "  --embedded  relax upstream strictness (top-level statements, optional")
	fmt.Fprintln(os.Stderr, "              argument labels) to match the magusfile engine")
	fmt.Fprintln(os.Stderr, "  --no-autoload  start the REPL without executing the magusfile")
	fmt.Fprintln(os.Stderr, "  -C <dir>    working directory for the REPL's import resolution")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Parsing is upstream-strict by default. A file written for the magusfile")
	fmt.Fprintln(os.Stderr, "engine needs --embedded, or it fails on rules upstream Buzz enforces and")
	fmt.Fprintln(os.Stderr, "magus does not (most often: \"argument N must be labeled\").")
}
