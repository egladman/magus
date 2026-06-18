package buzz_test

import (
	"bytes"
	"context"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// homepageExample is the canonical program from https://buzz-lang.dev/0.5.0/,
// verbatim, with a trailing `main(["10"]);` driver line so the test can run it
// (the language has no implicit main invocation from an embedded session). It
// exercises imports, namespace `\` access, fiber return-vs-yield types
// (`void *> int?`), `yield`, the discard `_`, optional subscript (`args[?0]`),
// `??`, force-unwrap (`!`), a half-open range foreach (`0..n`), foreach over a
// fiber instance (`&fibonacci(N)`), and string interpolation.
const homepageExample = `import "std";

fun fibonacci(n: int) > void *> int? {
    var n1 = 0;
    var n2 = 1;
    var next: int? = null;

    foreach (_ in 0..n) {
        _ = yield n1;
        next = n1 + n2;
        n1 = n2;
        n2 = next!;
    }
}

fun main(args: [str]) > void {
    final N = std\parseInt(args[?0] ?? "10")!;

    foreach (n in &fibonacci(N)) {
        std\print("{n}");
    }
}

main(["10"]);
`

// TestHomepageExample is the compliance anchor: the official 0.5.0 homepage
// program must compile and run, printing the first ten Fibonacci numbers.
func TestHomepageExample(t *testing.T) {
	var out bytes.Buffer
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	buzzstd.RegisterWithOutput(sess, &out)

	require.NoError(t, sess.Exec(context.Background(), homepageExample), "homepage example failed to run")

	const want = "0\n1\n1\n2\n3\n5\n8\n13\n21\n34\n"
	assert.Equal(t, want, out.String(), "fibonacci output")
}
