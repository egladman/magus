package buzz_test

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
)

// TestConformance runs all .bzz files in testdata/.
// Each file may have header comments:
//
//	// @expect: <value>  — run and assert __r.String() == <value>
//	// @error: <substr>  — assert parse/type/compile/runtime error contains <substr>
//	// @skip: <reason>   — skip this test case
func TestConformance(t *testing.T) {
	files, err := filepath.Glob("testdata/*.bzz")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no conformance test files found in testdata/")
	}
	for _, path := range files {
		path := path
		name := strings.TrimSuffix(filepath.Base(path), ".bzz")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			meta := parseConformanceMeta(string(src))
			if meta.skip != "" {
				t.Skipf("skip: %s", meta.skip)
			}
			runConformanceCase(t, name, string(src), meta)
		})
	}
}

type conformanceMeta struct {
	expect string
	errStr string
	skip   string
}

func parseConformanceMeta(src string) conformanceMeta {
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

func runConformanceCase(t *testing.T, name, src string, meta conformanceMeta) {
	t.Helper()
	sess := buzz.NewSession(context.Background())
	defer func() { _ = sess.Close() }()
	buzzstd.Register(sess)
	err := sess.Exec(context.Background(), src)

	if meta.errStr != "" {
		if err == nil {
			t.Fatalf("%s: expected error containing %q, got none", name, meta.errStr)
		}
		if !strings.Contains(err.Error(), meta.errStr) {
			t.Fatalf("%s: error %q does not contain %q", name, err.Error(), meta.errStr)
		}
		return
	}

	if err != nil {
		t.Fatalf("%s: unexpected error: %v", name, err)
	}

	if meta.expect != "" {
		got := sess.GetGlobal("__r")
		if got.String() != meta.expect {
			t.Errorf("%s: __r = %q, want %q", name, got.String(), meta.expect)
		}
	}
}
