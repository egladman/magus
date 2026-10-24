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

	"github.com/egladman/magus/libs/embed/rankeval"
	"github.com/egladman/magus/libs/embed/train/teacher"
)

// benchPrompt is one teacher's query and document prompts. bench puts them in the texts
// it encodes, so the encoder itself runs with no prefix and the texts digest keys them.
type benchPrompt struct{ query, doc string }

// benchPrompts are from each model card: CodeRankEmbed prompts only queries, with
// "Represent this query for searching relevant code: "; nomic-embed-text-v1.5's card
// (huggingface.co/nomic-ai/nomic-embed-text-v1.5, "Task instruction prefixes") prefixes
// queries with "search_query: " and documents with "search_document: ".
var benchPrompts = map[string]benchPrompt{
	"CodeRankEmbed":         {query: "Represent this query for searching relevant code: "},
	"nomic-embed-text-v1.5": {query: "search_query: ", doc: "search_document: "},
}

// benchTags are the two runs bench writes for model: queries with their prompt, and
// queries as documents are, the symmetric arm the student is distilled toward.
func benchTags(model string) (prompted, symmetric string) {
	return model + "-query-prompt", model + "-symmetric"
}

func prefixed(texts []teacher.Text, prefix string) []teacher.Text {
	out := make([]teacher.Text, len(texts))
	for i, t := range texts {
		out[i] = teacher.Text{ID: t.ID, Text: prefix + t.Text}
	}
	return out
}

// benchDepth is how many docs a run keeps per query, the external harness's depth.
const benchDepth = 100

// runBench writes CodeRankEmbed's runs over one subset the external harness exported
// (libs/embed/search_test.go) into its runs/<digest>/, where the harness
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
	prompt, ok := benchPrompts[c.model]
	if !ok {
		return fmt.Errorf("-model %q is not CodeRankEmbed or nomic-embed-text-v1.5: the harness runs potion itself", c.model)
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
	// The prompts sit in the texts, so the encoder takes no prefix of its own; docPrefix
	// holds nomic's document prefix, which would otherwise land on the queries too.
	defer func(old string) { docPrefix[c.model] = old }(docPrefix[c.model])
	docPrefix[c.model] = ""
	files := map[string][]teacher.Text{
		"queries-prompted.jsonl":  prefixed(queries, prompt.query),
		"queries-symmetric.jsonl": prefixed(queries, prompt.doc),
		"corpus-prompted.jsonl":   prefixed(docs, prompt.doc),
	}
	for name, texts := range files {
		if err := writeTexts(filepath.Join(*subset, c.model+"-"+name), texts); err != nil {
			return err
		}
	}
	l, err := c.load()
	if err != nil {
		return err
	}
	defer l.enc.Close()
	vecs := map[string][][]float32{}
	for _, name := range []string{"queries-prompted.jsonl", "queries-symmetric.jsonl", "corpus-prompted.jsonl"} {
		if vecs[name], err = teacherVectors(l, filepath.Join(*subset, c.model+"-"+name), filepath.Join(*subset, "teacher"), *shardSize); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	runs := filepath.Join(*subset, "runs", digest)
	prompted, symmetric := benchTags(c.model)
	for _, arm := range []struct{ tag, queries string }{
		{prompted, "queries-prompted.jsonl"},
		{symmetric, "queries-symmetric.jsonl"},
	} {
		if err := writeRun(runs, denseRun(arm.tag, queries, vecs[arm.queries], docs, vecs["corpus-prompted.jsonl"])); err != nil {
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
