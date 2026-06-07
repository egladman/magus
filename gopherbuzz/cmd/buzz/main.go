// Command buzz is a standalone runner for the Buzz language, mirroring the
// upstream `buzz` CLI (https://buzz-lang.dev). It runs a script from a file,
// stdin, or an inline snippet, and can type-check or dump the AST without
// running. The Buzz standard library is available (import "std", "math", …);
// no magus host bindings are installed.
//
// Only the Go standard library and the gopherbuzz packages are used — no
// third-party CLI framework — so the binary builds anywhere gopherbuzz does.
//
//	buzz script.buzz             # run a file
//	buzz                         # run stdin
//	buzz -e 'import "std"; std.print("hi");'
//	buzz -c script.buzz          # type-check only
//	buzz --ast script.buzz       # dump the AST as JSON
//	buzz -L ./lib script.buzz    # add an import search path
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
)

// version is the Buzz language version gopherbuzz targets.
const version = "0.5.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "buzz: "+err.Error())
		os.Exit(1)
	}
}

// opts holds the parsed command line.
type opts struct {
	eval     string   // -e <code>
	check    bool     // -c / --check
	dumpAST  bool     // --ast
	showVer  bool     // -v / --version
	showHelp bool     // -h / --help
	libDirs  []string // -L / --library-path (repeatable)
	args     []string // positional: [script, scriptArgs...]
}

func run(argv []string) error {
	o, err := parseArgs(argv)
	if err != nil {
		return err
	}
	if o.showHelp {
		usage(os.Stdout)
		return nil
	}
	if o.showVer {
		fmt.Printf("buzz %s (gopherbuzz, bytecode v%d)\n", version, buzz.BytecodeVersion)
		return nil
	}

	code, name, err := source(o)
	if err != nil {
		return err
	}

	// --ast needs only the parser; it neither imports nor runs.
	if o.dumpAST {
		prog, err := buzz.Parse(code)
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

	ctx := context.Background()
	sess := buzz.NewSession(ctx)
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
	return nil
}

// parseArgs parses the upstream-style flag set by hand: every option accepts
// both its short (-c) and long (--check) spelling, options stop at the first
// non-option (so a script's own arguments are never mistaken for buzz options),
// and "--" forces the end of options.
func parseArgs(argv []string) (opts, error) {
	var o opts
	i := 0
	for ; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			i++
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			break // "-" (stdin) or a script path: positionals start here
		}
		switch a {
		case "-h", "--help":
			o.showHelp = true
		case "-v", "--version":
			o.showVer = true
		case "-c", "--check":
			o.check = true
		case "--ast":
			o.dumpAST = true
		case "-e", "--eval":
			val, err := optValue(argv, &i, a)
			if err != nil {
				return o, err
			}
			o.eval = val
		case "-L", "--library-path":
			val, err := optValue(argv, &i, a)
			if err != nil {
				return o, err
			}
			o.libDirs = append(o.libDirs, val)
		default:
			// Support --flag=value for -e and -L.
			if k, v, ok := strings.Cut(a, "="); ok {
				switch k {
				case "-e", "--eval":
					o.eval = v
					continue
				case "-L", "--library-path":
					o.libDirs = append(o.libDirs, v)
					continue
				}
			}
			return o, fmt.Errorf("unknown option %q (try --help)", a)
		}
	}
	if i < len(argv) {
		o.args = argv[i:]
	}
	return o, nil
}

// optValue returns the argument following an option that takes a value,
// advancing the index past it.
func optValue(argv []string, i *int, flag string) (string, error) {
	if *i+1 >= len(argv) {
		return "", fmt.Errorf("option %s requires a value", flag)
	}
	*i++
	return argv[*i], nil
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

func usage(w io.Writer) {
	fmt.Fprint(w, `buzz — run Buzz scripts (gopherbuzz)

Usage:
  buzz [options] [script] [-- script-args...]
  buzz [options] -          read the script from stdin
  buzz [options] -e <code>  run an inline snippet

Options:
  -e, --eval <code>          run <code> instead of a file
  -c, --check                type-check the script without running it
      --ast                  dump the parsed AST as JSON and exit
  -L, --library-path <dir>   add an import search path (repeatable)
  -v, --version              print the version and exit
  -h, --help                 print this help and exit

The Buzz standard library is available (import "std", "math", "ffi", …).
Test blocks are not supported by this implementation.
`)
}
