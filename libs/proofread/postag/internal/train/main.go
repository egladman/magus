package main

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/libs/proofread/internal/conllu"
	"github.com/egladman/magus/libs/proofread/postag"
	"github.com/egladman/magus/libs/proofread/postag/internal/feature"
)

func main() {
	modelPath := flag.String("model", "gen/model.gz", "where to write the model")
	testPath := flag.String("testdata", "testdata/en_ewt-ud-test.conllu.gz", "where to write the gzipped test split")
	iterations := flag.Int("iterations", 10, "passes over the training split")
	seed := flag.Uint64("seed", 1, "seed for the per-pass shuffle")
	// Tuned on dev: past 1024 levels and 10 passes, accuracy holds and the model grows.
	levels := flag.Int("levels", 1024, "the largest quantized weight")
	flag.Parse()

	if err := run(*modelPath, *testPath, *iterations, *seed, *levels); err != nil {
		fmt.Fprintf(os.Stderr, "train: %v\n", err)
		os.Exit(1)
	}
}

func run(modelPath, testPath string, iterations int, seed uint64, levels int) error {
	splits := map[string][]conllu.Sentence{}

	var testRaw []byte

	for _, split := range []string{"train", "dev", "test"} {
		raw, err := fetch("en_ewt-ud-" + split + ".conllu")
		if err != nil {
			return err
		}

		if split == "test" {
			testRaw = raw
		}

		if splits[split], err = conllu.Read(bytes.NewReader(raw)); err != nil {
			return fmt.Errorf("%s: %w", split, err)
		}
	}

	start := time.Now()
	t := newTrainer(splits["train"])
	t.train(splits["train"], iterations, seed)

	weights, err := t.p.quantize(levels)
	if err != nil {
		return err
	}

	labels := make([]postag.Label, len(t.classes))
	for i, c := range t.classes {
		labels[i] = postag.Label{UPOS: c.upos, XPOS: c.xpos}
	}

	m, err := postag.NewModel(labels, t.dict, weights)
	if err != nil {
		return err
	}

	elapsed := time.Since(start)

	dev := evaluate(m, splits["dev"])
	m.Accuracy = evaluate(m, splits["test"])

	raw, err := m.MarshalBinary()
	if err != nil {
		return err
	}

	model, err := gzipBytes(raw)
	if err != nil {
		return err
	}

	testGz, err := gzipBytes(testRaw)
	if err != nil {
		return err
	}

	if err := writeFile(modelPath, model); err != nil {
		return err
	}

	if err := writeFile(testPath, testGz); err != nil {
		return err
	}

	fmt.Printf("trained %d passes in %s: %d classes, %d dictionary words, %d features\n",
		iterations, elapsed.Round(time.Millisecond), len(t.classes), len(t.dict), len(weights))
	for _, s := range []struct {
		name string
		a    postag.Accuracy
	}{{"dev: ", dev}, {"test:", m.Accuracy}} {
		fmt.Printf("%s UPOS %.4f XPOS %.4f over %d tokens\n", s.name, ratio(s.a.UPOS, s.a.Words), ratio(s.a.XPOS, s.a.Words), s.a.Words)
	}
	fmt.Printf("model: %d bytes\n", len(model))

	return nil
}

type class struct{ xpos, upos string }

type trainer struct {
	classes []class
	index   map[class]uint16
	dict    map[string]uint16
	p       *perceptron
}

// Honnibal's thresholds: a word seen 20 times and tagged one way 97% of the
// time skips the perceptron entirely.
const (
	dictMinCount = 20
	dictMinShare = 0.97
)

func newTrainer(train []conllu.Sentence) *trainer {
	t := &trainer{index: map[class]uint16{}, dict: map[string]uint16{}, p: newPerceptron()}
	counts := map[string]map[class]int{}

	for _, s := range train {
		for _, tok := range s.Words {
			c := class{xpos: tok.XPOS, upos: tok.UPOS}
			if _, ok := t.index[c]; !ok {
				t.index[c] = 0
				t.classes = append(t.classes, c)
			}

			if counts[tok.Form] == nil {
				counts[tok.Form] = map[class]int{}
			}

			counts[tok.Form][c]++
		}
	}

	slices.SortFunc(t.classes, func(a, b class) int {
		return cmp.Or(cmp.Compare(a.xpos, b.xpos), cmp.Compare(a.upos, b.upos))
	})

	for i, c := range t.classes {
		t.index[c] = uint16(i)
	}

	for word, byClass := range counts {
		total, top, topClass := 0, 0, class{}

		for c, n := range byClass {
			total += n
			if n > top || n == top && cmp.Or(cmp.Compare(c.xpos, topClass.xpos), cmp.Compare(c.upos, topClass.upos)) < 0 {
				top, topClass = n, c
			}
		}

		if total >= dictMinCount && float64(top)/float64(total) >= dictMinShare {
			t.dict[word] = t.index[topClass]
		}
	}

	return t
}

// train conditions each word on the tags it predicted for the words before
// it, as tagging will, not on the gold tags.
func (t *trainer) train(train []conllu.Sentence, iterations int, seed uint64) {
	order := slices.Clone(train)
	rng := rand.New(rand.NewPCG(seed, seed))
	scores := make([]int64, len(t.classes))

	var feats []feature.Hash

	for range iterations {
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

		for _, s := range order {
			context := feature.Padded(s.Forms())
			prev, prev2 := feature.Start, feature.Start2

			for i, tok := range s.Words {
				guess, ok := t.dict[tok.Form]
				if !ok {
					feats = feature.Extract(feats[:0], context, i, tok.Form, prev, prev2)
					guess = t.p.predict(feats, scores)
					t.p.update(t.index[class{xpos: tok.XPOS, upos: tok.UPOS}], guess, feats)
				}

				prev, prev2 = guess, prev
			}
		}
	}
}

// evaluate tags each sentence with m and counts the words, the UPOS matches
// and the XPOS matches.
func evaluate(m *postag.Model, sentences []conllu.Sentence) (a postag.Accuracy) {
	for _, s := range sentences {
		for i, got := range m.Tag(s.Forms()) {
			a.Words++

			if got.UPOS.String() == s.Words[i].UPOS {
				a.UPOS++
			}

			if string(got.XPOS) == s.Words[i].XPOS {
				a.XPOS++
			}
		}
	}

	return a
}

func ratio(n, d int) float64 { return float64(n) / float64(d) }

// gzipBytes compresses with a zero header, so equal input gives equal bytes.
func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}

	if _, err := zw.Write(b); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, b, 0o644)
}
