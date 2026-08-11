package std

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// TestProbeBinsNameRealReplacements pins the advisory table rather than the warning
// itself: the warning is a sync.Once on stderr, so a test that captured it would pass or
// fail depending on whether an earlier test in this package had already tripped it. What
// is worth pinning is that each entry names a REAL alternative - an entry pointing back at
// another shell-out is advice that sends the reader in a circle, and nothing else catches
// that.
func TestProbeBinsNameRealReplacements(t *testing.T) {
	if _, ok := probeBins["test"]; !ok {
		t.Fatal("test(1) must be listed: it is the shell-out that cost the docs site ~113k forks a build")
	}
	for bin, instead := range probeBins {
		if bin == "" || instead == "" {
			t.Errorf("probeBins[%q] = %q: both sides must be non-empty", bin, instead)
		}
		if strings.Contains(instead, "os.execute") {
			t.Errorf("probeBins[%q] suggests %q, which is the thing being warned about", bin, instead)
		}
	}
}

// TestOsSleepCancellable verifies a cancelled context interrupts os.sleep
// instead of blocking for the full requested duration. Regression coverage
// for the pre-fix osSleep, which discarded ctx and always ran a plain
// time.Sleep - a cancelled run could not be interrupted until the full sleep
// elapsed.
func TestOsSleepCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := osSleep(ctx, []vm.Value{vm.FloatValue(2000)})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("osSleep: expected an error from a cancelled context, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("osSleep: took %s to return after cancellation; expected it to stop well short of the requested 2s", elapsed)
	}
}

// TestSocketReceiveNegativeN verifies a negative n is rejected as an argument
// error instead of reaching make([]byte, n), which panics for a negative size
// (surfacing as "buzz: internal error" rather than a catchable script error).
func TestSocketReceiveNegativeN(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	var (
		result vm.Value
		err    error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("socketReceive: panicked on negative n: %v", r)
			}
		}()
		result, err = socketReceive(client, []vm.Value{vm.IntValue(-1)})
	}()
	if err == nil {
		t.Fatalf("socketReceive: expected an argument error for n=-1, got result %v", result)
	}
}

// TestSocketReceiveNTooLarge verifies n above maxSocketReceiveSize is rejected
// as an argument error rather than driving an unbounded make([]byte, n).
func TestSocketReceiveNTooLarge(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	_, err := socketReceive(client, []vm.Value{vm.IntValue(maxSocketReceiveSize + 1)})
	if err == nil {
		t.Fatal("socketReceive: expected an argument error for n > maxSocketReceiveSize, got nil")
	}
}
