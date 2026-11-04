package textbuf

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"
)

func TestApplyDiffMatchesModel(t *testing.T) {
	for seed := range uint64(200) {
		r := rand.New(rand.NewPCG(seed, 7))
		lines := make([]string, r.IntN(60))
		for i := range lines {
			lines[i] = randText(r, r.IntN(30)) + "\n"
		}
		old := strings.Join(lines, "")
		for i := range lines {
			switch r.IntN(8) {
			case 0:
				lines[i] = ""
			case 1:
				lines[i] = "  " + lines[i]
			case 2:
				lines[i] += randText(r, r.IntN(20)) + "\n"
			}
		}
		next := strings.Join(lines, "")
		b := newBuffer([]byte(old))
		// Lines wholly before the first differing byte, or wholly after the last,
		// lie outside every hunk.
		pre := commonPrefix([]byte(old), []byte(next))
		firstLine := strings.LastIndexByte(old[:pre], '\n') + 1
		suf := min(commonSuffix([]byte(old), []byte(next)), len(old)-pre, len(next)-pre)
		lastLine := len(old)
		if i := strings.IndexByte(old[len(old)-suf:], '\n'); i >= 0 {
			lastLine = len(old) - suf + i + 1
		}
		var before, after []anchor
		for range 20 {
			off := r.IntN(len(old) + 1)
			bias := bias(r.IntN(2))
			a, _ := b.Anchor(off, bias)
			switch {
			case off < firstLine:
				before = append(before, a)
			case off > lastLine:
				after = append(after, a)
			}
		}
		snap := b.Snapshot()
		want := make(map[anchor]int)
		for _, a := range before {
			want[a] = b.Resolve(a)
		}
		for _, a := range after {
			want[a] = b.Resolve(a) + len(next) - len(old)
		}
		b.ApplyDiff([]byte(next))
		if b.String() != next {
			t.Fatalf("seed %d: ApplyDiff text\n got %q\nwant %q", seed, b.String(), next)
		}
		if snap.String() != old {
			t.Fatalf("seed %d: snapshot changed", seed)
		}
		for a, p := range want {
			if got := b.Resolve(a); got != p {
				t.Fatalf("seed %d: anchor outside the change moved: %d, want %d", seed, got, p)
			}
		}
	}
}

func TestApplyDiffKeepsAnchorsBetweenHunks(t *testing.T) {
	old := "func a() {\nreturn 1\n}\n\nfunc keep() {}\n\nfunc b() {\nreturn 2\n}\n"
	next := "func a() {\n\treturn 1\n}\n\nfunc keep() {}\n\nfunc b() {\n\treturn 2\n}\n"
	b := newBuffer([]byte(old))
	at := strings.Index(old, "keep")
	a, _ := b.Anchor(at, left)
	inner, _ := b.Anchor(strings.Index(old, "1\n"), right)
	b.ApplyDiff([]byte(next))
	if got, want := b.Resolve(a), strings.Index(next, "keep"); got != want {
		t.Fatalf("anchor between hunks at %d, want %d", got, want)
	}
	if got, want := b.Resolve(inner), strings.Index(next, "1\n"); got != want {
		t.Fatalf("anchor inside a trimmed hunk at %d, want %d", got, want)
	}
}

// reformat changes the indentation of every 40th line, the shape of a formatter run
// over a mostly formatted file.
func reformat(src []byte) []byte {
	lines := bytes.SplitAfter(src, nl)
	for i := 0; i < len(lines); i += 40 {
		lines[i] = append([]byte("    "), bytes.TrimLeft(lines[i], "\t")...)
	}
	return bytes.Join(lines, nil)
}

// BenchmarkFormatterDiff applies a formatter's output and then the original back, two
// diffs per iteration.
func BenchmarkFormatterDiff(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(sizeName(size), func(b *testing.B) {
			buf, _ := benchBuffer(b, size, benchAnchors)
			src := buf.Bytes()
			formatted := reformat(src)
			b.ReportAllocs()
			for b.Loop() {
				buf.ApplyDiff(formatted)
				buf.ApplyDiff(src)
			}
		})
	}
}
