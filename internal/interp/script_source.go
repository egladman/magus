package interp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/egladman/magus/std"
)

// ScriptRunKey is the output-store key of a --record run: the script's source and its
// arguments, so rerunning one probe adds an attempt under the same ref and a changed probe
// gets a new one.
func ScriptRunKey(name, code, lease string, args []string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("buzz\x00%s\x00%s\x00%s\x00%s", name, code, lease, strings.Join(args, "\x00"))))
	return hex.EncodeToString(sum[:])
}

// scriptSource resolves the program text (and a name for diagnostics) from the -e
// flag and positional args. Exactly one input is allowed: -e, a single file path,
// or stdin (no args, or "-").
//
// When a bare filename is given (no directory separator), BUZZ_INCLUDE_PATH is
// searched if the file is not found in the working directory, matching the
// upstream Buzz toolchain convention.
func scriptSource(eval string, args []string) (code, name string, scriptArgs []string, err error) {
	switch {
	case eval != "":
		if len(args) > 0 {
			return "", "", nil, fmt.Errorf("cannot combine -e with a file argument")
		}
		return eval, "-e", nil, nil
	case len(args) >= 1 && args[0] != "-":
		resolved := resolveScriptFile(args[0])
		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", "", nil, err
		}
		return string(data), resolved, args[1:], nil
	default: // no args, or "-": read stdin
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", "", nil, fmt.Errorf("read stdin: %w", err)
		}
		rest := args
		if len(rest) > 0 { // drop the "-" that named stdin
			rest = rest[1:]
		}
		return string(data), "<stdin>", rest, nil
	}
}

// SplitScriptArgs cuts the command line at the first `--`: what precedes it is
// magus's to parse, what follows is the script's own argv. The bool reports whether
// a separator was present at all, so an empty tail after `--` stays distinguishable
// from no separator.
func SplitScriptArgs(args []string) (before []string, sep bool, after []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], true, args[i+1:]
		}
	}
	return args, false, nil
}

// scriptCoverPath returns a stable, slash-separated path for LCOV SF: records.
// Absolute paths under the cwd become relative so a coverprofile is machine-
// stable; -e and stdin have no file to attribute and return "".
func scriptCoverPath(name string) string {
	switch name {
	case "", "-e", "<stdin>":
		return ""
	}
	path := name
	if abs, err := filepath.Abs(name); err == nil {
		if cwd, err := os.Getwd(); err == nil {
			if rel, err := filepath.Rel(cwd, abs); err == nil && !strings.HasPrefix(rel, "..") {
				path = rel
			}
		}
	}
	return filepath.ToSlash(path)
}

// scriptImportDir is the directory a file script's imports resolve against.
// Stdin and -e have none: there is no file for a sibling import to stand beside.
func scriptImportDir(name string) string {
	switch name {
	case "", "-e", "<stdin>":
		return ""
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return filepath.Dir(name)
	}
	return filepath.Dir(abs)
}

// resolveScriptFile returns the path to use for reading a script. If the path
// contains a separator it is used as-is. Otherwise BUZZ_INCLUDE_PATH
// (colon-separated) is searched for the first match, falling back to the original
// path (which produces a clear "no such file" error).
func resolveScriptFile(path string) string {
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

// scriptReachesMagus reports whether code, or any file or source module it imports
// transitively, imports the magus namespace, a magus/* module or a spell. An import
// found nowhere counts as not reaching magus, and so does one inside a comment or a
// string: the lexer, not a pattern, finds them.
func scriptReachesMagus(sess *buzz.Session, code string) bool {
	sources := map[string]string{}
	for _, m := range std.AllSource() {
		sources[m.ImportPath()] = m.Source
	}
	seen := map[string]bool{}
	var reaches func(code, dir string) bool
	reaches = func(code, dir string) bool {
		for _, path := range scriptImportPaths(code) {
			p := strings.TrimPrefix(path, "buzz:")
			if p == "magus" || strings.HasPrefix(p, "magus/") || strings.HasPrefix(p, "spells/") {
				return true
			}
			if _, native := sess.NativeModule(p); native {
				continue
			}
			if src, ok := sources[p]; ok {
				if !seen["source:"+p] {
					seen["source:"+p] = true
					if reaches(src, "") {
						return true
					}
				}
				continue
			}
			path := findScriptImport(p, dir, sess.IncludeDirs())
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			data, err := os.ReadFile(path)
			if err == nil && reaches(string(data), filepath.Dir(path)) {
				return true
			}
		}
		return false
	}
	return reaches(code, "")
}

// scriptImportPaths lists the paths code's import statements name, or none when code
// does not lex; the session reports that error itself.
func scriptImportPaths(code string) []string {
	toks, err := token.Tokenize(code)
	if err != nil {
		return nil
	}
	var paths []string
	for i, t := range toks {
		if t.Kind != token.Import {
			continue
		}
		for _, next := range toks[i+1:] {
			if next.Kind == token.String {
				paths = append(paths, next.Val)
				break
			}
			if next.Kind != token.Ident && next.Kind != token.Comma {
				break
			}
		}
	}
	return paths
}

// findScriptImport resolves p the way the session does for a file import: beside the
// importing file first, then each include dir.
func findScriptImport(p, dir string, includeDirs []string) string {
	dirs := includeDirs
	if dir != "" {
		dirs = append([]string{dir}, includeDirs...)
	}
	for _, d := range dirs {
		candidate := filepath.Join(d, p+".buzz")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}
