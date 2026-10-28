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
	anchors map[anchor]int
}

func (m *model) replace(s, e int, text string) {
	m.text = slices.Concat(m.text[:s], []byte(text), m.text[e:])
	for a, p := range m.anchors {
		switch {
		case p < s || p == s && a.side() == left:
		case p <= e && a.side() == left:
			p = s
		case p <= e:
			p = s + len(text)
		default:
			p += len(text) - (e - s)
		}
		m.anchors[a] = p
	}
}

func (m *model) point(off int) point {
	before := m.text[:off]
	line := bytes.Count(before, nl)
	return point{line, off - (bytes.LastIndexByte(before, '\n') + 1)}
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

func check(t *testing.T, b *buffer, m *model) {
	t.Helper()
	validate(t, &b.t)
	validate(t, &b.anchors[left].t)
	validate(t, &b.anchors[right].t)
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
			t.Fatalf("anchor %#x (bias %d) at %d, want %d", uint32(a), a.side(), got, want)
		}
	}
}

func checkPoints(t *testing.T, r *rand.Rand, b *buffer, m *model) {
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
	if _, err := b.Offset(point{end.Line, end.Column + 1}); !errors.Is(err, errRange) {
		t.Fatalf("Offset past the end: %v", err)
	}
	if _, err := b.Offset(point{end.Line + 1, 0}); !errors.Is(err, errRange) {
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
			b := newBuffer([]byte(init))
			m := &model{text: []byte(init), anchors: map[anchor]int{}}
			type frozen struct {
				s    snapshot
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
						a, err := b.Anchor(off, bias(r.IntN(2)))
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
func applyRandom(t *testing.T, r *rand.Rand, b *buffer, m *model) {
	t.Helper()
	n := 1 + r.IntN(4)
	cuts := make([]int, 2*n)
	for i := range cuts {
		cuts[i] = r.IntN(len(m.text) + 1)
	}
	slices.Sort(cuts)
	handles := []anchor{startAnchor}
	for a := range m.anchors {
		handles = append(handles, a)
	}
	var edits []edit
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
		if a != startAnchor {
			base = m.anchors[a]
		}
		edits = append(edits, edit{Anchor: a, Offset: s - base, Delete: e - s, Text: text})
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
	b := newBuffer([]byte("hello world"))
	v := b.Version()
	cases := map[string][]edit{
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
	b := newBuffer([]byte("ab"))
	if err := b.Apply(edit{Offset: 1, Text: "1"}, edit{Offset: 1, Text: "2"}, edit{Offset: 1, Text: "3"}); err != nil {
		t.Fatal(err)
	}
	if b.String() != "a123b" {
		t.Fatalf("got %q", b.String())
	}
}

func TestReleasedAnchorPanics(t *testing.T) {
	b := newBuffer([]byte("abc"))
	a, _ := b.Anchor(1, right)
	b.Release(a)
	defer func() {
		if recover() == nil {
			t.Fatal("Resolve of a released anchor did not panic")
		}
	}()
	b.Resolve(a)
}

var benchSizes = []int{4 << 10, 64 << 10, 1 << 20}

const benchAnchors = 256

func sizeName(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%dMiB", n>>20)
	}
	return fmt.Sprintf("%dKiB", n>>10)
}

// goSource returns about size bytes shaped like Go source.
func goSource(size int) []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "func f%d(a, b int) int {\n", i)
		for j := range 6 {
			fmt.Fprintf(&b, "\tx%d := compute(a, b, %d) // step %d\n", j, i*j, j)
		}
		b.WriteString("\treturn x0\n}\n\n")
	}
	return b.Bytes()
}

func benchBuffer(b *testing.B, size, anchors int) (*buffer, *rand.Rand) {
	b.Helper()
	buf := newBuffer(goSource(size))
	r := rand.New(rand.NewPCG(1, 2))
	for range anchors {
		if _, err := buf.Anchor(r.IntN(buf.Len()+1), bias(r.IntN(2))); err != nil {
			b.Fatal(err)
		}
	}
	return buf, r
}

func BenchmarkInsert(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(sizeName(size), func(b *testing.B) {
			buf, r := benchBuffer(b, size, benchAnchors)
			b.ReportAllocs()
			for b.Loop() {
				if err := buf.Apply(edit{Offset: r.IntN(buf.Len() + 1), Text: "x"}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDelete(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(sizeName(size), func(b *testing.B) {
			buf, r := benchBuffer(b, size, benchAnchors)
			b.ReportAllocs()
			for b.Loop() {
				if buf.Len() < size/2 {
					b.StopTimer()
					buf, _ = benchBuffer(b, size, benchAnchors)
					b.StartTimer()
				}
				if err := buf.Apply(edit{Offset: r.IntN(buf.Len() - 7), Delete: 8}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkInsertSnapshot takes a snapshot before each insert, so every insert copies
// the path it writes.
func BenchmarkInsertSnapshot(b *testing.B) {
	buf, r := benchBuffer(b, 64<<10, benchAnchors)
	b.ReportAllocs()
	for b.Loop() {
		_ = buf.Snapshot()
		if err := buf.Apply(edit{Offset: r.IntN(buf.Len() + 1), Text: "x"}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolve(b *testing.B) {
	for _, n := range []int{256, 16384} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			buf := newBuffer(goSource(1 << 20))
			r := rand.New(rand.NewPCG(1, 2))
			as := make([]anchor, n)
			for i := range as {
				as[i], _ = buf.Anchor(r.IntN(buf.Len()+1), bias(r.IntN(2)))
			}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				buf.Resolve(as[i%n])
				i++
			}
		})
	}
}
