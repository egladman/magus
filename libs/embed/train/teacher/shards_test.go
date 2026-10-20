package teacher

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countingEmbedder maps a text to [len(text), 1] and counts the texts it was asked for.
type countingEmbedder struct {
	calls, tokens int
}

func (c *countingEmbedder) Encode(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = []float32{float32(len(t)), 1}
		c.calls++
		c.tokens += len(t)
	}
	return out, nil
}

func (c *countingEmbedder) Tokens() int { return c.tokens }

func shardTexts(n int) []Text {
	out := make([]Text, n)
	for i := range out {
		out[i] = Text{ID: fmt.Sprint("t", i), Text: strings.Repeat("w", i+1)}
	}
	return out
}

func manifestLines(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func TestRunResumesFromWholeShards(t *testing.T) {
	dir := t.TempDir()
	spec := RunSpec{Model: "m", Dim: 2, ShardSize: 2}
	texts := shardTexts(5)

	enc := &countingEmbedder{}
	st, err := Run(dir, spec, texts, enc, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Shards != 3 || st.Encoded != 1 || enc.calls != 2 {
		t.Fatalf("first run: %+v after %d texts, want 3 shards, 1 encoded, 2 texts", st, enc.calls)
	}

	enc = &countingEmbedder{}
	st, err = Run(dir, spec, texts, enc, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Skipped != 1 || st.Encoded != 2 || enc.calls != 3 {
		t.Fatalf("resume: %+v after %d texts, want 1 skipped, 2 encoded, 3 texts", st, enc.calls)
	}
	if lines := manifestLines(t, dir); len(lines) != 4 || !strings.Contains(lines[0], `"kind":"run"`) {
		t.Fatalf("manifest has %d lines, want a run record and 3 shards:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	enc = &countingEmbedder{}
	if st, err = Run(dir, spec, texts, enc, 0, nil); err != nil || st.Skipped != 3 || enc.calls != 0 {
		t.Fatalf("finished run: %+v, %v after %d texts; want every shard skipped", st, err, enc.calls)
	}
}

func TestRunObjectsAreContentAddressed(t *testing.T) {
	dir := t.TempDir()
	var recs []Record
	if _, err := Run(dir, RunSpec{Model: "m", Dim: 2, ShardSize: 4}, shardTexts(3), &countingEmbedder{}, 0,
		func(r Record) { recs = append(recs, r) }); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "objects", recs[0].Object+".f32"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	// Three rows of [len, 1]: 1, 2 and 3 as little-endian float32.
	want := []byte{0, 0, 0x80, 0x3f, 0, 0, 0x80, 0x3f, 0, 0, 0, 0x40, 0, 0, 0x80, 0x3f, 0, 0, 0x40, 0x40, 0, 0, 0x80, 0x3f}
	if hex.EncodeToString(sum[:]) != recs[0].Object || !bytes.Equal(b, want) || recs[0].Tokens != 6 {
		t.Errorf("object %s holds % x (tokens %d), want sha256-named % x with 6 tokens", recs[0].Object, b, recs[0].Tokens, want)
	}
}

func TestRunReencodesADamagedObject(t *testing.T) {
	dir := t.TempDir()
	spec := RunSpec{Model: "m", Dim: 2, ShardSize: 2}
	var recs []Record
	if _, err := Run(dir, spec, shardTexts(4), &countingEmbedder{}, 0, func(r Record) { recs = append(recs, r) }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "objects", recs[1].Object+".f32"), []byte("torn"), 0o644); err != nil {
		t.Fatal(err)
	}
	enc := &countingEmbedder{}
	if st, err := Run(dir, spec, shardTexts(4), enc, 0, nil); err != nil || st.Skipped != 1 || enc.calls != 2 {
		t.Errorf("rerun: %+v, %v after %d texts; want shard 1 encoded again", st, err, enc.calls)
	}
}

func TestRunCutsATornManifestTail(t *testing.T) {
	dir := t.TempDir()
	spec := RunSpec{Model: "m", Dim: 2, ShardSize: 2}
	if _, err := Run(dir, spec, shardTexts(2), &countingEmbedder{}, 0, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "manifest.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"kind":"shard","sha`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if st, err := Run(dir, spec, shardTexts(4), &countingEmbedder{}, 0, nil); err != nil || st.Skipped != 1 || st.Encoded != 1 {
		t.Fatalf("after a torn append: %+v, %v", st, err)
	}
	for _, l := range manifestLines(t, dir) {
		if !strings.HasSuffix(l, "}") {
			t.Errorf("manifest kept a torn line: %s", l)
		}
	}
}

func TestRunRefusesAnotherSpec(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, RunSpec{Model: "m", Dim: 2, ShardSize: 2}, shardTexts(2), &countingEmbedder{}, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(dir, RunSpec{Model: "m", Prefix: "q: ", Dim: 2, ShardSize: 2}, shardTexts(2), &countingEmbedder{}, 0, nil); err == nil {
		t.Error("a manifest written under another spec was reused")
	}
}

func TestReadTextsRefusesRepeatedIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "texts.jsonl")
	if err := os.WriteFile(path, []byte(`{"id":"a","text":"x"}`+"\n\n"+`{"id":"a","text":"y"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTexts(path); err == nil {
		t.Error("a repeated id was accepted")
	}
}
