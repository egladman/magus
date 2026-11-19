// Command buzz is a standalone runner for the Buzz language, mirroring the
// upstream `buzz` CLI (https://buzz-lang.dev). It runs a script from a file,
// stdin, or an inline snippet, and can type-check or dump the AST without
// running. The Buzz standard library is available (import "std", "math", …);
// no magus host bindings are installed.
//
// Its command line is declared through libs/clikit, as every Go command in
// the magus repository is, and no third-party CLI framework is used.
//
//	buzz script.buzz             # run a file
//	buzz                         # run stdin
//	buzz -e 'import "std"; std.print("hi");'
//	buzz -c script.buzz          # type-check only
//	buzz -t script.buzz          # run its test "..." {} blocks
//	buzz --ast script.buzz       # dump the AST as JSON
//	buzz -L ./lib script.buzz    # add an import search path
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/egladman/magus/libs/clikit/command"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	json "github.com/egladman/magus/libs/gopherbuzz/internal/codec"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// Build metadata is injected by the gopherbuzz module's buzz-build target.
// Defaults preserve useful output for `go run` and unreleased local builds.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// init pins the interpreter to the process's main OS thread. Scripts that
// zdef() into single-thread-affine C frameworks (macOS AppKit above all)
// must issue those calls from the main thread; without the lock the main
// goroutine may migrate. Free for everything else, so it is unconditional.
func init() { //nolint:gochecknoinits // runtime.LockOSThread pins the main goroutine to the main thread only when called from init
	runtime.LockOSThread()
}

func main() {
	line := fmt.Sprintf("buzz %s (gopherbuzz %s, commit %s, built %s, upstream %s, bytecode v%d)",
		buzz.LanguageVersion, version, commit, buildDate, buzz.UpstreamRef, vm.BytecodeVersion)
	command.Main(buzzCommand(run), command.WithVersion(line))
}

// opts holds the parsed command line.
type opts struct {
	eval     string   // -e <code>
	check    bool     // -c / --check
	embedded bool     // --embedded
	test     bool     // -t / --test
	dumpAST  bool     // --ast
	libDirs  []string // -L / --library-path (repeatable)
	args     []string // positional: [script, scriptArgs...]
}

// buzzCommand declares the upstream-style command line, handing it to run as
// opts. Every option takes its short (-c) and long (--check) spelling, options
// stop at the first non-option (so a script's own arguments are never mistaken
// for buzz options), and "--" forces the end of options.
func buzzCommand(run func(context.Context, opts) error) command.Command {
	var o opts
	return command.Command{
		Name:  "buzz",
		Short: "buzz - run Buzz scripts (gopherbuzz)",
		Usage: "buzz [options] [script] [-- script-args...]\n" +
			"       buzz [options] -          read the script from stdin\n" +
			"       buzz [options] -e <code>  run an inline snippet",
		Long: `The Buzz standard library is available (import "std", "math", "ffi", ...).`,
		Flags: []command.Flag{
			{Name: "eval", Kind: command.String, Doc: "run `code` instead of a file", Target: &o.eval},
			{Name: "e", Kind: command.String, AliasOf: "eval", Doc: "run `code` instead of a file"},
			{Name: "check", Kind: command.Bool, Default: false, Doc: "type-check the script without running it", Target: &o.check},
			{Name: "c", Kind: command.Bool, AliasOf: "check", Doc: "type-check the script without running it"},
			{Name: "embedded", Kind: command.Bool, Default: false, Doc: "relax upstream script rules (allow top-level control flow + unlabeled args) for embedding-style scripts", Target: &o.embedded},
			{Name: "test", Kind: command.Bool, Default: false, Doc: `run the script's test "..." { } blocks`, Target: &o.test},
			{Name: "t", Kind: command.Bool, AliasOf: "test", Doc: `run the script's test "..." { } blocks`},
			{Name: "ast", Kind: command.Bool, Default: false, Doc: "dump the parsed AST as JSON and exit", Target: &o.dumpAST},
			{Name: "library-path", Kind: command.StringList, Doc: "add an import search path (repeatable)", Target: &o.libDirs},
			{Name: "L", Kind: command.StringList, AliasOf: "library-path", Doc: "add an import search path (repeatable)"},
			{Name: "v", Kind: command.Bool, AliasOf: "version", Doc: "print the version and exit"},
		},
		Run: func(ctx context.Context, inv command.Invocation) error {
			if len(inv.Args) > 0 {
				o.args = inv.Args
			}
			return run(ctx, o)
		},
	}
}

func run(ctx context.Context, o opts) error {
	code, name, err := source(o)
	if err != nil {
		return err
	}

	// --ast needs only the parser; it neither imports nor runs. Default strict (like
	// upstream); --embedded relaxes for embedding-style scripts.
	if o.dumpAST {
		parse := buzz.Parse
		if o.embedded {
			parse = buzz.ParseEmbedded
		}
		prog, err := parse(code)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		b, err := json.MarshalIndent(prog, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	// Strict by default, matching upstream Buzz (which has no embedded mode); the
	// embedding hosts opt into leniency, and so can this CLI via --embedded.
	var sessOpts []buzz.Option
	if o.embedded {
		sessOpts = append(sessOpts, buzz.WithEmbedded())
	}
	sess := buzz.NewSession(ctx, sessOpts...)
	defer func() { _ = sess.Close() }()
	buzzstd.Register(sess)
	if dirs := libDirs(o); len(dirs) > 0 {
		sess.SetIncludeDirs(dirs)
	}

	// --check type-checks (parse + imports + checker) without executing.
	if o.check {
		if _, err := sess.Compile(code); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}

	if err := sess.Exec(ctx, code); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	// --test runs each `test "..." {}` block registered while executing the file
	// and reports pass/fail, matching upstream `buzz --test`.
	if o.test {
		return runTests(ctx, sess, name)
	}

	// Like upstream's Run flavor, the entry-point script's `main(args)` function
	// is invoked automatically after its top-level runs. The CLI args after the
	// script name are passed as a [str]; a script with no `main` is a no-op.
	if mainFn := sess.GetGlobal("main"); mainFn.IsFun() {
		var items []vm.Value
		if len(o.args) > 1 {
			for _, a := range o.args[1:] {
				items = append(items, vm.StringValue(a))
			}
		}
		if _, err := sess.CallValue(ctx, mainFn, []vm.Value{vm.ListValue(items)}); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// runTests executes every registered test block, printing one line per test, and
// returns an error if any test failed (so the process exits non-zero). A test
// "fails" when its body raises — typically a std.assert that did not hold.
func runTests(ctx context.Context, sess *buzz.Session, name string) error {
	tests := sess.Tests()
	if len(tests) == 0 {
		fmt.Printf("%s: no tests\n", name)
		return nil
	}
	failed := 0
	skipped := 0
	for _, tc := range tests {
		_, err := sess.CallValue(ctx, tc.Fn, nil)
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
	return fmt.Sprintf(" (%s)", reason)
}

// source resolves the program text and a name for diagnostics. Exactly one input
// is allowed: -e, a single file path, or stdin (no args, or "-").
func source(o opts) (code, name string, err error) {
	switch {
	case o.eval != "":
		if len(o.args) > 0 {
			return "", "", fmt.Errorf("cannot combine -e with a script argument")
		}
		return o.eval, "-e", nil
	case len(o.args) >= 1 && o.args[0] != "-":
		resolved := resolveFile(o.args[0], libDirs(o))
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

// resolveFile locates a script. A path with a separator is used as-is; a bare
// name is searched in the library dirs (-L) and then BUZZ_INCLUDE_PATH, matching
// the upstream Buzz toolchain convention, before falling back to the name itself
// (which yields a clear "no such file" error).
func resolveFile(path string, dirs []string) string {
	if filepath.Base(path) != path {
		return path
	}
	for _, dir := range dirs {
		if candidate := filepath.Join(dir, path); fileExists(candidate) {
			return candidate
		}
	}
	return path
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// libDirs merges -L paths with BUZZ_INCLUDE_PATH (colon/semicolon-separated),
// -L taking precedence.
func libDirs(o opts) []string {
	dirs := append([]string(nil), o.libDirs...)
	if env := os.Getenv("BUZZ_INCLUDE_PATH"); env != "" {
		dirs = append(dirs, filepath.SplitList(env)...)
	}
	return dirs
}
