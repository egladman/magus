package textbuf

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"
)

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

// reformat changes the indentation of every 40th line, the shape of a formatter run
// over a mostly formatted file.
func reformat(src []byte) []byte {
	lines := bytes.SplitAfter(src, nl)
	for i := 0; i < len(lines); i += 40 {
		lines[i] = append([]byte("    "), bytes.TrimLeft(lines[i], "\t")...)
	}
	return bytes.Join(lines, nil)
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
