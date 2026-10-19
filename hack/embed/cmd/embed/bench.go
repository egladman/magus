package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/egladman/magus/hack/embed/teacher"
	"github.com/egladman/magus/internal/rankeval"
)

// benchQueryPrompt is the query instruction CodeRankEmbed's model card requires.
const benchQueryPrompt = "Represent this query for searching relevant code: "

// benchDepth is how many docs a run keeps per query, the external harness's depth.
const benchDepth = 100

// runBench writes CodeRankEmbed's runs over one subset the external harness exported
// (internal/textembed/eval_test.go) into its runs/<digest>/, where the harness
// fuses and scores them. One corpus encoding serves two runs: queries with the model
// card's prompt, the teacher as published, and prompt-less, the symmetric model the
// student is distilled toward. Every text goes through resumable teacher shards under
// the subset's teacher/, so a stopped run resumes at its last whole shard.
func runBench(args []string) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	var c common
	c.register(fs)
	subset := fs.String("subset", "", "a subset directory the external harness exported, holding queries.jsonl and corpus.jsonl")
	shardSize := fs.Int("shard-size", 1024, "texts per teacher shard")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *subset == "" {
		return errors.New("-subset is required")
	}
	if c.model != "CodeRankEmbed" {
		return fmt.Errorf("-model %q is not CodeRankEmbed: the harness runs potion itself, and nomic-embed-text-v1.5 waits on its reference parity", c.model)
	}
	digest, err := exportDigest(*subset)
	if err != nil {
		return err
	}
	queries, err := teacher.ReadTexts(filepath.Join(*subset, "queries.jsonl"))
	if err != nil {
		return err
	}
	docs, err := teacher.ReadTexts(filepath.Join(*subset, "corpus.jsonl"))
	if err != nil {
		return err
	}
	prompted := make([]teacher.Text, len(queries))
	for i, q := range queries {
		prompted[i] = teacher.Text{ID: q.ID, Text: benchQueryPrompt + q.Text}
	}
	if err := writeTexts(filepath.Join(*subset, "queries-prompted.jsonl"), prompted); err != nil {
		return err
	}
	l, err := c.load()
	if err != nil {
		return err
	}
	defer l.enc.Close()
	vecs := map[string][][]float32{}
	for _, name := range []string{"queries.jsonl", "queries-prompted.jsonl", "corpus.jsonl"} {
		if vecs[name], err = teacherVectors(l, filepath.Join(*subset, name), filepath.Join(*subset, "teacher"), *shardSize); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	runs := filepath.Join(*subset, "runs", digest)
	for _, arm := range []struct{ tag, queries string }{
		{"CodeRankEmbed-query-prompt", "queries-prompted.jsonl"},
		{"CodeRankEmbed-symmetric", "queries.jsonl"},
	} {
		if err := writeRun(runs, denseRun(arm.tag, queries, vecs[arm.queries], docs, vecs["corpus.jsonl"])); err != nil {
			return err
		}
	}
	return nil
}

// exportDigest is the first 16 hex of the sha256 of queries.jsonl followed by
// corpus.jsonl, the name the external harness gives the export's runs directory.
func exportDigest(dir string) (string, error) {
	sum := sha256.New()
	for _, name := range []string{"queries.jsonl", "corpus.jsonl"} {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		_, err = io.Copy(sum, f)
		f.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(sum.Sum(nil))[:16], nil
}

// teacherVectors encodes a texts file through teacher.Run, resuming any finished
// shards, and reads every vector back in input order. The query prompt sits in the
// texts, not the spec's prefix, so the texts digest keys it.
func teacherVectors(l *loaded, path, root string, shardSize int) ([][]float32, error) {
	texts, err := teacher.ReadTexts(path)
	if err != nil {
		return nil, err
	}
	sha, err := teacher.FileSHA256(path)
	if err != nil {
		return nil, err
	}
	spec := l.spec
	spec.ShardSize, spec.TextsSHA256 = shardSize, sha
	dir := filepath.Join(root, spec.Key())
	st, err := teacher.Run(dir, spec, texts, l.enc, 0, func(r teacher.Record) {
		fmt.Fprintf(os.Stderr, "%s shard %d: %d texts, %.1f texts/s\n", filepath.Base(path), r.Shard, r.Rows, float64(r.Rows)/r.Seconds)
	})
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "%s: %d shards, %d resumed, %d texts encoded in %s\n", filepath.Base(path), st.Shards, st.Skipped, st.Texts, st.Elapsed.Round(time.Second))
	run, err := readTeacherRun(dir, sha, len(texts))
	if err != nil {
		return nil, err
	}
	return run.vecs, nil
}

// denseRun ranks docs for every query by the dot product of their vectors, which the
// teacher L2-normalizes, keeping the best benchDepth by rankeval.CompareHits. A zero
// vector is never ranked and ranks nothing. The external harness ranks potion the same
// way; the root module cannot import this one.
func denseRun(tag string, queries []teacher.Text, qv [][]float32, docs []teacher.Text, dv [][]float32) rankeval.TRECRun {
	hits := make([][]rankeval.Hit, len(queries))
	next := make(chan int)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for i := range next {
				if isZero(qv[i]) {
					continue
				}
				hits[i] = rankeval.Top(func(yield func(rankeval.Hit) bool) {
					for j, d := range dv {
						if !isZero(d) && !yield(rankeval.Hit{Doc: docs[j].ID, Score: dot(qv[i], d)}) {
							return
						}
					}
				}, benchDepth)
			}
		})
	}
	for i := range queries {
		next <- i
	}
	close(next)
	wg.Wait()
	run := rankeval.TRECRun{Tag: tag, Queries: map[string][]rankeval.Hit{}}
	for i, q := range queries {
		if len(hits[i]) > 0 {
			run.Queries[q.ID] = hits[i]
		}
	}
	return run
}

func dot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func isZero(v []float32) bool {
	return !slices.ContainsFunc(v, func(x float32) bool { return x != 0 })
}

func writeRun(dir string, run rankeval.TRECRun) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b bytes.Buffer
	if err := rankeval.WriteRun(&b, run); err != nil {
		return err
	}
	path := filepath.Join(dir, run.Tag+".trec")
	if err := writeAtomic(path, b.Bytes()); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d queries ranked\n", path, len(run.Queries))
	return nil
}

// writeTexts writes {"id", "text"} lines, leaving a file that already holds them
// untouched so its digest, and the teacher shards keyed on it, stay valid.
func writeTexts(path string, texts []teacher.Text) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for _, t := range texts {
		if err := enc.Encode(t); err != nil {
			return err
		}
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b.Bytes()) {
		return nil
	}
	return writeAtomic(path, b.Bytes())
}
