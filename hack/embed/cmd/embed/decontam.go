package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/hack/embed/teacher"
)

// Decontamination drops a training text that repeats an eval text: the whole text after
// whitespace normalization, or any window of gramWords words. The 13-word window and the
// shorter whole-text gram for eval texts of minGramWords to 12 words follow Brown et al.
// 2020 (GPT-3, appendix C). CoRNStack removed only exact matches with eval sets and calls
// that insufficient (Suresh et al., ICLR 2025, Limitations).
const (
	gramWords    = 13
	minGramWords = 8
)

// requiredEvalSets are the external sets every texts run scans against, each read from
// <-eval>/<name>.jsonl, unless -no-eval names it.
var requiredEvalSets = []string{"csn-go", "cosqa", "stackoverflow-qa"}

// decontaminator holds every eval text's normalized whole-text hash and gram hashes.
// grams is sorted and unique, with gramSets[i] the eval sets holding grams[i] as a bit
// mask over sets and gramTexts[i] how many eval texts hold it.
type decontaminator struct {
	sets      []string
	exact     map[uint64]uint64
	grams     []uint64
	gramSets  []uint64
	gramTexts []int32
	lengths   []int
	common    int32
	report    decontamReport
	dropped   []droppedText
}

// decontamReport is what a texts run records about decontamination in runs.jsonl.
type decontamReport struct {
	GramWords    int               `json:"gram_words"`
	MinGramWords int               `json:"min_gram_words"`
	Common       int               `json:"common"`
	EvalTexts    map[string]int    `json:"eval_texts"`
	EvalSHA256   map[string]string `json:"eval_sha256,omitempty"`
	LeftOut      []string          `json:"left_out"`
	Grams        int               `json:"grams"`
	CommonGrams  int               `json:"common_grams"`
	Scanned      map[string]int    `json:"scanned"`
	Dropped      map[string]int    `json:"dropped"`
	DroppedBy    map[string]int    `json:"dropped_by"`
}

// droppedText is one line of dropped.jsonl: a training text decontamination removed, why,
// and the eval sets it repeats.
type droppedText struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Reason string   `json:"reason"`
	Sets   []string `json:"sets"`
}

type gramRef struct {
	hash uint64
	set  int
	text int32
}

// evalCorpus collects eval texts before newDecontaminator indexes them.
type evalCorpus struct {
	sets  []string
	texts map[string][]string
	files map[string]string
}

func (e *evalCorpus) add(set string, texts ...string) {
	if e.texts == nil {
		e.texts = map[string][]string{}
	}
	if _, ok := e.texts[set]; !ok {
		e.sets = append(e.sets, set)
	}
	e.texts[set] = append(e.texts[set], texts...)
}

// newDecontaminator indexes every eval text. A gram more than common eval texts hold is
// boilerplate, such as an error-return idiom, and is recorded but never drops a text.
func newDecontaminator(e *evalCorpus, common int) (*decontaminator, error) {
	if len(e.sets) > 64 {
		return nil, fmt.Errorf("%d eval sets overflow the 64-bit set mask", len(e.sets))
	}
	if common < 1 {
		return nil, fmt.Errorf("-common %d must be at least 1", common)
	}
	d := &decontaminator{
		sets:   e.sets,
		exact:  map[uint64]uint64{},
		common: int32(common),
		report: decontamReport{
			GramWords: gramWords, MinGramWords: minGramWords, Common: common,
			EvalTexts: map[string]int{}, EvalSHA256: e.files, LeftOut: []string{},
			Scanned: map[string]int{}, Dropped: map[string]int{}, DroppedBy: map[string]int{},
		},
	}
	var refs []gramRef
	lengths := map[int]bool{}
	var n int32
	for si, set := range e.sets {
		for _, text := range e.texts[set] {
			words := strings.Fields(text)
			if len(words) == 0 {
				continue
			}
			d.report.EvalTexts[set]++
			d.exact[wholeHash(words)] |= 1 << si
			size := min(len(words), gramWords)
			if size < minGramWords {
				continue
			}
			lengths[size] = true
			for _, h := range windowHashes(words, size) {
				refs = append(refs, gramRef{hash: h, set: si, text: n})
			}
			n++
		}
	}
	slices.SortFunc(refs, func(a, b gramRef) int {
		return cmp.Or(cmp.Compare(a.hash, b.hash), cmp.Compare(a.text, b.text))
	})
	for i, r := range refs {
		last := len(d.grams) - 1
		if last >= 0 && d.grams[last] == r.hash {
			d.gramSets[last] |= 1 << r.set
			if refs[i-1].text != r.text {
				d.gramTexts[last]++
			}
			continue
		}
		d.grams = append(d.grams, r.hash)
		d.gramSets = append(d.gramSets, 1<<r.set)
		d.gramTexts = append(d.gramTexts, 1)
	}
	for _, c := range d.gramTexts {
		if c > d.common {
			d.report.CommonGrams++
		}
	}
	d.report.Grams = len(d.grams)
	d.lengths = slices.Sorted(maps.Keys(lengths))
	return d, nil
}

// admit reports whether a training text of kind may enter training. It is refused when
// any of texts repeats an eval text, and the refusal is counted and listed under id.
func (d *decontaminator) admit(kind, id string, texts ...string) bool {
	d.report.Scanned[kind]++
	for _, text := range texts {
		reason, mask := d.match(strings.Fields(text))
		if reason == "" {
			continue
		}
		var sets []string
		for si, set := range d.sets {
			if mask&(1<<si) != 0 {
				sets = append(sets, set)
				d.report.DroppedBy[kind+"/"+reason+"/"+set]++
			}
		}
		d.report.Dropped[kind]++
		d.dropped = append(d.dropped, droppedText{ID: id, Kind: kind, Reason: reason, Sets: sets})
		return false
	}
	return true
}

// match is "exact" or "ngram" with the eval sets words repeats, or "" when it repeats
// none.
func (d *decontaminator) match(words []string) (string, uint64) {
	if len(words) == 0 {
		return "", 0
	}
	if mask := d.exact[wholeHash(words)]; mask != 0 {
		return "exact", mask
	}
	var mask uint64
	for _, size := range d.lengths {
		for _, h := range windowHashes(words, size) {
			if i, ok := slices.BinarySearch(d.grams, h); ok && d.gramTexts[i] <= d.common {
				mask |= d.gramSets[i]
			}
		}
	}
	if mask != 0 {
		return "ngram", mask
	}
	return "", 0
}

// gramBase is an odd multiplier; window hashes are polynomials over word hashes mod 2^64.
const gramBase = 0x9e3779b97f4a7c15

func wordHash(w string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(w))
	return h.Sum64()
}

func wholeHash(words []string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.Join(words, " ")))
	return h.Sum64()
}

// windowHashes is the hash of every run of size consecutive words, rolled in one pass.
func windowHashes(words []string, size int) []uint64 {
	if len(words) < size {
		return nil
	}
	var top uint64 = 1
	for range size - 1 {
		top *= gramBase
	}
	out := make([]uint64, 0, len(words)-size+1)
	var h uint64
	for i, w := range words {
		if i >= size {
			h -= wordHash(words[i-size]) * top
		}
		h = h*gramBase + wordHash(w)
		if i >= size-1 {
			out = append(out, h)
		}
	}
	return out
}

// readEvalSets adds every <dir>/<name>.jsonl of {"id", "text"} lines as eval set name.
// Each required set must be present unless leftOut names it; an empty dir is allowed only
// when every required set is left out.
func readEvalSets(e *evalCorpus, dir string, leftOut map[string]bool) error {
	var paths []string
	if dir != "" {
		var err error
		if paths, err = filepath.Glob(filepath.Join(dir, "*.jsonl")); err != nil {
			return err
		}
	}
	found := map[string]bool{}
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		texts, err := readEvalTexts(p)
		if err != nil {
			return err
		}
		digest, err := teacher.FileSHA256(p)
		if err != nil {
			return err
		}
		if e.files == nil {
			e.files = map[string]string{}
		}
		e.files[name] = digest
		e.add(name, texts...)
		found[name] = true
	}
	for _, name := range requiredEvalSets {
		if !found[name] && !leftOut[name] {
			return fmt.Errorf("eval set %s: no %s.jsonl under -eval %q; export it, or name it in -no-eval to leave it out", name, name, dir)
		}
	}
	return nil
}

func readEvalTexts(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		var r struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if r.ID == "" {
			return nil, fmt.Errorf("%s:%d: no id", path, n)
		}
		out = append(out, r.Text)
	}
	return out, sc.Err()
}
