package codec_test

import (
	"bytes"
	"testing"

	"github.com/egladman/magus/internal/codec"
)

type pair struct {
	K string `json:"k"`
	V int    `json:"v"`
}

func TestMarshalUnmarshalRoundtrip(t *testing.T) {
	t.Parallel()
	in := pair{K: "hello", V: 42}
	b, err := codec.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out pair
	if err := codec.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("got %+v, want %+v", out, in)
	}
}

func TestMarshalIndent(t *testing.T) {
	t.Parallel()
	b, err := codec.MarshalIndent(map[string]int{"x": 1}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("\n")) {
		t.Error("MarshalIndent output has no newlines")
	}
}

func TestEncoderDecoder(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	enc := codec.NewEncoder(&buf)
	if err := enc.Encode(pair{K: "a", V: 1}); err != nil {
		t.Fatal(err)
	}
	dec := codec.NewDecoder(&buf)
	var got pair
	if err := dec.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.K != "a" || got.V != 1 {
		t.Errorf("got %+v, want {a 1}", got)
	}
}
