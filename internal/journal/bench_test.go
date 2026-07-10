package journal

import (
	"context"
	"io"
	"testing"
)

// BenchmarkEmitOutput measures the hot path: one output event through the capture logger to
// a file handler (the JSONL run log), the per-subprocess-line cost.
func BenchmarkEmitOutput(b *testing.B) {
	ctx := WithInvocation(WithLogger(context.Background(), NewLogger(NewFileHandler(io.Discard))), "invbench")
	ev := Event{Kind: KindOutput, Stream: StreamStdout, Project: "web", Target: "build", Text: "some subprocess output line here"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Emit(ctx, ev)
	}
}
