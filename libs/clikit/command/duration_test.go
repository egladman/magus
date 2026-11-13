package command

import (
	"flag"
	"strings"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in, unit string
		want     time.Duration
		err      string
	}{
		{"5m", "ms", 5 * time.Minute, ""},
		{"420ms", "s", 420 * time.Millisecond, ""},
		{"1h30m", "", 90 * time.Minute, ""},
		{"420", "ms", 420 * time.Millisecond, ""},
		{"15", "s", 15 * time.Second, ""},
		{"2", "h", 2 * time.Hour, ""},
		{"", "ms", 0, "empty duration"},
		{"xyz", "ms", 0, "invalid duration"},
		{"420", "", 0, "has no unit"},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in, c.unit)
		if c.err != "" {
			if err == nil {
				t.Fatalf("ParseDuration(%q, %q) = %v, want error containing %q", c.in, c.unit, got, c.err)
			}
			if !strings.Contains(err.Error(), c.err) {
				t.Fatalf("ParseDuration(%q, %q) error %q, want containing %q", c.in, c.unit, err.Error(), c.err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseDuration(%q, %q): %v", c.in, c.unit, err)
		}
		if got != c.want {
			t.Fatalf("ParseDuration(%q, %q) = %v, want %v", c.in, c.unit, got, c.want)
		}
	}
}

func TestDurationUsageNamesTheBareUnit(t *testing.T) {
	f := Flag{Name: "duration", Kind: Duration, Unit: "ms", Doc: "How long the tool ran"}
	got := DurationUsage(f)
	want := "How long the tool ran. Bare numbers are milliseconds."
	if got != want {
		t.Fatalf("DurationUsage = %q, want %q", got, want)
	}
	already := Flag{Name: "x", Kind: Duration, Unit: "ms", Doc: "Quiet window. Bare numbers are milliseconds."}
	if DurationUsage(already) != already.Doc {
		t.Fatalf("DurationUsage rewrote a Doc that already named bare numbers: %q", DurationUsage(already))
	}
}

func TestDurationVarAcceptsBareNumber(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var d time.Duration
	DurationVar(fs, &d, "duration", 0, "ms", DurationUsage(Flag{Kind: Duration, Unit: "ms", Doc: "elapsed"}))
	if err := fs.Parse([]string{"--duration", "420"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d != 420*time.Millisecond {
		t.Fatalf("got %v, want 420ms", d)
	}
}

func TestCheckRequiresUnitOnDuration(t *testing.T) {
	err := Check(Command{Name: "tool", Flags: []Flag{
		{Name: "timeout", Kind: Duration, Doc: "give up"},
	}})
	if err == nil || !strings.Contains(err.Error(), "no Unit") {
		t.Fatalf("Check = %v, want error naming no Unit", err)
	}
	err = Check(Command{Name: "tool", Flags: []Flag{
		{Name: "timeout", Kind: Duration, Unit: "m", Doc: "give up"},
		{Name: "t", Kind: Duration, AliasOf: "timeout", Doc: "short"},
	}})
	if err != nil {
		t.Fatalf("Check with Unit and alias: %v", err)
	}
}
