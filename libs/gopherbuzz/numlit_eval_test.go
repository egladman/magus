package buzz_test

import (
	"context"
	"strconv"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
)

// TestNumericLiteralEval covers the non-decimal integer literals and underscore
// separators the lexer accepts (matching upstream Buzz: 0x/0b prefixes and _
// separators, no 0o/exponent/uppercase). Each source snippet must evaluate to
// the expected int64.
func TestNumericLiteralEval(t *testing.T) {
	ctx := context.Background()
	cases := map[string]int64{
		"return 0x1a;":       26,
		"return 0xFF_FF;":    65535,
		"return 0b1010;":     10,
		"return 1_000_000;":  1000000,
		"return 0xDEADBEEF;": 3735928559,
	}
	for src, want := range cases {
		sess := buzz.NewSession(ctx, buzz.WithEmbedded())
		buzzstd.Register(sess)
		v, err := sess.Eval(ctx, src)
		if err != nil {
			t.Errorf("%q: eval err: %v", src, err)
			continue
		}
		if got := v.String(); got != strconv.FormatInt(want, 10) {
			t.Errorf("%q: got %s, want %d", src, got, want)
		}
	}
}
