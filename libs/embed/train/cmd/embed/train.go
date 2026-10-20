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

	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/libs/embed/train/distill"
	"github.com/egladman/magus/libs/embed/train/teacher"
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
	fs.StringVar(&f.texts, "texts", "", "the training texts, JSON lines of {\"id\", \"text\"}, as embed-mine writes them under libs/embed/train/gen/")
	fs.StringVar(&f.forced, "forced", "", "forced tokens, one per line; every token embed.Tokens yields from a line is kept")
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
	var f trainFlags
	f.register(fs)
	_ = fs.Parse(args)
	if err := f.check(fs); err != nil {
		return err
	}

	d, err := readTeacherData(&f.vocab, f.tokenRun, f.textsRun)
	if err != nil {
		return err
	}
	sample := d.pcaSample(f.pcaSample)
	pca, err := distill.FitPCA(sample, f.dims)
	if err != nil {
		return err
	}
	m, err := d.initStudent(pca, f.sif)
	if err != nil {
		return err
	}
	examples, empty, err := d.examples(f.vocab.texts, pca)
	if err != nil {
		return err
	}
	alpha, err := m.FitScale(examples)
	if err != nil {
		return err
	}
	pairs, skipped, pairsDigest, err := readPairs(d.v, f.pairs, f.root)
	if err != nil {
		return err
	}
	fmt.Printf("vocab %d; %d examples (%d with no vocabulary token); %d pairs (%d skipped); PCA %d -> %d keeps %.1f%% of the sample's variance; scale x%.3f\n",
		d.v.Len(), len(examples), empty, len(pairs), skipped, len(pca.Mean), pca.Dims(), 100*explained(pca, sample), alpha)

	rep, err := distill.Train(m, examples, pairs, f.trainOptions())
	if err != nil {
		return err
	}
	for i, ep := range rep.Epochs {
		fmt.Printf("epoch %d: loss %.6f (MSE %.6f, MNRL %.4f), held-out MSE %.6f, %d pairs dropped\n", i, ep.Loss, ep.MSE, ep.MNRL, ep.Valid, ep.DroppedPairs)
	}

	table, err := encodeTable(m, d.v, embed.Provenance{
		Teacher: d.tokens.spec.Model, TeacherRevision: d.tokens.spec.Revision,
		TextsDigest: d.textsDigest, PairsDigest: pairsDigest,
		Pipeline: f.pipeline, Run: f.run, GOARCH: runtime.GOARCH, Seed: f.seed,
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(f.out, table, 0o644); err != nil {
		return err
	}
	fmt.Printf("best epoch %d of %d, %d steps; table %s: %d bytes, sha256 %s\n", rep.BestEpoch, len(rep.Epochs), rep.Steps, f.out, len(table), sha256Hex(table))
	return nil
}

// trainFlags are train's flags: the vocabulary's, the teacher runs and pairs it reads,
// the student's initialization, and the optimizer's hyperparameters.
type trainFlags struct {
	vocab                                               vocabFlags
	tokenRun, textsRun, pairs, root, pipeline, run, out string
	dims, pcaSample                                     int
	sif                                                 float64
	seed                                                uint64
	epochs, mseBatch, pairBatch, patience, workers      int
	lr, lambda, tau                                     float64
}

func (f *trainFlags) register(fs *flag.FlagSet) {
	f.vocab.register(fs)
	fs.StringVar(&f.tokenRun, "token-run", "", "the teacher run directory over `embed vocab`'s token texts")
	fs.StringVar(&f.textsRun, "texts-run", "", "the teacher run directory over -texts")
	fs.StringVar(&f.pairs, "pairs", "", "pairs JSON lines (libs/embed/train/gen/pairs.jsonl); required unless -lambda 0")
	fs.StringVar(&f.root, "root", ".", "the tree pair refs resolve against, at the pinned commit")
	fs.IntVar(&f.dims, "dims", 256, "table dims, a multiple of 64")
	fs.IntVar(&f.pcaSample, "pca-sample", 131072, "text targets the PCA is fitted on, picked by hash")
	fs.Float64Var(&f.sif, "sif", 1e-3, "SIF coefficient a in a/(a + p)")
	fs.Uint64Var(&f.seed, "seed", 0, "the one seed every shuffle draws from")
	fs.StringVar(&f.pipeline, "pipeline", "", "the commit the pipeline ran at")
	fs.StringVar(&f.run, "run", "", "this run's id, recorded in the table and runs.jsonl")
	fs.StringVar(&f.out, "out", "", "where to write the table")
	def := distill.DefaultTrainOptions(256)
	fs.IntVar(&f.epochs, "epochs", def.Epochs, "at most this many epochs")
	fs.Float64Var(&f.lr, "lr", def.LearningRate, "Adam learning rate")
	fs.Float64Var(&f.lambda, "lambda", def.Lambda, "weight of the MNRL term")
	fs.Float64Var(&f.tau, "tau", def.Temperature, "MNRL temperature")
	fs.IntVar(&f.mseBatch, "mse-batch", def.MSEBatch, "texts per MSE batch")
	fs.IntVar(&f.pairBatch, "pair-batch", def.PairBatch, "pairs per MNRL batch")
	fs.IntVar(&f.patience, "patience", def.Patience, "epochs without a held-out improvement before stopping")
	fs.IntVar(&f.workers, "workers", min(def.Workers, runtime.NumCPU()), "goroutines for training")
}

// check refuses a run missing a flag the table's provenance names, and pairs without
// an MNRL weight or the reverse.
func (f *trainFlags) check(fs *flag.FlagSet) error {
	set := map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
	for _, name := range []string{"token-run", "texts-run", "seed", "pipeline", "run", "out"} {
		if !set[name] {
			return fmt.Errorf("-%s is required", name)
		}
	}
	if (f.pairs == "") != (f.lambda == 0) {
		return errors.New("pass -pairs with a positive -lambda, or -lambda 0 with no -pairs")
	}
	return nil
}

func (f *trainFlags) trainOptions() distill.TrainOptions {
	opts := distill.DefaultTrainOptions(f.dims)
	opts.Seed, opts.Epochs, opts.LearningRate, opts.Lambda, opts.Temperature = f.seed, f.epochs, f.lr, f.lambda, f.tau
	opts.MSEBatch, opts.PairBatch, opts.Patience, opts.Workers = f.mseBatch, f.pairBatch, f.patience, f.workers
	return opts
}

// teacherData is what the student distills: the vocabulary, one teacher's vectors for
// its tokens and for the training texts, and the texts' ids and digest.
type teacherData struct {
	v               *distill.Vocab
	tokens, targets teacherRun
	ids             []string
	textsDigest     string
}

// readTeacherData rebuilds the vocabulary and reads the teacher runs over its tokens
// and over the training texts, which one teacher must have embedded.
func readTeacherData(vf *vocabFlags, tokenRun, textsRun string) (teacherData, error) {
	var d teacherData
	var err error
	if d.v, err = vf.build(); err != nil {
		return d, err
	}
	tt, err := tokenTexts(d.v)
	if err != nil {
		return d, err
	}
	if d.tokens, err = readTeacherRun(tokenRun, sha256Hex(tt), d.v.Len()); err != nil {
		return d, fmt.Errorf("token run: %w", err)
	}
	if d.ids, err = textIDs(vf.texts); err != nil {
		return d, err
	}
	if d.textsDigest, err = teacher.FileSHA256(vf.texts); err != nil {
		return d, err
	}
	if d.targets, err = readTeacherRun(textsRun, d.textsDigest, len(d.ids)); err != nil {
		return d, fmt.Errorf("texts run: %w", err)
	}
	if a, b := d.tokens.spec, d.targets.spec; a.Model != b.Model || a.Revision != b.Revision || a.WeightsSHA256 != b.WeightsSHA256 || a.Dim != b.Dim {
		return d, fmt.Errorf("the token run used %s@%s and the texts run %s@%s: one teacher must embed both", a.Model, a.Revision, b.Model, b.Revision)
	}
	return d, nil
}

// pcaSample is the targets of the at most n texts SampleByHash picks under pcaSalt.
func (d teacherData) pcaSample(n int) [][]float32 {
	var sample [][]float32
	for _, i := range distill.SampleByHash(d.ids, n, pcaSalt) {
		sample = append(sample, d.targets.vecs[i])
	}
	return sample
}

// initStudent starts the student from each token's teacher vector projected on pca,
// weighted by count SIF with coefficient sif.
func (d teacherData) initStudent(pca *distill.PCA, sif float64) (*distill.Model, error) {
	dirs := make([][]float64, d.v.Len())
	for i, vec := range d.tokens.vecs {
		dirs[i] = pca.Project(vec)
	}
	weights, err := distill.CountSIF(d.v, sif)
	if err != nil {
		return nil, err
	}
	return distill.NewModel(dirs, weights)
}

// examples bags each training text in texts over the vocabulary, its target the text's
// teacher vector projected on pca. empty counts the texts with no vocabulary token.
func (d teacherData) examples(texts string, pca *distill.PCA) (examples []distill.Example, empty int, err error) {
	row := make(map[string]int, len(d.ids))
	for i, id := range d.ids {
		row[id] = i
	}
	return distill.Examples(d.v, jsonlTexts{texts}, func(id string) ([]float64, bool) {
		i, ok := row[id]
		if !ok {
			return nil, false
		}
		return pca.Project(d.targets.vecs[i]), true
	})
}

// readPairs bags the training pairs at path, whose refs resolve against root. With no
// path there are none, and the digest is "none".
func readPairs(v *distill.Vocab, path, root string) (pairs []distill.Pair, skipped int, digest string, err error) {
	if path == "" {
		return nil, 0, "none", nil
	}
	if digest, err = teacher.FileSHA256(path); err != nil {
		return nil, 0, "", err
	}
	pairs, skipped, err = distill.BagPairs(v, jsonlPairs{path: path, root: root})
	return pairs, skipped, digest, err
}

// encodeTable quantizes m into a table and checks that it reads back.
func encodeTable(m *distill.Model, v *distill.Vocab, prov embed.Provenance) ([]byte, error) {
	var buf bytes.Buffer
	if err := distill.WriteTable(&buf, m, v, prov); err != nil {
		return nil, err
	}
	if _, err := embed.ReadTable(buf.Bytes()); err != nil {
		return nil, fmt.Errorf("the table just encoded does not read back: %w", err)
	}
	return buf.Bytes(), nil
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
