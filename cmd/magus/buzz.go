package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
	vm "github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/internal/dry"
)

// buzzCmd runs Buzz source from a file, stdin, or an inline snippet using the
// Buzz interpreter and its standard library. No magus host bindings (magus, fs,
// vcs, spells) are installed; for the binding-rich experience use
// `magus repl --engine buzz` instead.
//
// This is the in-binary form of the former standalone magus-buzz tool, folded into
// the main command (like `kubectl kustomize`) so a clean Buzz runner is always
// present wherever magus is. The buzz spell's `run` op forks `magus buzz`, so the
// spell needs no separately-installed binary.
func buzzCmd(ctx context.Context, args []string) error {
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

	code, name, err := buzzSource(eval, rest)
	if err != nil {
		return err
	}

	// Default is strict (upstream Buzz parity, what the buzz spell's `run` op forks).
	// --embedded opts into the relaxations the magusfile engine uses, so a magus
	// module like the docs generator (scribe) can be run or tested here.
	var opts []buzz.Option
	if embedded {
		opts = append(opts, buzz.WithEmbedded())
	}
	sess := buzz.NewSession(ctx, opts...)
	defer func() { _ = sess.Close() }()
	buzzstd.Register(sess)
	// Add the magus-only pure-compute modules Buzz's own stdlib lacks (markdown,
	// encoding), so a magus-context script or test can use them without the full
	// host-binding engine. Deliberately additive: do NOT override modules buzzstd
	// already provides (crypto, env, ...), so upstream-Buzz behavior for the buzz
	// spell is preserved. Both come from the playground's browser-safe registry.
	for _, modName := range []string{"markdown", "encoding"} {
		if register := dry.BrowserSafeHostModules[modName]; register != nil {
			sess.SetSyntheticModule(modName, register(ctx, sess))
		}
	}

	if err := sess.Exec(ctx, code); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if test {
		return runBuzzTests(ctx, sess, name)
	}
	return nil
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
	for _, tc := range tests {
		if _, err := sess.CallValue(ctx, tc.Fn, []vm.Value(nil)); err != nil {
			failed++
			fmt.Printf("FAIL  test %q\n      %v\n", tc.Name, err)
		} else {
			fmt.Printf("ok    test %q\n", tc.Name)
		}
	}
	fmt.Printf("---\n%d passed, %d failed\n", len(tests)-failed, failed)
	if failed > 0 {
		return fmt.Errorf("%d of %d tests failed", failed, len(tests))
	}
	return nil
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
	fmt.Fprintln(os.Stderr, "Usage: magus buzz [file | -]")
	fmt.Fprintln(os.Stderr, "       magus buzz -e <code>")
	fmt.Fprintln(os.Stderr, "       magus buzz -t <file>   # run its test \"...\" {} blocks")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Run Buzz source from a file, stdin, or an inline snippet.")
	fmt.Fprintln(os.Stderr, "The Buzz language stdlib plus magus extras (markdown) are available, but")
	fmt.Fprintln(os.Stderr, "no magus host bindings (use `magus repl --engine buzz` for the binding-rich shell).")
}
