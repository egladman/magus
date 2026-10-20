package wordclass

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/libs/proofread/internal/wordclass"
)

// The defaults proofread-train train passes. Tuned on dev: past 1024 levels and 10
// passes, accuracy holds and the model grows.
const (
	DefaultIterations = 10
	DefaultSeed       = 1
	DefaultLevels     = 1024
)

// Options are one training run's settings.
type Options struct {
	ModelPath  string // where the gzipped model is written
	TestPath   string // where the gzipped EWT test split is written, for the accuracy test
	Iterations int    // passes over the training split
	Seed       uint64 // seeds the per-pass shuffle
	Levels     int    // the largest quantized weight
}

// Run trains a wordclass model on UD English EWT, fetched by a pinned digest
// or read from the cache, writes it and the test split to o's paths, and
// reports the accuracy it measured to w. It needs the network on a cold cache.
func Run(w io.Writer, o Options) error {
	if o.ModelPath == "" || o.TestPath == "" {
		return errors.New("train needs both a model path and a test split path")
	}

	if o.Iterations < 1 || o.Levels < 1 {
		return fmt.Errorf("train needs at least 1 iteration and 1 level, got %d and %d", o.Iterations, o.Levels)
	}

	splits := map[string][]sentence{}

	var testRaw []byte

	for _, split := range []string{"train", "dev", "test"} {
		raw, err := fetch("en_ewt-ud-" + split + ".conllu")
		if err != nil {
			return err
		}

		if split == "test" {
			testRaw = raw
		}

		if splits[split], err = readSentences(bytes.NewReader(raw)); err != nil {
			return fmt.Errorf("%s: %w", split, err)
		}
	}

	start := time.Now()
	t := newTrainer(splits["train"])
	t.train(splits["train"], o.Iterations, o.Seed)

	weights, err := t.p.quantize(o.Levels)
	if err != nil {
		return err
	}

	labels := make([]wordclass.Label, len(t.classes))
	for i, c := range t.classes {
		labels[i] = wordclass.Label{UPOS: c.upos, XPOS: c.xpos}
	}

	m, err := wordclass.NewModel(labels, t.dict, weights)
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

	if err := writeFile(o.ModelPath, model); err != nil {
		return err
	}

	if err := writeFile(o.TestPath, testGz); err != nil {
		return err
	}

	fmt.Fprintf(w, "trained %d passes in %s: %d classes, %d dictionary words, %d features\n",
		o.Iterations, elapsed.Round(time.Millisecond), len(t.classes), len(t.dict), len(weights))
	for _, s := range []struct {
		name string
		a    wordclass.Accuracy
	}{{"dev: ", dev}, {"test:", m.Accuracy}} {
		fmt.Fprintf(w, "%s UPOS %.4f XPOS %.4f over %d tokens\n", s.name, ratio(s.a.UPOS, s.a.Words), ratio(s.a.XPOS, s.a.Words), s.a.Words)
	}
	fmt.Fprintf(w, "model: %d bytes\n", len(model))

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

func newTrainer(train []sentence) *trainer {
	t := &trainer{index: map[class]uint16{}, dict: map[string]uint16{}, p: newPerceptron()}
	counts := map[string]map[class]int{}

	for _, s := range train {
		for _, tok := range s.words {
			c := class{xpos: tok.xpos, upos: tok.upos}
			if _, ok := t.index[c]; !ok {
				t.index[c] = 0
				t.classes = append(t.classes, c)
			}

			if counts[tok.form] == nil {
				counts[tok.form] = map[class]int{}
			}

			counts[tok.form][c]++
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
func (t *trainer) train(train []sentence, iterations int, seed uint64) {
	order := slices.Clone(train)
	rng := rand.New(rand.NewPCG(seed, seed))
	scores := make([]int64, len(t.classes))

	var feats []wordclass.Feature

	for range iterations {
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

		for _, s := range order {
			padded := wordclass.Pad(s.forms())
			prev, prev2 := wordclass.SentenceStart, wordclass.SentenceStart2

			for i, tok := range s.words {
				guess, ok := t.dict[tok.form]
				if !ok {
					feats = wordclass.AppendFeatures(feats[:0], padded, i, tok.form, prev, prev2)
					guess = t.p.predict(feats, scores)
					t.p.update(t.index[class{xpos: tok.xpos, upos: tok.upos}], guess, feats)
				}

				prev, prev2 = guess, prev
			}
		}
	}
}

// evaluate tags each sentence with m and counts the words, the UPOS matches
// and the XPOS matches.
func evaluate(m *wordclass.Model, sentences []sentence) (a wordclass.Accuracy) {
	for _, s := range sentences {
		for i, got := range m.Tag(s.forms()) {
			a.Words++

			if got.Class.String() == s.words[i].upos {
				a.UPOS++
			}

			if got.Penn == s.words[i].xpos {
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
