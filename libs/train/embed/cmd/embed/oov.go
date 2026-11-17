package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/libs/embed/rankeval"
)

// oovRepo is a pinned repository whose source files the oov command tokenizes. name is
// the inputs.jsonl row; the set in evals.jsonl is "oov-<language>-<repo>".
type oovRepo struct {
	name     string
	language string
	exts     []string
}

var oovRepos = []oovRepo{
	{"oov/requests", "python", []string{".py"}},
	{"oov/vite", "typescript", []string{".ts", ".tsx", ".mts", ".cts", ".js", ".mjs", ".cjs"}},
	{"oov/ripgrep", "rust", []string{".rs"}},
	{"oov/guava", "java", []string{".java"}},
	{"oov/Newtonsoft.Json", "csharp", []string{".cs"}},
}

// oovSystem is the table whose vocabulary is measured, as evals.jsonl names it.
const oovSystem = "potion-code-16M-v2-converted"

const oovMetric = "oov-rate"

func (r oovRepo) set() string {
	return "oov-" + r.language + "-" + path.Base(r.name)
}

// oovFile counts one source file's tokens, and those with no row in the vocabulary.
type oovFile struct{ tokens, missing int }

// scanOOV reads a gzipped tarball and counts, per file whose extension is in exts, the
// embed.Tokens that are absent from vocab. Entries other than regular files, such as
// GitHub's pax header, are skipped.
func scanOOV(r io.Reader, exts []string, vocab map[string]bool) ([]oovFile, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	var files []oovFile
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg || !slices.Contains(exts, strings.ToLower(path.Ext(h.Name))) {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", h.Name, err)
		}
		var f oovFile
		for _, tok := range embed.Tokens(string(body)) {
			f.tokens++
			if !vocab[tok] {
				f.missing++
			}
		}
		if f.tokens > 0 {
			files = append(files, f)
		}
	}
}

// oovRecord is the evals.jsonl row for one repo. Queries carries the token count, the
// rate's denominator, and Export the commit the tarball is pinned at.
func oovRecord(r oovRepo, in inputRecord, tableDigest string, files []oovFile) (rankeval.EvalRecord, error) {
	clusters := make([]rankeval.Cluster, len(files))
	for i, f := range files {
		clusters[i] = rankeval.Cluster{Trials: f.tokens, Hits: f.missing}
	}
	rate, low, high, tokens, err := rankeval.ClusterRatio(clusters)
	if err != nil {
		return rankeval.EvalRecord{}, fmt.Errorf("%s: %w", r.name, err)
	}
	return rankeval.EvalRecord{System: oovSystem, TableDigest: tableDigest, Set: r.set(), Metric: oovMetric,
		Value: rate, Low: low, High: high, Queries: tokens, Export: in.Revision}, nil
}

// loadShippedVocab returns the token set of gen/vocab.jsonl and the digest of the
// converted table beside it. It refuses a table that is not the pinned one, and one
// whose size differs from the vocabulary, so the rows name the table that ships.
func loadShippedVocab(vocabPath, tablePath string) (map[string]bool, string, error) {
	v, err := readVocab(vocabPath)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(tablePath)
	if err != nil {
		return nil, "", fmt.Errorf("the convert target writes the table: %w", err)
	}
	pin, err := embed.Pin()
	if err != nil {
		return nil, "", err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != pin {
		return nil, "", fmt.Errorf("%s has sha256 %x, gen/table.digest pins %s: rerun the convert target", tablePath, sum, pin)
	}
	table, err := embed.ReadTable(data)
	if err != nil {
		return nil, "", err
	}
	if table.Len() != v.Len() {
		return nil, "", fmt.Errorf("%s holds %d tokens, %s lists %d", tablePath, table.Len(), vocabPath, v.Len())
	}
	set := make(map[string]bool, v.Len())
	for _, tok := range v.Tokens {
		set[tok] = true
	}
	return set, table.Digest(), nil
}

func runOOV(args []string) error {
	fs := flag.NewFlagSet("oov", flag.ExitOnError)
	var c common
	c.register(fs)
	vocabPath := fs.String("vocab", filepath.Join("embed", "gen", "vocab.jsonl"), "the shipped vocabulary")
	tablePath := fs.String("table", "", "the converted table; default <cache>/potion-code-16M-v2.table")
	evals := fs.String("evals", filepath.Join("..", "embed", "evals.jsonl"), "the eval records file the rates are appended to")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: embed oov [flags] [repo ...]\n\nfor each pinned repo (default all), appends the share of identifier tokens with no vocabulary row to evals.jsonl")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if *tablePath == "" {
		*tablePath = filepath.Join(c.cache, "potion-code-16M-v2.table")
	}
	inputs, err := readInputs(c.inputs)
	if err != nil {
		return err
	}
	vocab, digest, err := loadShippedVocab(*vocabPath, *tablePath)
	if err != nil {
		return err
	}
	for _, r := range oovRepos {
		if fs.NArg() > 0 && !slices.Contains(fs.Args(), path.Base(r.name)) {
			continue
		}
		in, err := findInput(inputs, r.name)
		if err != nil {
			return err
		}
		archive, err := verify(c.cache, in)
		if err != nil {
			return err
		}
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		files, err := scanOOV(f, r.exts, vocab)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", r.name, err)
		}
		rec, err := oovRecord(r, in, digest, files)
		if err != nil {
			return err
		}
		if err := rankeval.AppendEvalRecord(*evals, rec); err != nil {
			return err
		}
		fmt.Printf("%-18s %-10s %9d tokens in %5d files  oov %.4f [%.4f, %.4f]\n", r.name, r.language, rec.Queries, len(files), rec.Value, rec.Low, rec.High)
	}
	return nil
}
