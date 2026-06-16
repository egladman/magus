package std

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
)

// TestConformance runs all .buzz files in testdata/, registering the Buzz std
// library so imports resolve. Each file may carry header directives:
//
//	// @expect: <value>  — run and assert __r.String() == <value>
//	// @error: <substr>  — assert error contains <substr>
//	// @skip: <reason>   — skip this test case
func TestConformance(t *testing.T) {
	files, err := filepath.Glob("testdata/*.buzz")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no conformance test files in testdata/")
	}
	for _, path := range files {
		path := path
		name := strings.TrimSuffix(filepath.Base(path), ".buzz")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			meta := parseMeta(string(src))
			if meta.skip != "" {
				t.Skipf("skip: %s", meta.skip)
			}
			runCase(t, string(src), meta)
		})
	}
}

// TestRegisterNoPanic verifies that Register does not panic on a fresh session.
func TestRegisterNoPanic(t *testing.T) {
	sess := buzz.NewSession(context.Background())
	defer func() { _ = sess.Close() }()
	Register(sess) // must not panic
}

// TestAllModulesImportable verifies that every standard module can be imported
// by name (no file-not-found error for synthetic modules).
func TestAllModulesImportable(t *testing.T) {
	for _, mod := range []string{"std", "math", "fs", "os", "crypto", "gc", "debug", "io", "serialize", "buffer", "ffi"} {
		mod := mod
		t.Run(mod, func(t *testing.T) {
			sess := buzz.NewSession(context.Background())
			defer func() { _ = sess.Close() }()
			Register(sess)
			src := fmt.Sprintf("import %q;", mod)
			if err := sess.Exec(context.Background(), src); err != nil {
				t.Errorf("import %q raised error: %v", mod, err)
			}
		})
	}
}

type conformanceMeta struct {
	expect string
	errStr string
	skip   string
}

func parseMeta(src string) conformanceMeta {
	var m conformanceMeta
	scanner := bufio.NewScanner(strings.NewReader(src))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "//") {
			break
		}
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "@expect:"); ok {
			m.expect = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(line, "@error:"); ok {
			m.errStr = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(line, "@skip:"); ok {
			m.skip = strings.TrimSpace(rest)
		}
	}
	return m
}

func runCase(t *testing.T, src string, meta conformanceMeta) {
	t.Helper()
	sess := buzz.NewSession(context.Background())
	defer func() { _ = sess.Close() }()
	Register(sess)

	err := sess.Exec(context.Background(), src)
	if meta.errStr != "" {
		if err == nil {
			t.Fatalf("expected error containing %q, got nil", meta.errStr)
		}
		if !strings.Contains(err.Error(), meta.errStr) {
			t.Fatalf("error %q does not contain %q", err.Error(), meta.errStr)
		}
		return
	}
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if meta.expect != "" {
		globals := sess.Globals()
		r, ok := globals["__r"]
		if !ok {
			t.Fatalf("__r not set; did the script assign it?")
		}
		if got := r.String(); got != meta.expect {
			t.Errorf("__r = %q, want %q", got, meta.expect)
		}
	}
}
