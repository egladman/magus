package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/gomlx/compute/dtypes/float16"
	"github.com/gomlx/gomlx/core/tensors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/libs/embed/train/teacher"
)

const convertRevision = "0123456789abcdef0123456789abcdef01234567"

var convertVocab = []string{"[PAD]", "[UNK]", "parse", "##r", "config", "_", "load", "x"}

const convertDims = 64

// convertRow is row r of the fixture's embedding matrix; every value is exact in F16.
func convertRow(r int) []float32 {
	row := make([]float32, convertDims)
	for j := range row {
		row[j] = float32(j*(r+1)%7-3) / 4
	}
	return row
}

func writeConvertTokenizer(t *testing.T, path string) {
	t.Helper()
	vocab := map[string]int{}
	for i, tok := range convertVocab {
		vocab[tok] = i
	}
	b, err := json.Marshal(map[string]any{
		"added_tokens":   []map[string]any{{"id": 1, "content": "[UNK]", "normalized": false}},
		"normalizer":     map[string]any{"type": "BertNormalizer", "clean_text": true, "handle_chinese_chars": true, "strip_accents": nil, "lowercase": true},
		"pre_tokenizer":  map[string]any{"type": "BertPreTokenizer"},
		"post_processor": nil,
		"model": map[string]any{"type": "WordPiece", "unk_token": "[UNK]", "continuing_subword_prefix": "##",
			"max_input_chars_per_word": 100, "vocab": vocab},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o644))
}

func writeConvertSafetensors(t *testing.T, path string) {
	t.Helper()
	var data []byte
	for r := range convertVocab {
		for _, x := range convertRow(r) {
			data = binary.LittleEndian.AppendUint16(data, float16.FromFloat32(x).Bits())
		}
	}
	hb, err := json.Marshal(map[string]any{"embeddings": map[string]any{
		"dtype": "F16", "shape": []int{len(convertVocab), convertDims}, "data_offsets": []int{0, len(data)}}})
	require.NoError(t, err)
	out := binary.LittleEndian.AppendUint64(nil, uint64(len(hb)))
	require.NoError(t, os.WriteFile(path, append(append(out, hb...), data...), 0o644))
}

// convertFixture writes potion's two pinned files under a cache root, the inputs.jsonl
// pinning them, and the vocabulary a table is converted over.
type convertFixture struct {
	cache, inputs, vocab string
}

func newConvertFixture(t *testing.T) convertFixture {
	t.Helper()
	dir := t.TempDir()
	f := convertFixture{cache: filepath.Join(dir, "cache"), inputs: filepath.Join(dir, "inputs.jsonl"),
		vocab: filepath.Join(dir, "vocab.jsonl")}
	snap := filepath.Join("hf", potion, convertRevision)
	require.NoError(t, os.MkdirAll(filepath.Join(f.cache, snap), 0o755))
	writeConvertTokenizer(t, filepath.Join(f.cache, snap, "tokenizer.json"))
	writeConvertSafetensors(t, filepath.Join(f.cache, snap, "model.safetensors"))
	var lines []byte
	for _, name := range []string{"tokenizer.json", "model.safetensors"} {
		rel := filepath.Join(snap, name)
		sum, err := teacher.FileSHA256(filepath.Join(f.cache, rel))
		require.NoError(t, err)
		b, err := json.Marshal(inputRecord{Name: potion + "/" + name, Source: "https://example.com/" + name,
			Revision: convertRevision, SHA256: sum, License: "MIT", Path: rel})
		require.NoError(t, err)
		lines = append(append(lines, b...), '\n')
	}
	require.NoError(t, os.WriteFile(f.inputs, lines, 0o644))
	vocab := `{"token":"config","count":2}` + "\n" + `{"token":"load","count":1}` + "\n" + `{"token":"parse","count":2}` + "\n" +
		`{"token":"parse_config","count":2}` + "\n" + `{"token":"parser","count":2}` + "\n"
	require.NoError(t, os.WriteFile(f.vocab, []byte(vocab), 0o644))
	return f
}

func (f convertFixture) args(out string) []string {
	return []string{"-inputs", f.inputs, "-cache", f.cache, "-vocab", f.vocab, "-run", "convert-test", "-out", out}
}

func TestConvertWritesTheSameTableEveryRun(t *testing.T) {
	f := newConvertFixture(t)
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.mset"), filepath.Join(dir, "second.mset")
	require.NoError(t, runConvert(f.args(first)))
	require.NoError(t, runConvert(f.args(second)))
	a, err := os.ReadFile(first)
	require.NoError(t, err)
	b, err := os.ReadFile(second)
	require.NoError(t, err)
	assert.Equal(t, a, b, "two runs over the same inputs wrote different bytes")

	table, err := embed.ReadTable(a)
	require.NoError(t, err)
	vocabDigest, err := teacher.FileSHA256(f.vocab)
	require.NoError(t, err)
	got := table.Provenance()
	assert.NotEmpty(t, got.TokenizerDigest)
	assert.Equal(t, embed.Provenance{
		Format: got.Format, Teacher: potion, TeacherRevision: convertRevision, TextsDigest: vocabDigest,
		PairsDigest: "none", TokenizerDigest: got.TokenizerDigest, Pipeline: convertPipeline, Run: "convert-test",
		GOARCH: convertGOARCH,
	}, got)
	assert.Equal(t, convertDims, table.Dims())
	assert.Equal(t, 5, table.Len(), "parser, config, parse_config, parse and load")
}

// Every architecture must write these bytes; a fused multiply-add in the float math
// changes them.
func TestConvertWritesPinnedBytes(t *testing.T) {
	f := newConvertFixture(t)
	out := filepath.Join(t.TempDir(), "table.mset")
	require.NoError(t, runConvert(f.args(out)))
	b, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "PENDING", sha256Hex(b))
}

func TestReadVocabRanksByCount(t *testing.T) {
	f := newConvertFixture(t)
	v, err := readVocab(f.vocab)
	require.NoError(t, err)
	assert.Equal(t, []string{"config", "parse", "parse_config", "parser", "load"}, v.Tokens)
	assert.Equal(t, []int64{2, 2, 2, 2, 1}, v.Counts)
	assert.Equal(t, int64(9), v.Total)

	b, err := vocabJSONL(v)
	require.NoError(t, err)
	written, err := os.ReadFile(f.vocab)
	require.NoError(t, err)
	assert.Equal(t, string(written), string(b), "vocabJSONL sorts by token")
}

func TestConvertRefusesAnUnfetchedInput(t *testing.T) {
	f := newConvertFixture(t)
	require.NoError(t, os.Remove(filepath.Join(f.cache, "hf", potion, convertRevision, "model.safetensors")))
	err := runConvert(f.args(filepath.Join(t.TempDir(), "table.mset")))
	assert.ErrorContains(t, err, "embed fetch "+potion+"/model.safetensors")
}

func TestPieceModelMeansEachTokensPieces(t *testing.T) {
	f := newConvertFixture(t)
	tok, emb, rev, err := loadPotion(f.inputs, f.cache)
	require.NoError(t, err)
	assert.Equal(t, convertRevision, rev)

	tokens := []string{"parse_config", "parser", "load"}
	m, pieces, err := pieceModel(tokens, tok, emb)
	require.NoError(t, err)
	assert.Equal(t, 3+2+1, pieces)
	for i, rows := range [][]int{{2, 5, 4}, {2, 3}, {6}} {
		mean := make([]float64, convertDims)
		for _, r := range rows {
			for j, x := range convertRow(r) {
				mean[j] += float64(x) / float64(len(rows))
			}
		}
		var norm float64
		for _, x := range mean {
			norm += x * x
		}
		norm = math.Sqrt(norm)
		assert.InDelta(t, float64(len(rows))*norm, m.Weight(i), 1e-9, "weight of %s", tokens[i])
		for j, x := range m.Direction(i) {
			require.InDelta(t, mean[j]/norm, x, 1e-12, fmt.Sprintf("direction of %s, dim %d", tokens[i], j))
		}
	}
}

func TestPieceModelRefusesAZeroMean(t *testing.T) {
	f := newConvertFixture(t)
	tok, emb, _, err := loadPotion(f.inputs, f.cache)
	require.NoError(t, err)
	flat, err := tensors.CopyFlatData[float32](emb)
	require.NoError(t, err)
	clear(flat[1*convertDims : 2*convertDims])
	zeroUnk := tensors.FromFlatDataAndDimensions(flat, len(convertVocab), convertDims)
	_, _, err = pieceModel([]string{"load", "zzz"}, tok, zeroUnk)
	assert.ErrorContains(t, err, `token "zzz": the mean of its pieces [1] is zero`)
}
