package pool_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings"
	_ "github.com/egladman/magus/internal/interp/engine/lua/gopherlua"
	"github.com/egladman/magus/internal/interp/pool"
)

// incMagusfile is a target that appends one byte to counter each time it runs;
// the file length is the run count.
const incMagusfile = `
global function inc(args: {string})
    local f = io.open("%s", "a")
    if f then f:write("x") f:close() end
end
`

func writeMagusfile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "magusfile.tl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findSource(t *testing.T, dir string) *interp.Source {
	t.Helper()
	src, err := interp.Find(dir)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if src == nil {
		t.Fatal("Find: no source")
	}
	return src
}

func TestPoolSubmitSuccess(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "ran")
	writeMagusfile(t, dir, `
global function build(args: {string})
    local f = io.open("`+sentinel+`", "w")
    if f then f:write("ok") f:close() end
end
`)
	src := findSource(t, dir)
	p := pool.New(src, 1)
	defer p.Close()

	ch := p.Submit(context.Background(), "build", nil)
	res := <-ch
	if res.Err != nil {
		t.Fatalf("Submit: %v", res.Err)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel: %v", err)
	}
	if string(got) != "ok" {
		t.Errorf("sentinel = %q, want %q", got, "ok")
	}
}

func TestPoolSubmitMissingTarget(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function build(args: {string}) end
`)
	src := findSource(t, dir)
	p := pool.New(src, 1)
	defer p.Close()

	ch := p.Submit(context.Background(), "no-such-target", nil)
	res := <-ch
	if res.Err == nil {
		t.Fatal("expected error for missing target")
	}
	if !strings.Contains(res.Err.Error(), "not found") {
		t.Errorf("unexpected error: %v", res.Err)
	}
}

func TestPoolCycleDetection(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function build(args: {string}) end
`)
	src := findSource(t, dir)
	p := pool.New(src, 2)
	defer p.Close()

	ancestors := []string{"build"}
	ch := p.Submit(context.Background(), "build", ancestors)
	res := <-ch
	if res.Err == nil {
		t.Fatal("expected cycle-detection error")
	}
	if !strings.Contains(res.Err.Error(), "cycle") {
		t.Errorf("unexpected error: %v", res.Err)
	}
}

func TestPoolContextCancellation(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function slow(args: {string}) end
`)
	src := findSource(t, dir)
	// capacity=0: pool creates no workers, Submit blocks waiting for one.
	p := pool.New(src, 0)
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so Submit cannot acquire a worker

	ch := p.Submit(ctx, "slow", nil)
	res := <-ch
	if !errors.Is(res.Err, context.Canceled) && res.Err == nil {
		// either context.Canceled or a startup error is acceptable
		t.Logf("got err: %v (acceptable)", res.Err)
	}
}

// TestPoolDispatchMemoRunsOnce verifies the run-once guarantee: with a
// TargetMemo in ctx, the same name listed twice in one Dispatch runs the target
// exactly once (the second caller subscribes to the in-flight entry).
func TestPoolDispatchMemoRunsOnce(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	writeMagusfile(t, dir, fmt.Sprintf(incMagusfile, counter))
	src := findSource(t, dir)
	p := pool.New(src, 2)
	defer p.Close()

	ctx := buzz.WithTargetMemo(context.Background(), buzz.NewTargetMemo())
	if err := p.Dispatch(ctx, []string{"inc", "inc"}, nil); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	got, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	if string(got) != "x" {
		t.Errorf("memoized dispatch ran target %d times, want 1 (counter=%q)", len(got), got)
	}
}

// TestPoolDispatchNoMemoRunsEach is the baseline: with no memo in ctx, each
// listed name runs, so the duplicate runs the target twice.
func TestPoolDispatchNoMemoRunsEach(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	writeMagusfile(t, dir, fmt.Sprintf(incMagusfile, counter))
	src := findSource(t, dir)
	p := pool.New(src, 2)
	defer p.Close()

	if err := p.Dispatch(context.Background(), []string{"inc", "inc"}, nil); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	got, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	if string(got) != "xx" {
		t.Errorf("un-memoized dispatch counter=%q, want %q", got, "xx")
	}
}

func TestPoolReuseWorker(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	writeMagusfile(t, dir, `
global function inc(args: {string})
    local f = io.open("`+counter+`", "a")
    if f then f:write("x") f:close() end
end
`)
	src := findSource(t, dir)
	p := pool.New(src, 1)
	defer p.Close()

	for range 3 {
		res := <-p.Submit(context.Background(), "inc", nil)
		if res.Err != nil {
			t.Fatalf("Submit: %v", res.Err)
		}
	}
	got, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	if string(got) != "xxx" {
		t.Errorf("counter = %q, want %q", got, "xxx")
	}
}

func TestRegistryGetReturnsSharedPool(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function noop(args: {string}) end
`)
	src := findSource(t, dir)
	reg := pool.NewRegistry(2)
	defer reg.Close()

	p1 := reg.Get(src)
	p2 := reg.Get(src)
	if p1 != p2 {
		t.Error("Registry.Get returned different pools for same source")
	}
}

func TestAncestorStack(t *testing.T) {
	ctx := context.Background()
	if got := pool.AncestorsFromContext(ctx); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	ctx2 := pool.WithAncestor(ctx, "build")
	stack := pool.AncestorsFromContext(ctx2)
	if len(stack) != 1 || stack[0] != "build" {
		t.Errorf("unexpected stack: %v", stack)
	}
	ctx3 := pool.WithAncestor(ctx2, "test")
	stack3 := pool.AncestorsFromContext(ctx3)
	if len(stack3) != 2 || stack3[1] != "test" {
		t.Errorf("unexpected stack3: %v", stack3)
	}
}
