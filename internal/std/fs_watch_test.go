package std

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// watchCallback adapts a Go func(changed) (stop bool) to the host.Callback the
// fs.watch binding hands FsWatch.
type watchCallback struct {
	fn func(changed []string) bool
}

func (c watchCallback) Call(_ context.Context, args ...any) ([]any, error) {
	var changed []string
	if len(args) > 0 {
		changed, _ = args[0].([]string)
	}
	return []any{c.fn(changed)}, nil
}

func TestFsWatchRequiresPaths(t *testing.T) {
	t.Parallel()
	err := FsWatch(context.Background(), nil, watchCallback{fn: func([]string) bool { return true }})
	if err == nil {
		t.Fatal("FsWatch with no paths should error")
	}
}

// TestFsWatchFiresCallbackAndStops drives FsWatch end-to-end: a real file change
// must reach the callback with a non-empty change set, and returning true must
// make the blocking call return nil.
func TestFsWatchFiresCallbackAndStops(t *testing.T) {
	dir := t.TempDir()
	fired := make(chan []string, 1)
	cb := watchCallback{fn: func(changed []string) bool {
		select {
		case fired <- changed:
		default:
		}
		return true // stop after the first batch
	}}

	done := make(chan error, 1)
	go func() { done <- FsWatch(context.Background(), []string{dir}, cb) }()

	// Poke the tree until the callback fires, which sidesteps the watcher's
	// arm-up race. Space the writes wider than the 200ms debounce so a batch
	// actually settles between them (continuous writes would keep resetting it).
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.go", i)), []byte("package p\n"), 0o644)
			time.Sleep(500 * time.Millisecond)
		}
	}()

	select {
	case changed := <-fired:
		if len(changed) == 0 {
			t.Error("callback received an empty change set")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the watch callback")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("FsWatch returned %v, want nil after the callback asked to stop", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FsWatch did not return after the callback asked to stop")
	}
}

func TestCallbackTruthy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ret  []any
		want bool
	}{
		{nil, false},
		{[]any{nil}, false},
		{[]any{false}, false},
		{[]any{true}, true},
		{[]any{"non-bool is truthy"}, true},
	}
	for _, tc := range cases {
		if got := callbackTruthy(tc.ret); got != tc.want {
			t.Errorf("callbackTruthy(%v) = %v, want %v", tc.ret, got, tc.want)
		}
	}
}

func TestRelToCwd(t *testing.T) {
	t.Parallel()
	base := filepath.FromSlash("/a/b")
	got := relToCwd(base, []string{
		filepath.FromSlash("/a/b/sub/x.go"),
		filepath.FromSlash("/a/b/y.go"),
	})
	want := []string{filepath.FromSlash("sub/x.go"), "y.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("relToCwd = %v, want %v", got, want)
	}
}
