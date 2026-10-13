package postag

import "testing"

func TestTagUPOSString(t *testing.T) {
	for u := ADJ; u <= X; u++ {
		got, ok := parseUPOS(u.String())
		if !ok || got != u {
			t.Errorf("parseUPOS(%q) = %v, %v; want %v", u.String(), got, ok, u)
		}
	}
	if got := UPOS(0).String(); got != "UPOS(0)" {
		t.Errorf("UPOS(0).String() = %q", got)
	}
	if got := UPOS(18).String(); got != "UPOS(18)" {
		t.Errorf("UPOS(18).String() = %q", got)
	}
}
