package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/egladman/magus/hack/embed/distill"
	"github.com/egladman/magus/hack/embed/teacher"
	"github.com/egladman/magus/internal/textembed"
)

// pcaSalt picks the training texts whose targets PCA is fitted on.
const pcaSalt = "embed-pca/v1:"

// vocabFlags are the flags vocab and train share: train rebuilds the vocabulary from
// them and refuses a token run that embedded another.
type vocabFlags struct {
	texts, forced            string
	minCount, maxBytes, size int
}

func (f *vocabFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.texts, "texts", "", "the training texts, JSON lines of {\"id\", \"text\"}, as embed-mine writes them under hack/embed/gen/")
	fs.StringVar(&f.forced, "forced", "", "forced tokens, one per line; every token textrank.Tokens yields from a line is kept")
	fs.IntVar(&f.minCount, "min-count", 2, "a counted token below this count is left out")
	fs.IntVar(&f.maxBytes, "max-bytes", 32, "a counted token longer than this is left out")
	fs.IntVar(&f.size, "size", 29000, "vocabulary size, forced tokens included")
}

func (f *vocabFlags) build() (*distill.Vocab, error) {
	if f.texts == "" || f.forced == "" {
		return nil, errors.New("-texts and -forced are required")
	}
	forced, err := readLines(f.forced)
	if err != nil {
		return nil, err
	}
	return distill.BuildVocab(jsonlTexts{f.texts}, distill.VocabOptions{
		MinCount: f.minCount, MaxBytes: f.maxBytes, Size: f.size, Forced: forced,
	})
}

// runVocab builds the vocabulary and writes it as the texts file the token teacher run
// embeds.
func runVocab(args []string) error {
	fs := flag.NewFlagSet("vocab", flag.ExitOnError)
	var vf vocabFlags
	vf.register(fs)
	out := fs.String("out", "", "where to write the token texts for `embed teacher -texts`")
	_ = fs.Parse(args)
	if *out == "" {
		return errors.New("-out is required")
	}
	v, err := vf.build()
	if err != nil {
		return err
	}
	b, err := tokenTexts(v)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("%d tokens over %d counted; token texts %s\n", v.Len(), v.Total, sha256Hex(b))
	return nil
}

// runTrain distills the table: it rebuilds the vocabulary, reads the teacher runs over
// the tokens and the training texts, fits PCA on a hash sample of the texts' targets,
// starts the student from the projected token vectors with count SIF weights at the
// least-squares scale, trains it, and writes the quantized table.
func runTrain(args []string) error {
	fs := flag.NewFlagSet("train", flag.ExitOnError)
	var vf vocabFlags
	vf.register(fs)
	tokenRun := fs.String("token-run", "", "the teacher run directory over `embed vocab`'s token texts")
	textsRun := fs.String("texts-run", "", "the teacher run directory over -texts")
	pairsPath := fs.String("pairs", "", "pairs JSON lines (hack/embed/gen/pairs.jsonl); required unless -lambda 0")
	root := fs.String("root", ".", "the tree pair refs resolve against, at the pinned commit")
	dims := fs.Int("dims", 256, "table dims, a multiple of 64")
	pcaSample := fs.Int("pca-sample", 131072, "text targets the PCA is fitted on, picked by hash")
	sif := fs.Float64("sif", 1e-3, "SIF coefficient a in a/(a + p)")
	seed := fs.Uint64("seed", 0, "the one seed every shuffle draws from")
	pipeline := fs.String("pipeline", "", "the commit the pipeline ran at")
	run := fs.String("run", "", "this run's id, recorded in the table and runs.jsonl")
	out := fs.String("out", "", "where to write the table")
	def := distill.DefaultTrainOptions(256)
	epochs := fs.Int("epochs", def.Epochs, "at most this many epochs")
	lr := fs.Float64("lr", def.LearningRate, "Adam learning rate")
	lambda := fs.Float64("lambda", def.Lambda, "weight of the MNRL term")
	tau := fs.Float64("tau", def.Temperature, "MNRL temperature")
	mseBatch := fs.Int("mse-batch", def.MSEBatch, "texts per MSE batch")
	pairBatch := fs.Int("pair-batch", def.PairBatch, "pairs per MNRL batch")
	patience := fs.Int("patience", def.Patience, "epochs without a held-out improvement before stopping")
	workers := fs.Int("workers", min(def.Workers, runtime.NumCPU()), "goroutines for training")
	_ = fs.Parse(args)

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for _, name := range []string{"token-run", "texts-run", "seed", "pipeline", "run", "out"} {
		if !set[name] {
			return fmt.Errorf("-%s is required", name)
		}
	}
	if (*pairsPath == "") != (*lambda == 0) {
		return errors.New("pass -pairs with a positive -lambda, or -lambda 0 with no -pairs")
	}

	v, err := vf.build()
	if err != nil {
		return err
	}
	tt, err := tokenTexts(v)
	if err != nil {
		return err
	}
	tokens, err := readTeacherRun(*tokenRun, sha256Hex(tt), v.Len())
	if err != nil {
		return fmt.Errorf("token run: %w", err)
	}
	ids, err := textIDs(vf.texts)
	if err != nil {
		return err
	}
	textsDigest, err := teacher.FileSHA256(vf.texts)
	if err != nil {
		return err
	}
	targets, err := readTeacherRun(*textsRun, textsDigest, len(ids))
	if err != nil {
		return fmt.Errorf("texts run: %w", err)
	}
	if a, b := tokens.spec, targets.spec; a.Model != b.Model || a.Revision != b.Revision || a.WeightsSHA256 != b.WeightsSHA256 || a.Dim != b.Dim {
		return fmt.Errorf("the token run used %s@%s and the texts run %s@%s: one teacher must embed both", a.Model, a.Revision, b.Model, b.Revision)
	}

	var sample [][]float32
	for _, i := range distill.SampleByHash(ids, *pcaSample, pcaSalt) {
		sample = append(sample, targets.vecs[i])
	}
	pca, err := distill.FitPCA(sample, *dims)
	if err != nil {
		return err
	}
	dirs := make([][]float64, v.Len())
	for i, vec := range tokens.vecs {
		dirs[i] = pca.Project(vec)
	}
	weights, err := distill.CountSIF(v, *sif)
	if err != nil {
		return err
	}
	m, err := distill.NewModel(dirs, weights)
	if err != nil {
		return err
	}
	row := make(map[string]int, len(ids))
	for i, id := range ids {
		row[id] = i
	}
	examples, empty, err := distill.Examples(v, jsonlTexts{vf.texts}, func(id string) ([]float64, bool) {
		i, ok := row[id]
		if !ok {
			return nil, false
		}
		return pca.Project(targets.vecs[i]), true
	})
	if err != nil {
		return err
	}
	alpha, err := m.FitScale(examples)
	if err != nil {
		return err
	}

	pairsDigest := "none"
	var pairs []distill.Pair
	var skipped int
	if *pairsPath != "" {
		if pairsDigest, err = teacher.FileSHA256(*pairsPath); err != nil {
			return err
		}
		if pairs, skipped, err = distill.BagPairs(v, jsonlPairs{path: *pairsPath, root: *root}); err != nil {
			return err
		}
	}
	fmt.Printf("vocab %d; %d examples (%d with no vocabulary token); %d pairs (%d skipped); PCA %d -> %d keeps %.1f%% of the sample's variance; scale x%.3f\n",
		v.Len(), len(examples), empty, len(pairs), skipped, len(pca.Mean), pca.Dims(), 100*explained(pca, sample), alpha)

	opts := distill.DefaultTrainOptions(*dims)
	opts.Seed, opts.Epochs, opts.LearningRate, opts.Lambda, opts.Temperature = *seed, *epochs, *lr, *lambda, *tau
	opts.MSEBatch, opts.PairBatch, opts.Patience, opts.Workers = *mseBatch, *pairBatch, *patience, *workers
	rep, err := distill.Train(m, examples, pairs, opts)
	if err != nil {
		return err
	}
	for i, ep := range rep.Epochs {
		fmt.Printf("epoch %d: loss %.6f (MSE %.6f, MNRL %.4f), held-out MSE %.6f, %d pairs dropped\n", i, ep.Loss, ep.MSE, ep.MNRL, ep.Valid, ep.DroppedPairs)
	}

	var buf bytes.Buffer
	err = distill.WriteTable(&buf, m, v, textembed.Provenance{
		Teacher: tokens.spec.Model, TeacherRevision: tokens.spec.Revision,
		TextsDigest: textsDigest, PairsDigest: pairsDigest,
		Pipeline: *pipeline, Run: *run, GOARCH: runtime.GOARCH, Seed: *seed,
	})
	if err != nil {
		return err
	}
	if _, err := textembed.ReadTable(buf.Bytes()); err != nil {
		return fmt.Errorf("the table just encoded does not read back: %w", err)
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("best epoch %d of %d, %d steps; table %s: %d bytes, sha256 %s\n", rep.BestEpoch, len(rep.Epochs), rep.Steps, *out, buf.Len(), sha256Hex(buf.Bytes()))
	return nil
}

func textIDs(path string) ([]string, error) {
	var ids []string
	for t, err := range (jsonlTexts{path}).Texts() {
		if err != nil {
			return nil, err
		}
		ids = append(ids, t.ID)
	}
	return ids, nil
}

// explained is the share of the sample's total variance the PCA's components keep.
func explained(p *distill.PCA, sample [][]float32) float64 {
	var total float64
	for _, r := range sample {
		for j, x := range r {
			d := float64(x) - p.Mean[j]
			total += d * d
		}
	}
	total /= float64(len(sample) - 1)
	var kept float64
	for _, v := range p.Variances {
		kept += v
	}
	return kept / total
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
