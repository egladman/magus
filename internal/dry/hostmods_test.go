package dry_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/dry"
)

func TestEval_HostModules(t *testing.T) {
	cases := map[string]string{
		`import "strings"; return strings.camelCase("hello world");`: "helloWorld",
		`import "encoding"; return encoding.base64Encode("hi");`:     "aGk=",
	}
	for src, want := range cases {
		r := dry.Eval(context.Background(), src)
		if !r.OK {
			t.Errorf("%q: eval failed: %+v", src, r.Diag)
			continue
		}
		got := r.Result
		// Result strings may be wrapped in quotes; trim.
		if len(got) >= 2 && got[0] == '"' && got[len(got)-1] == '"' {
			got = got[1 : len(got)-1]
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", src, got, want)
		}
	}
}
