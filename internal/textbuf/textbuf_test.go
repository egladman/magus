package textbuf

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// model is the naive reference: the text as a byte slice and each anchor's offset.
type model struct {
	text    []byte
	anchors map[Anchor]int
}

func (m *model) replace(s, e int, text string) {
	m.text = slices.Concat(m.text[:s], []byte(text), m.text[e:])
	for a, p := range m.anchors {
		switch {
		case p < s || p == s && a.bias() == Left:
		case p <= e && a.bias() == Left:
			p = s
		case p <= e:
			p = s + len(text)
		default:
			p += len(text) - (e - s)
		}
		m.anchors[a] = p
	}
}

func (m *model) point(off int) Point {
	before := m.text[:off]
	line := bytes.Count(before, nl)
	return Point{line, off - (bytes.LastIndexByte(before, '\n') + 1)}
}

const alphabet = "ab\nc\xc3\xa9de\n\nfgh"

func randText(r *rand.Rand, n int) string {
	var b strings.Builder
	for range n {
		if r.IntN(64) == 0 {
			b.WriteString("\xc3\xa9")
			continue
		}
		b.WriteByte(alphabet[r.IntN(len(alphabet))])
	}
	return b.String()
}

// randLen favors short edits but reaches past several chunks and nodes.
func randLen(r *rand.Rand) int {
	switch r.IntN(10) {
	case 0:
		return r.IntN(5000)
	case 1, 2:
		return r.IntN(300)
	default:
		return r.IntN(8)
	}
}

// validate checks the B+ tree shape: every leaf at height 0, every slot's inline
// summary and count equal to its node's, and every node but the root at least half full.
func validate[T any, S interface {
	summary[S, T]
	comparable
}](t *testing.T, tr *tree[T, S]) {
	t.Helper()
	if tr.root == nil {
		if tr.count != 0 {
			t.Fatalf("empty tree counts %d items", tr.count)
		}
		return
	}
	var walk func(n *node[T, S], root bool)
	walk = func(n *node[T, S], root bool) {
		if !root && n.len() < minItems || n.len() > maxItems || n.len() == 0 {
			t.Fatalf("node at height %d holds %d entries", n.height, n.len())
		}
		for _, k := range n.kids {
			if k.node.height != n.height-1 {
				t.Fatalf("child at height %d under height %d", k.node.height, n.height)
			}
			if c := k.node.child(); c.sum != k.sum || c.count != k.count {
				t.Fatalf("stale slot: %+v, %d; node says %+v, %d", k.sum, k.count, c.sum, c.count)
			}
			walk(k.node, false)
		}
	}
	walk(tr.root, true)
	if c := tr.root.child(); c.sum != tr.sum || c.count != tr.count {
		t.Fatalf("stale root summary")
	}
}

func check(t *testing.T, b *Buffer, m *model) {
	t.Helper()
	validate(t, &b.t)
	validate(t, &b.anchors[Left].t)
	validate(t, &b.anchors[Right].t)
	if got := b.Bytes(); !bytes.Equal(got, m.text) {
		t.Fatalf("text mismatch:\n got %q\nwant %q", got, m.text)
	}
	if b.String() != string(m.text) {
		t.Fatal("String differs from Bytes")
	}
	if b.Lines() != bytes.Count(m.text, nl)+1 {
		t.Fatalf("Lines() = %d, want %d", b.Lines(), bytes.Count(m.text, nl)+1)
	}
	for a, want := range m.anchors {
		if got := b.Resolve(a); got != want {
			t.Fatalf("anchor %#x (bias %d) at %d, want %d", uint32(a), a.bias(), got, want)
		}
	}
}

func checkPoints(t *testing.T, r *rand.Rand, b *Buffer, m *model) {
	t.Helper()
	for range 8 {
		off := r.IntN(len(m.text) + 1)
		want := m.point(off)
		p, err := b.Point(off)
		if err != nil || p != want {
			t.Fatalf("Point(%d) = %v, %v; want %v", off, p, err, want)
		}
		got, err := b.Offset(want)
		if err != nil || got != off {
			t.Fatalf("Offset(%v) = %d, %v; want %d", want, got, err, off)
		}
		s, e := min(off, len(m.text)-off), max(off, len(m.text)-off)
		sl, err := b.Slice(s, e)
		if err != nil || !bytes.Equal(sl, m.text[s:e]) {
			t.Fatalf("Slice(%d, %d) = %q, %v", s, e, sl, err)
		}
	}
	end := m.point(len(m.text))
	if _, err := b.Offset(Point{end.Line, end.Column + 1}); !errors.Is(err, ErrRange) {
		t.Fatalf("Offset past the end: %v", err)
	}
	if _, err := b.Offset(Point{end.Line + 1, 0}); !errors.Is(err, ErrRange) {
		t.Fatalf("Offset past the last line: %v", err)
	}
}

func TestBufferMatchesModel(t *testing.T) {
	for seed := range uint64(200) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
			size := randLen(r) * 3
			if seed%10 == 0 {
				size = 100 << 10 // deep enough for three levels of chunks
			}
			init := randText(r, size)
			b := New([]byte(init))
			m := &model{text: []byte(init), anchors: map[Anchor]int{}}
			type frozen struct {
				s    Snapshot
				text string
			}
			var snaps []frozen
			for range 150 {
				switch op := r.IntN(10); {
				case op < 5:
					applyRandom(t, r, b, m)
				case op < 7:
					for range 1 + r.IntN(24) {
						off := r.IntN(len(m.text) + 1)
						a, err := b.Anchor(off, Bias(r.IntN(2)))
						if err != nil {
							t.Fatal(err)
						}
						if _, dup := m.anchors[a]; dup {
							t.Fatalf("handle %#x handed out twice", uint32(a))
						}
						m.anchors[a] = off
					}
				case op < 8 && len(m.anchors) > 0:
					for a := range m.anchors {
						b.Release(a)
						delete(m.anchors, a)
						break
					}
				case op < 9:
					snaps = append(snaps, frozen{b.Snapshot(), string(m.text)})
				default:
					checkPoints(t, r, b, m)
				}
				check(t, b, m)
			}
			for _, f := range snaps {
				if f.s.String() != f.text {
					t.Fatalf("snapshot at version %d changed", f.s.Version())
				}
			}
		})
	}
}

// applyRandom applies one to four disjoint edits, each relative to a random anchor.
func applyRandom(t *testing.T, r *rand.Rand, b *Buffer, m *model) {
	t.Helper()
	n := 1 + r.IntN(4)
	cuts := make([]int, 2*n)
	for i := range cuts {
		cuts[i] = r.IntN(len(m.text) + 1)
	}
	slices.Sort(cuts)
	handles := []Anchor{Start}
	for a := range m.anchors {
		handles = append(handles, a)
	}
	var edits []Edit
	type ranged struct {
		s, e int
		text string
	}
	var want []ranged
	for i := range n {
		s, e := cuts[2*i], cuts[2*i+1]
		text := randText(r, randLen(r))
		a := handles[r.IntN(len(handles))]
		base := 0
		if a != Start {
			base = m.anchors[a]
		}
		edits = append(edits, Edit{Anchor: a, Offset: s - base, Delete: e - s, Text: text})
		want = append(want, ranged{s, e, text})
	}
	r.Shuffle(len(edits), func(i, j int) {
		edits[i], edits[j] = edits[j], edits[i]
		want[i], want[j] = want[j], want[i]
	})
	if err := b.Apply(edits...); err != nil {
		t.Fatal(err)
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(x, y int) int { return cmp.Or(want[x].s-want[y].s, want[x].e-want[y].e) })
	for i := n - 1; i >= 0; i-- {
		w := want[order[i]]
		m.replace(w.s, w.e, w.text)
	}
}

func TestApplyRejects(t *testing.T) {
	b := New([]byte("hello world"))
	v := b.Version()
	cases := map[string][]Edit{
		"past the end":    {{Offset: 9, Delete: 5}},
		"before 0":        {{Offset: -1}},
		"negative delete": {{Offset: 3, Delete: -1}},
		"overlap":         {{Offset: 0, Delete: 5}, {Offset: 4, Delete: 2}},
	}
	for name, edits := range cases {
		if err := b.Apply(edits...); err == nil {
			t.Errorf("%s: Apply succeeded", name)
		}
	}
	if b.String() != "hello world" || b.Version() != v {
		t.Fatalf("a rejected Apply changed the buffer: %q at version %d", b.String(), b.Version())
	}
}

func TestInsertsAtOnePositionKeepArgumentOrder(t *testing.T) {
	b := New([]byte("ab"))
	if err := b.Apply(Edit{Offset: 1, Text: "1"}, Edit{Offset: 1, Text: "2"}, Edit{Offset: 1, Text: "3"}); err != nil {
		t.Fatal(err)
	}
	if b.String() != "a123b" {
		t.Fatalf("got %q", b.String())
	}
}

func TestReleasedAnchorPanics(t *testing.T) {
	b := New([]byte("abc"))
	a, _ := b.Anchor(1, Right)
	b.Release(a)
	defer func() {
		if recover() == nil {
			t.Fatal("Resolve of a released anchor did not panic")
		}
	}()
	b.Resolve(a)
}

func TestAnchorKeysRelabel(t *testing.T) {
	b := New([]byte("x"))
	var as []Anchor
	for range 200 {
		a, err := b.Anchor(0, Left)
		if err != nil {
			t.Fatal(err)
		}
		as = append(as, a)
	}
	if err := b.Apply(Edit{Text: "abc"}); err != nil {
		t.Fatal(err)
	}
	for _, a := range as {
		if got := b.Resolve(a); got != 0 {
			t.Fatalf("anchor at %d, want 0", got)
		}
	}
}

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
		b := New([]byte(old))
		// Lines wholly before the first differing byte, or wholly after the last,
		// lie outside every hunk.
		pre := commonPrefix([]byte(old), []byte(next))
		firstLine := strings.LastIndexByte(old[:pre], '\n') + 1
		suf := min(commonSuffix([]byte(old), []byte(next)), len(old)-pre, len(next)-pre)
		lastLine := len(old)
		if i := strings.IndexByte(old[len(old)-suf:], '\n'); i >= 0 {
			lastLine = len(old) - suf + i + 1
		}
		var before, after []Anchor
		for range 20 {
			off := r.IntN(len(old) + 1)
			bias := Bias(r.IntN(2))
			a, _ := b.Anchor(off, bias)
			switch {
			case off < firstLine:
				before = append(before, a)
			case off > lastLine:
				after = append(after, a)
			}
		}
		snap := b.Snapshot()
		want := make(map[Anchor]int)
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
	b := New([]byte(old))
	at := strings.Index(old, "keep")
	a, _ := b.Anchor(at, Left)
	inner, _ := b.Anchor(strings.Index(old, "1\n"), Right)
	b.ApplyDiff([]byte(next))
	if got, want := b.Resolve(a), strings.Index(next, "keep"); got != want {
		t.Fatalf("anchor between hunks at %d, want %d", got, want)
	}
	if got, want := b.Resolve(inner), strings.Index(next, "1\n"); got != want {
		t.Fatalf("anchor inside a trimmed hunk at %d, want %d", got, want)
	}
}
