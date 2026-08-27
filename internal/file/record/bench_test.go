package record

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// benchOwner mirrors the shape lock.go writes on every acquire: a handful of short scalars
// describing a live process.
type benchOwner struct {
	PID     int       `record:"pid"`
	Inv     string    `record:"invocation,omitempty"`
	Command string    `record:"command"`
	Cwd     string    `record:"cwd"`
	Started time.Time `record:"started"`
}

func benchValue() benchOwner {
	return benchOwner{
		PID:     41221,
		Inv:     "inv-0123456789abcdef",
		Command: "magus run ci .",
		Cwd:     "/Users/someone/Repos/magus",
		Started: time.Now(),
	}
}

// BenchmarkWritePublish is the cost of publishing a record where none exists: what the first
// acquire of a lock path pays.
//
// The destination is removed inside the loop rather than left to accumulate: a parent directory
// that grows by one entry per iteration measures its own fan-out, not the write.
func BenchmarkWritePublish(b *testing.B) {
	dir := b.TempDir()
	target := filepath.Join(dir, "owner")
	v := benchValue()
	b.ReportAllocs()
	for b.Loop() {
		if err := Write(target, v); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err := os.RemoveAll(target); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}

// BenchmarkWriteReplace is the steady-state cost: a record is already there, so Write pays the
// RemoveAll of the old one on top of building the new. This is the shape lock.go actually hits,
// since a lock path is acquired over and over.
func BenchmarkWriteReplace(b *testing.B) {
	dir := b.TempDir()
	target := filepath.Join(dir, "owner")
	v := benchValue()
	if err := Write(target, v); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := Write(target, v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRead is the other half of the hot path: reentrantErr reads the owner record on
// every contended acquire.
func BenchmarkRead(b *testing.B) {
	dir := b.TempDir()
	target := filepath.Join(dir, "owner")
	if err := Write(target, benchValue()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		var got benchOwner
		if err := Read(target, &got); err != nil {
			b.Fatal(err)
		}
	}
}

// The decomposition: Write is MkdirTemp + one WriteFile per field + RemoveAll of the old record
// + Rename. These measure each piece against the same five-field shape, so the 670us of
// BenchmarkWriteReplace can be attributed rather than guessed at.

func BenchmarkPieceMkdirTemp(b *testing.B) {
	dir := b.TempDir()
	b.ReportAllocs()
	for b.Loop() {
		tmp, err := os.MkdirTemp(dir, ".record-tmp-owner-")
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		_ = os.RemoveAll(tmp)
		b.StartTimer()
	}
}

func BenchmarkPieceWriteFields(b *testing.B) {
	dir := b.TempDir()
	fields, err := marshal(benchValue())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		for k, val := range fields {
			if err := os.WriteFile(filepath.Join(dir, k), []byte(val+"\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkPieceRemoveAll(b *testing.B) {
	dir := b.TempDir()
	target := filepath.Join(dir, "owner")
	v := benchValue()
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		if err := Write(target, v); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := os.RemoveAll(target); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPieceRename(b *testing.B) {
	dir := b.TempDir()
	a, c := filepath.Join(dir, "a"), filepath.Join(dir, "c")
	if err := os.Mkdir(a, 0o755); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		from, to := a, c
		if i%2 == 1 {
			from, to = c, a
		}
		if err := os.Rename(from, to); err != nil {
			b.Fatal(err)
		}
	}
}
