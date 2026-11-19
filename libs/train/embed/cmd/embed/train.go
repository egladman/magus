package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/libs/train/embed/distill"
	"github.com/egladman/magus/libs/train/embed/teacher"
)

// pcaSalt picks the training texts whose targets PCA is fitted on.
const pcaSalt = "train.embed-pca/v1alpha1:"

// vocabFlags are the flags vocab and train share: train rebuilds the vocabulary from
// them and refuses a token run that embedded another.
// trainOpts holds the flags of the one command a process runs; vocab and train share its vocabulary flags.
var trainOpts trainFlags

type vocabFlags struct {
	texts, forced            string
	minCount, maxBytes, size int
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

// runVocab builds the vocabulary and writes it twice: as the texts file the token
// teacher run embeds, and as the token counts convert reads.
func runVocab(vf *vocabFlags, outFlag, vocabFlag string) error {
	out, vocabOut := &outFlag, &vocabFlag
	if *out == "" || *vocabOut == "" {
		return errors.New("-out and -vocab are required")
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
	vb, err := vocabJSONL(v)
	if err != nil {
		return err
	}
	if err := writeAtomic(*vocabOut, vb); err != nil {
		return err
	}
	fmt.Printf("%d tokens over %d counted; token texts %s; vocabulary %s\n", v.Len(), v.Total, sha256Hex(b), sha256Hex(vb))
	return nil
}

// runTrain distills the table: it rebuilds the vocabulary, reads the teacher runs over
// the tokens and the training texts, fits PCA on a hash sample of the texts' targets,
// starts the student from the projected token vectors with count SIF weights at the
// least-squares scale, trains it, and writes the quantized table.
func runTrain(f *trainFlags) error {
	if err := f.check(); err != nil {
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
	sifText, seedText, lrText, lambdaText, tauText      string
	sif, lr, lambda, tau                                float64
	seed                                                uint64
	epochs, mseBatch, pairBatch, patience, workers      int
}

// check refuses a run missing a flag the table's provenance names, and pairs without
// an MNRL weight or the reverse. It reads the float and seed flags, which bind as text.
// check refuses a run missing a flag the table's provenance names, and pairs without
// an MNRL weight or the reverse.
func (f *trainFlags) check() error {
	if err := f.required(); err != nil {
		return err
	}
	var err error
	if f.seed, err = strconv.ParseUint(f.seedText, 10, 64); err != nil {
		return fmt.Errorf("-seed: %q is not a whole number", f.seedText)
	}
	if err = f.floats(); err != nil {
		return err
	}
	if (f.pairs == "") != (f.lambda == 0) {
		return errors.New("pass -pairs with a positive -lambda, or -lambda 0 with no -pairs")
	}
	return nil
}

// required names the first flag the table's provenance needs that the run left empty.
func (f *trainFlags) required() error {
	for _, r := range []struct{ name, value string }{
		{"token-run", f.tokenRun}, {"texts-run", f.textsRun}, {"seed", f.seedText},
		{"pipeline", f.pipeline}, {"run", f.run}, {"out", f.out},
	} {
		if r.value == "" {
			return fmt.Errorf("-%s is required", r.name)
		}
	}
	return nil
}

// floats reads the flags that bind as text, since command.Flag binds no float.
func (f *trainFlags) floats() error {
	var err error
	if f.sif, err = parseFloat("sif", f.sifText); err != nil {
		return err
	}
	if f.lr, err = parseFloat("lr", f.lrText); err != nil {
		return err
	}
	return f.weights()
}

// weights reads the MNRL flags that bind as text.
func (f *trainFlags) weights() error {
	var err error
	if f.lambda, err = parseFloat("lambda", f.lambdaText); err != nil {
		return err
	}
	f.tau, err = parseFloat("tau", f.tauText)
	return err
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

// vocabOpts holds the output flags of vocab.
var vocabOpts struct{ out, vocab string }

var vocabFlagList = []command.Flag{
	{Name: "texts", Kind: command.String, Doc: "the training texts, JSON lines of {\"id\", \"text\"}, as embed-mine writes them under libs/train/embed/gen/", Target: &trainOpts.vocab.texts},
	{Name: "forced", Kind: command.String, Doc: "forced tokens, one per line; every token embed.Tokens yields from a line is kept", Target: &trainOpts.vocab.forced},
	{Name: "min-count", Kind: command.Int, Default: 2, Doc: "a counted token below this count is left out", Target: &trainOpts.vocab.minCount},
	{Name: "max-bytes", Kind: command.Int, Default: 32, Doc: "a counted token longer than this is left out", Target: &trainOpts.vocab.maxBytes},
	{Name: "size", Kind: command.Int, Default: 29000, Doc: "vocabulary size, forced tokens included", Target: &trainOpts.vocab.size},
}

var vocabCmd = command.Command{
	Name:   "vocab",
	Short:  "build the vocabulary; write its token texts for a teacher run and its counts for convert",
	Effect: command.EffectLocalWrite,
	Flags: slices.Concat(vocabFlagList, []command.Flag{
		{Name: "out", Kind: command.String, Doc: "where to write the token texts for `embed teacher -texts`", Target: &vocabOpts.out},
		{Name: "vocab", Kind: command.String, Doc: "where to write the vocabulary for `embed convert -vocab`", Target: &vocabOpts.vocab},
	}),
	Run: func(_ context.Context, _ command.Invocation) error {
		return runVocab(&trainOpts.vocab, vocabOpts.out, vocabOpts.vocab)
	},
}

// trainDefaults are the optimizer settings the train flags default to.
var trainDefaults = distill.DefaultTrainOptions(256)

// floatText is the text a float flag declares as its default.
func floatText(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

var trainCmd = command.Command{
	Name:   "train",
	Short:  "distill, train and quantize the table from finished teacher runs",
	Effect: command.EffectLocalWrite,
	Flags: slices.Concat(vocabFlagList, []command.Flag{
		{Name: "token-run", Kind: command.String, Doc: "the teacher run directory over `embed vocab`'s token texts; required", Target: &trainOpts.tokenRun},
		{Name: "texts-run", Kind: command.String, Doc: "the teacher run directory over -texts; required", Target: &trainOpts.textsRun},
		{Name: "pairs", Kind: command.String, Doc: "pairs JSON lines (libs/train/embed/gen/pairs.jsonl); required unless -lambda 0", Target: &trainOpts.pairs},
		{Name: "root", Kind: command.String, Default: ".", Doc: "the tree pair refs resolve against, at the pinned commit", Target: &trainOpts.root},
		{Name: "dims", Kind: command.Int, Default: 256, Doc: "table dims, a multiple of 64", Target: &trainOpts.dims},
		{Name: "pca-sample", Kind: command.Int, Default: 131072, Doc: "text targets the PCA is fitted on, picked by hash", Target: &trainOpts.pcaSample},
		{Name: "sif", Kind: command.String, Default: floatText(1e-3), Doc: "SIF coefficient a in a/(a + p)", Target: &trainOpts.sifText},
		{Name: "seed", Kind: command.String, Doc: "the one seed every shuffle draws from; required", Target: &trainOpts.seedText},
		{Name: "pipeline", Kind: command.String, Doc: "the commit the pipeline ran at; required", Target: &trainOpts.pipeline},
		{Name: "run", Kind: command.String, Doc: "this run's id, recorded in the table and runs.jsonl; required", Target: &trainOpts.run},
		{Name: "out", Kind: command.String, Doc: "where to write the table; required", Target: &trainOpts.out},
		{Name: "epochs", Kind: command.Int, Default: trainDefaults.Epochs, Doc: "at most this many epochs", Target: &trainOpts.epochs},
		{Name: "lr", Kind: command.String, Default: floatText(trainDefaults.LearningRate), Doc: "Adam learning rate", Target: &trainOpts.lrText},
		{Name: "lambda", Kind: command.String, Default: floatText(trainDefaults.Lambda), Doc: "weight of the MNRL term", Target: &trainOpts.lambdaText},
		{Name: "tau", Kind: command.String, Default: floatText(trainDefaults.Temperature), Doc: "MNRL temperature", Target: &trainOpts.tauText},
		{Name: "mse-batch", Kind: command.Int, Default: trainDefaults.MSEBatch, Doc: "texts per MSE batch", Target: &trainOpts.mseBatch},
		{Name: "pair-batch", Kind: command.Int, Default: trainDefaults.PairBatch, Doc: "pairs per MNRL batch", Target: &trainOpts.pairBatch},
		{Name: "patience", Kind: command.Int, Default: trainDefaults.Patience, Doc: "epochs without a held-out improvement before stopping", Target: &trainOpts.patience},
		{Name: "workers", Kind: command.Int, Default: min(trainDefaults.Workers, runtime.NumCPU()), Doc: "goroutines for training", Target: &trainOpts.workers},
	}),
	Run: func(_ context.Context, _ command.Invocation) error { return runTrain(&trainOpts) },
}
