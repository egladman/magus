package exit

import (
	"errors"
	"flag"
	"fmt"
	"testing"
)

type busy struct{}

func (busy) Error() string { return "busy" }
func (busy) ExitCode() int { return TempFail }

func TestCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, OK},
		{"help", fmt.Errorf("parse: %w", flag.ErrHelp), OK},
		{"plain", errors.New("boom"), Failure},
		{"usage", Usagef("unknown target %q", "x"), Usage},
		{"wrapped usage", fmt.Errorf("run: %w", Usagef("no target")), Usage},
		{"coded", busy{}, TempFail},
		{"reported", Reported(Failure), Failure},
		// A broken build is not a scheduling problem because a peer was busy too.
		{"reported wins", errors.Join(busy{}, Reported(Failure)), Failure},
	}
	for _, c := range cases {
		if got := Code(c.err); got != c.want {
			t.Errorf("%s: Code = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestAlreadyReported(t *testing.T) {
	if !AlreadyReported(fmt.Errorf("wrapped: %w", Reported(Usage))) {
		t.Error("a wrapped Reported failure is not reported")
	}
	if AlreadyReported(Usagef("misuse")) || AlreadyReported(nil) {
		t.Error("a misuse or nil reads as already reported")
	}
}

func TestUsagefFormats(t *testing.T) {
	if got := Usagef("want %d args", 2).Error(); got != "want 2 args" {
		t.Errorf("Error() = %q", got)
	}
}
