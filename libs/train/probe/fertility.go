package probe

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Source is one file row of libs/train/embed/gen/sources.jsonl, as far as the probes
// read it.
type Source struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	Language  string `json:"language"`
	Split     string `json:"split"`
	Generated bool   `json:"generated"`
	SHA256    string `json:"sha256"`
}

// HeldIn reads sources.jsonl and returns its hand-written train-split files in the named
// languages, in file order. Holdout and generated files are never returned.
func HeldIn(path string, languages ...string) ([]Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Source
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<24)
	for n := 1; sc.Scan(); n++ {
		var s Source
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if s.Kind == "file" && s.Split == "train" && !s.Generated && slices.Contains(languages, s.Language) {
			out = append(out, s)
		}
	}
	return out, sc.Err()
}

// Text is a held-in file's bytes as read from the tree.
type Text struct {
	Source
	Body []byte
}

// ReadHeldIn reads each source under root. A file whose bytes no longer hash to the
// sha256 sources.jsonl recorded is left out and counted in stale, so a measurement
// covers exactly the split `magus run generate libs/train` dealt.
func ReadHeldIn(root string, sources []Source) (texts []Text, stale int, err error) {
	for _, s := range sources {
		b, err := os.ReadFile(filepath.Join(root, s.Path))
		if os.IsNotExist(err) {
			stale++
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != s.SHA256 {
			stale++
			continue
		}
		texts = append(texts, Text{Source: s, Body: b})
	}
	return texts, stale, nil
}

// Encoder is a tokenizer: teacher.BPE satisfies it.
type Encoder interface {
	Encode(text string) []int
}

// Fertility is one language's token count under one tokenizer.
type Fertility struct {
	Files         int     `json:"files"`
	Bytes         int64   `json:"bytes"`
	Tokens        int64   `json:"tokens"`
	TokensPerByte float64 `json:"tokens_per_byte"`
}

// MeasureFertility counts tokens per byte for each language in texts. The tokenizer's
// template tokens (what it wraps an empty text in) are not counted, so a file's count is
// its text's alone.
func MeasureFertility(enc Encoder, texts []Text) map[string]Fertility {
	wrap := int64(len(enc.Encode("")))
	out := map[string]Fertility{}
	for _, t := range texts {
		f := out[t.Language]
		f.Files++
		f.Bytes += int64(len(t.Body))
		f.Tokens += int64(len(enc.Encode(string(t.Body)))) - wrap
		out[t.Language] = f
	}
	for lang, f := range out {
		if f.Bytes > 0 {
			f.TokensPerByte = float64(f.Tokens) / float64(f.Bytes)
		}
		out[lang] = f
	}
	return out
}

// FertilityRow is the runs.jsonl line of one fertility measurement.
type FertilityRow struct {
	Kind          string               `json:"kind"`
	Tokenizer     string               `json:"tokenizer"`
	Inputs        map[string]string    `json:"inputs_sha256"`
	SourcesSHA256 string               `json:"sources_sha256"`
	Stale         int                  `json:"stale"`
	Languages     map[string]Fertility `json:"languages"`
	WallSeconds   float64              `json:"wall_seconds"`
	Load1         float64              `json:"load1"`
}
