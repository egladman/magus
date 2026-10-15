package wordclass

import "testing"

func TestClassString(t *testing.T) {
	for c := Adjective; c <= Other; c++ {
		got, ok := parseClass(c.String())
		if !ok || got != c {
			t.Errorf("parseClass(%q) = %v, %v; want %v", c.String(), got, ok, c)
		}
	}
	if got := Class(0).String(); got != "Class(0)" {
		t.Errorf("Class(0).String() = %q", got)
	}
	if got := Class(18).String(); got != "Class(18)" {
		t.Errorf("Class(18).String() = %q", got)
	}
}
