package bindings

import (
	"context"
	"fmt"
	"strings"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

// These benchmarks answer one question: for the same computation, is it faster as
// a Go host method reached across the binding boundary, or as Buzz source running
// on the VM?
//
// collapseWs is the comparison because BOTH already exist and agree on semantics -
// std.StringsCollapseWs (Go, via strings.Fields) and the hand-rolled version in
// docs/lib/text.buzz. Nothing had to be written twice to measure it.
//
// The shape to read for, rather than a single number: a host call pays a FIXED
// marshalling cost per call and then runs the body at Go speed, while a Buzz
// implementation pays nothing at a boundary and runs the body on the VM. So the
// answer has to move with input size, and one measurement at one size would be
// the misleading version.
//
// callsPerExec is large enough that parse+compile of the surrounding chunk is
// noise rather than the thing being timed.
const callsPerExec = 20000

// buzzCollapseWs is docs/lib/text.buzz's implementation, verbatim in behaviour:
// fold every whitespace run to one space and trim.
const buzzCollapseWs = `
fun collapseWsBuzz(s: str) > str {
    final flat = s.split("\n").join(" ").split("\t").join(" ").split("\r").join(" ");
    var parts = mut [];
    foreach (p in flat.split(" ")) {
        if (p != "") { parts.append(p); }
    }
    return parts.join(" ").trim();
}
`

// benchInput builds an input with whitespace runs to collapse. words controls the
// size; the shape (a run of spaces between each word) is what both
// implementations actually walk.
func benchInput(words int) string {
	var b strings.Builder
	for i := 0; i < words; i++ {
		b.WriteString("word   ")
	}
	return b.String()
}

// runCollapseBench executes body callsPerExec times per iteration and reports the
// per-CALL cost, so host and Buzz numbers are directly comparable.
func runCollapseBench(b *testing.B, prelude, call string, words int) {
	b.Helper()
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	b.Cleanup(func() { _ = sess.Close() })
	RegisterModuleSurface(ctx, sess)

	src := fmt.Sprintf(`
import "strings";
%s
final input = "%s";
var sink = "";
var i = 0;
while (i < %d) {
    sink = %s;
    i = i + 1;
}
`, prelude, benchInput(words), callsPerExec, call)

	// Compile and run once outside the timer: a failure here is a broken
	// benchmark, not a slow one, and it must not be reported as a result.
	if err := sess.Exec(ctx, src); err != nil {
		b.Fatalf("warmup: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := sess.Exec(ctx, src); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*callsPerExec), "ns/call")
}

func BenchmarkCollapseWsHostShort(b *testing.B) {
	runCollapseBench(b, "", `strings\collapseWs(input)`, 3)
}

func BenchmarkCollapseWsBuzzShort(b *testing.B) {
	runCollapseBench(b, buzzCollapseWs, `collapseWsBuzz(input)`, 3)
}

func BenchmarkCollapseWsHostMedium(b *testing.B) {
	runCollapseBench(b, "", `strings\collapseWs(input)`, 50)
}

func BenchmarkCollapseWsBuzzMedium(b *testing.B) {
	runCollapseBench(b, buzzCollapseWs, `collapseWsBuzz(input)`, 50)
}

func BenchmarkCollapseWsHostLong(b *testing.B) {
	runCollapseBench(b, "", `strings\collapseWs(input)`, 400)
}

func BenchmarkCollapseWsBuzzLong(b *testing.B) {
	runCollapseBench(b, buzzCollapseWs, `collapseWsBuzz(input)`, 400)
}

// The boundary cost with the body removed: strings\upperFirst on a 1-character
// string is about the cheapest host method there is, so what this measures is
// almost entirely the cost of crossing into Go and back. Its Buzz counterpart
// does the same trivial work with no crossing at all.
func BenchmarkBoundaryOnlyHost(b *testing.B) {
	runCollapseBench(b, "", `strings\upperFirst("a")`, 1)
}

func BenchmarkBoundaryOnlyBuzz(b *testing.B) {
	runCollapseBench(b, `
fun upperFirstBuzz(s: str) > str {
    if (s == "") { return s; }
    return s.sub(0, len: 1).upper() + s.sub(1);
}
`, `upperFirstBuzz("a")`, 1)
}
