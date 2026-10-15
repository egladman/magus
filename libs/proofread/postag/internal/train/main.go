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

		if splits[split], err = parseCoNLLU(raw); err != nil {
			return fmt.Errorf("%s: %w", split, err)
		}
	}

	start := time.Now()
	t := newTrainer(splits["train"])
	t.train(splits["train"], iterations, seed)

	q, err := t.p.quantize(levels)
	if err != nil {
		return err
	}

	elapsed := time.Since(start)

	dev := t.evaluate(q, splits["dev"])
	test := t.evaluate(q, splits["test"])

	model, err := gzipBytes(t.encode(q, test))
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
		iterations, elapsed.Round(time.Millisecond), len(t.classes), len(t.dict), len(q.weights))
	fmt.Printf("dev:  UPOS %.4f XPOS %.4f over %d tokens\n", ratio(dev.upos, dev.tokens), ratio(dev.xpos, dev.tokens), dev.tokens)
	fmt.Printf("test: UPOS %.4f XPOS %.4f over %d tokens\n", ratio(test.upos, test.tokens), ratio(test.xpos, test.tokens), test.tokens)
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

func newTrainer(train []sentence) *trainer {
	t := &trainer{index: map[class]uint16{}, dict: map[string]uint16{}, p: newPerceptron()}
	counts := map[string]map[class]int{}

	for _, s := range train {
		for _, tok := range s {
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

	var feats []feature.Hash

	for range iterations {
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

		for _, s := range order {
			words := s.forms()
			context := feature.Padded(words)
			prev, prev2 := feature.Start, feature.Start2

			for i, tok := range s {
				guess, ok := t.dict[tok.form]
				if !ok {
					feats = feature.Extract(feats[:0], context, i, tok.form, prev, prev2)
					guess = t.p.predict(feats, scores)
					t.p.update(t.index[class{xpos: tok.xpos, upos: tok.upos}], guess, feats)
				}

				prev, prev2 = guess, prev
			}
		}
	}
}

// evaluate tags each sentence exactly as postag.Tag does and counts the
// tokens, the UPOS matches and the XPOS matches.
func (t *trainer) evaluate(q *quantized, sentences []sentence) (counts testCounts) {
	scores := make([]int64, len(t.classes))

	var feats []feature.Hash

	for _, s := range sentences {
		words := s.forms()
		context := feature.Padded(words)
		prev, prev2 := feature.Start, feature.Start2

		for i, tok := range s {
			c, ok := t.dict[tok.form]
			if !ok {
				feats = feature.Extract(feats[:0], context, i, tok.form, prev, prev2)
				c = q.predict(feats, scores)
			}

			counts.tokens++

			if t.classes[c].upos == tok.upos {
				counts.upos++
			}

			if t.classes[c].xpos == tok.xpos {
				counts.xpos++
			}

			prev, prev2 = c, prev
		}
	}

	return counts
}

func (s sentence) forms() []string {
	words := make([]string, len(s))
	for i, tok := range s {
		words[i] = tok.form
	}
	return words
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
