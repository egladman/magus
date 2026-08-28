package journal

import (
	"context"
	"io"
	"os"
	"testing"
)

// BenchmarkEmitOutput measures the hot path: one output event through the capture logger to
// a file handler (the JSONL run log), the per-subprocess-line cost.
func BenchmarkEmitOutput(b *testing.B) {
	ctx := WithInvocationID(WithLogger(context.Background(), NewLogger(NewFileHandler(io.Discard))), "invbench")
	ev := Event{Kind: KindOutput, Stream: StreamStdout, Project: "web", Target: "build", Text: "some subprocess output line here"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Emit(ctx, ev)
	}
}

// BenchmarkEmitResult measures the per-target cost: one result event through the capture
// logger to the file handler. It is the counterpart to BenchmarkEmitOutput and exists
// because [FileHandler.Handle] treats the two differently - result events flush so a live
// follower sees them, output events stay buffered. Without this benchmark the flush is an
// unmeasured claim on a path that runs once per target.
func BenchmarkEmitResult(b *testing.B) {
	ctx := WithInvocationID(WithLogger(context.Background(), NewLogger(NewFileHandler(io.Discard))), "invbench")
	ev := Event{Kind: KindResult, Project: "web", Target: "build", Status: StatusPass, Ref: "outa1b2c3d4e5f6", DurMs: 1200}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Emit(ctx, ev)
	}
}

// BenchmarkEmitResultFile is BenchmarkEmitResult against a REAL file rather than
// io.Discard, because that is the difference the flush policy turns on.
//
// Against io.Discard a flush is bufio bookkeeping and no syscall, so the discard
// benchmarks understate what [FileHandler.Handle] costs in production, where the run log
// is a file on disk. This one includes the write(2). Keep both: the discard pair isolates
// the CPU cost of building an event, this one prices the flush.
func BenchmarkEmitResultFile(b *testing.B) {
	f, err := os.CreateTemp(b.TempDir(), "run-*.jsonl")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	ctx := WithInvocationID(WithLogger(context.Background(), NewLogger(NewFileHandler(f))), "invbench")
	ev := Event{Kind: KindResult, Project: "web", Target: "build", Status: StatusPass, Ref: "outa1b2c3d4e5f6", DurMs: 1200}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Emit(ctx, ev)
	}
}
