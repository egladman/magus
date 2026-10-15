package postag

import (
	"bytes"
	"compress/gzip"
	"io"
	"reflect"
	"testing"

	"github.com/egladman/magus/libs/proofread/postag/internal/feature"
)

func TestModelRoundTrip(t *testing.T) {
	zr, err := gzip.NewReader(bytes.NewReader(modelGz))
	if err != nil {
		t.Fatal(err)
	}

	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}

	var m Model
	if err := m.UnmarshalBinary(raw); err != nil {
		t.Fatal(err)
	}

	got, err := m.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, raw) {
		t.Fatalf("MarshalBinary wrote %d bytes, the embedded model holds %d", len(got), len(raw))
	}
}

func TestNewModel(t *testing.T) {
	labels := []Label{{UPOS: "NOUN", XPOS: "NN"}, {UPOS: "VERB", XPOS: "VB"}}
	dict := map[string]uint16{"the": 0}
	weights := map[feature.Hash][]Weight{9: {{Class: 0, Value: -2}, {Class: 1, Value: 3}}, 4: {{Class: 1, Value: 1}}}

	m, err := NewModel(labels, dict, weights)
	if err != nil {
		t.Fatal(err)
	}

	m.Accuracy = Accuracy{Words: 3, UPOS: 2, XPOS: 1}

	b, err := m.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	var back Model
	if err := back.UnmarshalBinary(b); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(&back, m) {
		t.Errorf("round trip\n got %+v\nwant %+v", back, *m)
	}

	for name, args := range map[string]struct {
		labels  []Label
		dict    map[string]uint16
		weights map[feature.Hash][]Weight
	}{
		"an unknown UPOS":                  {[]Label{{UPOS: "NN"}}, nil, nil},
		"a word's class past the labels":   {labels, map[string]uint16{"x": 2}, nil},
		"a weight's class past the labels": {labels, nil, map[feature.Hash][]Weight{1: {{Class: 2}}}},
	} {
		if _, err := NewModel(args.labels, args.dict, args.weights); err == nil {
			t.Errorf("NewModel with %s: no error", name)
		}
	}
}

func TestModelUnmarshalCorrupt(t *testing.T) {
	m, err := NewModel([]Label{{UPOS: "NOUN", XPOS: "NN"}}, map[string]uint16{"a": 0}, nil)
	if err != nil {
		t.Fatal(err)
	}

	good, err := m.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	for name, b := range map[string][]byte{
		"no magic":       []byte("model"),
		"truncated":      good[:len(good)-1],
		"trailing bytes": append(bytes.Clone(good), 0),
	} {
		if err := new(Model).UnmarshalBinary(b); err == nil {
			t.Errorf("UnmarshalBinary of %s: no error", name)
		}
	}
}
