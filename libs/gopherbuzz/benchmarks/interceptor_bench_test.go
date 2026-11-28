package benchmarks

import (
	"context"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// interceptedCalls is how many native calls one run of the loop makes.
const interceptedCalls = 10_000

const callLoop = `
fun loop() > int {
    var sum = 0;
    var i = 0;
    while (i < 10000) {
        sum = sum + nat(i);
        i = i + 1;
    }
    return sum;
}
`

type passThrough struct{}

func (passThrough) InterceptCall(ctx context.Context, _ string, args []vm.Value, call vm.Callable) (vm.Value, error) {
	return call(ctx, args)
}

// benchNativeCalls reports ns per native call over a loop of interceptedCalls. The
// loop is compiled once, so each iteration is calls and the loop's own arithmetic.
func benchNativeCalls(b *testing.B, opts ...buzz.Option) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, append([]buzz.Option{buzz.WithEmbedded()}, opts...)...)
	b.Cleanup(func() { _ = sess.Close() })
	sess.SetGlobal("nat", vm.DirectValue("nat", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		return args[0], nil
	}))
	if err := sess.Exec(ctx, callLoop); err != nil {
		b.Fatal(err)
	}
	loop := sess.GetGlobal("loop")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := sess.CallValue(ctx, loop, nil); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*interceptedCalls), "ns/call")
}

// BenchmarkCallInterceptorNone is the seam with nothing installed: one nil check per
// native call.
func BenchmarkCallInterceptorNone(b *testing.B) { benchNativeCalls(b) }

// BenchmarkCallInterceptorPassThrough is the seam's dispatch cost with an
// interceptor that only runs the call.
func BenchmarkCallInterceptorPassThrough(b *testing.B) {
	benchNativeCalls(b, buzz.WithCallInterceptor(passThrough{}))
}
