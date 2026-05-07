package gopherlua

import (
	"fmt"
	"strings"
	"testing"
)

func TestRewriteSteps_lineHooks(t *testing.T) {
	src := `local x = 1
local y = 2
local z = x + y`

	res, err := rewriteSteps(src)
	if err != nil {
		t.Fatalf("rewriteSteps: %v", err)
	}

	lines := strings.Split(res.Rewritten, "\n")
	wantHooks := []string{
		`__magus_step_hook(1, "line")`,
		`__magus_step_hook(2, "line")`,
		`__magus_step_hook(3, "line")`,
	}
	for _, want := range wantHooks {
		found := false
		for _, l := range lines {
			if strings.TrimSpace(l) == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected hook %q in rewritten source\ngot:\n%s", want, res.Rewritten)
		}
	}
	if !strings.Contains(res.Rewritten, "local x = 1") {
		t.Error("original statement missing from rewritten source")
	}
}

func TestRewriteSteps_returnHook(t *testing.T) {
	src := `function foo()
    return 42
end`

	res, err := rewriteSteps(src)
	if err != nil {
		t.Fatalf("rewriteSteps: %v", err)
	}

	if !strings.Contains(res.Rewritten, `__magus_step_hook(2, "return")`) {
		t.Errorf("missing return hook\ngot:\n%s", res.Rewritten)
	}
	if strings.Contains(res.Rewritten, `__magus_step_hook(3, "return")`) {
		t.Errorf("unexpected end-of-function hook after explicit return\ngot:\n%s", res.Rewritten)
	}
}

func TestRewriteSteps_implicitReturn(t *testing.T) {
	src := `function bar()
    local x = 1
end`

	res, err := rewriteSteps(src)
	if err != nil {
		t.Fatalf("rewriteSteps: %v", err)
	}

	if !strings.Contains(res.Rewritten, `__magus_step_hook(2, "line")`) {
		t.Errorf("missing line hook for inner statement\ngot:\n%s", res.Rewritten)
	}
	if !strings.Contains(res.Rewritten, `__magus_step_hook(3, "return")`) {
		t.Errorf("missing return hook at function end\ngot:\n%s", res.Rewritten)
	}
}

func TestRewriteSteps_nestedFunction(t *testing.T) {
	src := `local function outer()
    local function inner()
        local v = 1
    end
    inner()
end`

	res, err := rewriteSteps(src)
	if err != nil {
		t.Fatalf("rewriteSteps: %v", err)
	}

	if !strings.Contains(res.Rewritten, `__magus_step_hook(3, "line")`) {
		t.Errorf("missing hook for inner function body\ngot:\n%s", res.Rewritten)
	}
	if !strings.Contains(res.Rewritten, `__magus_step_hook(5, "line")`) {
		t.Errorf("missing hook for inner() call\ngot:\n%s", res.Rewritten)
	}
	if !strings.Contains(res.Rewritten, `__magus_step_hook(4, "return")`) {
		t.Errorf("missing return hook for inner end\ngot:\n%s", res.Rewritten)
	}
}

func TestRewriteSteps_ifStmt(t *testing.T) {
	src := `if x > 0 then
    local y = 1
else
    local z = 2
end`

	res, err := rewriteSteps(src)
	if err != nil {
		t.Fatalf("rewriteSteps: %v", err)
	}

	if !strings.Contains(res.Rewritten, `__magus_step_hook(1, "line")`) {
		t.Errorf("missing hook for if statement\ngot:\n%s", res.Rewritten)
	}
	if !strings.Contains(res.Rewritten, `__magus_step_hook(2, "line")`) {
		t.Errorf("missing hook for then-branch statement\ngot:\n%s", res.Rewritten)
	}
	if !strings.Contains(res.Rewritten, `__magus_step_hook(4, "line")`) {
		t.Errorf("missing hook for else-branch statement\ngot:\n%s", res.Rewritten)
	}
}

func TestRewriteSteps_parseError(t *testing.T) {
	src := `this is not valid lua ~~~`
	res, err := rewriteSteps(src)
	if err == nil {
		t.Error("expected parse error, got nil")
	}
	if res.Rewritten != src {
		t.Error("on parse error, Rewritten should equal original src")
	}
}

func TestRewriteSteps_noStatements(t *testing.T) {
	src := `-- just a comment`
	res, err := rewriteSteps(src)
	if err != nil {
		t.Fatalf("rewriteSteps: %v", err)
	}
	if res.Rewritten != src {
		t.Errorf("source without executable statements should be unchanged\ngot:\n%s", res.Rewritten)
	}
}

// genLuaSrc builds a synthetic Lua program with approximately n source lines.
func genLuaSrc(n int) string {
	var b strings.Builder
	b.Grow(n * 20)
	lineNum := 0
	for lineNum < n {
		fmt.Fprintf(&b, "local x%d = %d\n", lineNum, lineNum)
		lineNum++
	}
	return b.String()
}

func BenchmarkRewriteSteps(b *testing.B) {
	for _, sz := range []int{100, 500, 1000} {
		src := genLuaSrc(sz)
		b.Run(fmt.Sprintf("%dloc", sz), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				rewriteSteps(src) //nolint:errcheck
			}
		})
	}
}
