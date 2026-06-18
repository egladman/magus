// Command ffi-c is a runnable demonstration of gopherbuzz's C FFI. It compiles
// the bundled mathx.c into a shared library, then runs demo.buzz against it —
// exercising a scalar call, pointer out-parameters, and a callback.
//
//	cd examples/ffi-c && go run .
//
// Requires a C compiler (cc/clang/gcc) on PATH and a purego-supported platform.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if buzz.GetFFIProvider() == nil {
		return fmt.Errorf("FFI is unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	libPath, err := compileLib(dir)
	if err != nil {
		return err
	}

	src, err := os.ReadFile(filepath.Join(dir, "demo.buzz"))
	if err != nil {
		return err
	}
	// zdef takes a literal path; splice in the freshly compiled library.
	program := strings.ReplaceAll(string(src), "__LIBPATH__", libPath)

	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	buzzstd.Register(sess)
	return sess.Exec(context.Background(), program)
}

// compileLib builds mathx.c into a shared library next to it and returns the path.
func compileLib(dir string) (string, error) {
	cc := ""
	for _, c := range []string{"cc", "clang", "gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		return "", fmt.Errorf("no C compiler (cc/clang/gcc) on PATH")
	}
	ext := "so"
	if runtime.GOOS == "darwin" {
		ext = "dylib"
	}
	out := filepath.Join(dir, "libmathx."+ext)
	cmd := exec.Command(cc, "-shared", "-fPIC", "-o", out, filepath.Join(dir, "clib", "mathx.c"))
	if msg, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("compiling mathx.c: %w\n%s", err, msg)
	}
	return out, nil
}
