package textbuf

import "testing"

func TestAnchorKeysRelabel(t *testing.T) {
	b := newBuffer([]byte("x"))
	var as []anchor
	for range 200 {
		a, err := b.Anchor(0, left)
		if err != nil {
			t.Fatal(err)
		}
		as = append(as, a)
	}
	if err := b.Apply(edit{Text: "abc"}); err != nil {
		t.Fatal(err)
	}
	for _, a := range as {
		if got := b.Resolve(a); got != 0 {
			t.Fatalf("anchor at %d, want 0", got)
		}
	}
}
