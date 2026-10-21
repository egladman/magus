package main

import (
	"errors"
	"flag"
	"fmt"
	"maps"
	"math"
	"slices"

	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/libs/embed/train/distill"
	"github.com/egladman/magus/libs/embed/train/teacher"
	"github.com/gomlx/gomlx/core/tensors"
)

// potion is the model convert reads, pinned by the potion-code-16M-v2/ entries of
// inputs.jsonl.
const potion = "potion-code-16M-v2"

// convertPipeline and convertGOARCH are what a converted table records as its pipeline
// and architecture. Both are fixed: the same vocabulary and potion files write the same
// bytes at any commit and on any architecture, so the pin holds everywhere.
const (
	convertPipeline = "embed convert"
	convertGOARCH   = "any"
)

// runConvert writes potion-code-16M-v2 as a table over the embed vocabulary. Potion
// embeds a text as the normalized sum of its pieces' vectors, so each token stands for
// the sum over its own pieces. embed.Tokens repeats a word's parts, so the table only
// approximates potion. The same inputs and flags write the same bytes.
func runConvert(args []string) error {
	fs := flag.NewFlagSet("convert", flag.ExitOnError)
	vocabPath := fs.String("vocab", "", "the vocabulary, JSON lines of {\"token\", \"count\"} as `embed vocab -vocab` writes them")
	inputs := fs.String("inputs", "inputs.jsonl", "the pinned inputs file")
	cache := fs.String("cache", cacheRoot(), "the cache root inputs.jsonl paths are relative to")
	run := fs.String("run", "", "this run's id, recorded in the table")
	out := fs.String("out", "", "where to write the table")
	_ = fs.Parse(args)
	if *vocabPath == "" || *run == "" || *out == "" {
		return errors.New("-vocab, -run and -out are required")
	}
	if *cache == "" {
		return errors.New("-cache is empty and no cache directory resolved")
	}
	v, err := readVocab(*vocabPath)
	if err != nil {
		return err
	}
	tok, emb, rev, err := loadPotion(*inputs, *cache)
	if err != nil {
		return err
	}
	m, pieces, err := pieceModel(v.Tokens, tok, emb)
	if err != nil {
		return err
	}
	vocabDigest, err := teacher.FileSHA256(*vocabPath)
	if err != nil {
		return err
	}
	table, err := encodeTable(m, v, embed.Provenance{
		Teacher: potion, TeacherRevision: rev, TextsDigest: vocabDigest, PairsDigest: "none",
		Pipeline: convertPipeline, Run: *run, GOARCH: convertGOARCH,
	})
	if err != nil {
		return err
	}
	if err := writeAtomic(*out, table); err != nil {
		return err
	}
	fmt.Printf("%d tokens, %.2f pieces each; table %s: %d bytes, sha256 %s\n",
		v.Len(), float64(pieces)/float64(v.Len()), *out, len(table), sha256Hex(table))
	return nil
}

// loadPotion verifies potion's pinned tokenizer.json and model.safetensors under cache
// and reads them: the tokenizer, its embedding matrix and the pinned revision.
func loadPotion(inputsPath, cache string) (*teacher.WordPiece, *tensors.Tensor, string, error) {
	inputs, err := readInputs(inputsPath)
	if err != nil {
		return nil, nil, "", err
	}
	paths := map[string]string{}
	var rev string
	for _, f := range []string{"tokenizer.json", "model.safetensors"} {
		in, err := findInput(inputs, potion+"/"+f)
		if err != nil {
			return nil, nil, "", err
		}
		if paths[f], err = verify(cache, in); err != nil {
			return nil, nil, "", err
		}
		if rev != "" && in.Revision != rev {
			return nil, nil, "", fmt.Errorf("inputs.jsonl pins %s at %s and %s", potion, rev, in.Revision)
		}
		rev = in.Revision
	}
	tok, err := teacher.LoadTokenizerJSON(paths["tokenizer.json"])
	if err != nil {
		return nil, nil, "", err
	}
	w, err := teacher.ReadSafetensors(paths["model.safetensors"])
	if err != nil {
		return nil, nil, "", err
	}
	emb, ok := w["embeddings"]
	if !ok || len(w) != 1 || emb.Shape().Rank() != 2 {
		return nil, nil, "", fmt.Errorf("%s holds %v, want one 2-D tensor named embeddings", paths["model.safetensors"], slices.Sorted(maps.Keys(w)))
	}
	return tok, emb, rev, nil
}

// pieceModel starts a model whose token t has as its direction the mean of emb's rows
// over t's word pieces and as its weight the piece count times that mean's norm. It
// returns the pieces counted over every token. A token with no piece, a piece past the
// last row or pieces whose mean is zero is an error.
func pieceModel(tokens []string, tok *teacher.WordPiece, emb *tensors.Tensor) (*distill.Model, int, error) {
	rows, dims := emb.Shape().Dimensions[0], emb.Shape().Dimensions[1]
	flat, err := tensors.CopyFlatData[float32](emb)
	if err != nil {
		return nil, 0, err
	}
	dirs := make([][]float64, len(tokens))
	weights := make([]float64, len(tokens))
	var total int
	for i, t := range tokens {
		ids := tok.Pieces(t)
		if len(ids) == 0 {
			return nil, 0, fmt.Errorf("token %q has no word piece", t)
		}
		mean := make([]float64, dims)
		for _, id := range ids {
			if int(id) >= rows {
				return nil, 0, fmt.Errorf("token %q has piece %d, past the %d embedding rows", t, id, rows)
			}
			for j, x := range flat[int(id)*dims : (int(id)+1)*dims] {
				mean[j] += float64(x)
			}
		}
		var ss float64
		for j := range mean {
			mean[j] /= float64(len(ids))
			// The explicit conversion rounds the product, so no architecture fuses it
			// into the add and the table's bytes match everywhere.
			ss = float64(mean[j]*mean[j]) + ss
		}
		if ss == 0 {
			return nil, 0, fmt.Errorf("token %q: the mean of its pieces %v is zero", t, ids)
		}
		dirs[i], weights[i] = mean, float64(len(ids))*math.Sqrt(ss)
		total += len(ids)
	}
	m, err := distill.NewModel(dirs, weights)
	return m, total, err
}
