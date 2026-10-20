package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/embed/train/distill"
	"github.com/egladman/magus/libs/embed/train/teacher"
)

// jsonlTexts reads JSON lines of {"id", "text"}, the shape embed-mine writes and the
// teacher reads, as a distill.TextSource. Other fields on a line are ignored.
type jsonlTexts struct{ path string }

func (c jsonlTexts) Texts() iter.Seq2[distill.Text, error] {
	return func(yield func(distill.Text, error) bool) {
		seen := map[string]bool{}
		for n, line := range jsonLines(c.path, yield) {
			var t teacher.Text
			if err := json.Unmarshal(line, &t); err != nil {
				yield(distill.Text{}, fmt.Errorf("%s:%d: %w", c.path, n, err))
				return
			}
			if t.ID == "" || seen[t.ID] {
				yield(distill.Text{}, fmt.Errorf("%s:%d: id %q is empty or repeated", c.path, n, t.ID))
				return
			}
			seen[t.ID] = true
			if !yield(distill.Text{ID: t.ID, Body: t.Text}, nil) {
				return
			}
		}
	}
}

// pairRecord is one line of libs/embed/train/gen/pairs.jsonl: references into the tree at
// the pinned commit, never the text itself.
type pairRecord struct {
	Source   string `json:"source"`
	QueryRef string `json:"query_ref"`
	DocRef   string `json:"doc_ref"`
	Split    string `json:"split"`
}

// jsonlPairs reads pairs.jsonl as distill.Pairs, yielding the train split only and
// resolving each ref against root.
type jsonlPairs struct{ path, root string }

func (p jsonlPairs) Pairs() iter.Seq2[distill.TextPair, error] {
	return func(yield func(distill.TextPair, error) bool) {
		files := map[string][]string{}
		for n, line := range jsonLines(p.path, yield) {
			var r pairRecord
			if err := json.Unmarshal(line, &r); err != nil {
				yield(distill.TextPair{}, fmt.Errorf("%s:%d: %w", p.path, n, err))
				return
			}
			switch r.Split {
			case "train":
			case "dev", "holdout", "eval":
				continue
			default:
				yield(distill.TextPair{}, fmt.Errorf("%s:%d: split %q is not train, dev, holdout or eval", p.path, n, r.Split))
				return
			}
			q, err := resolveRef(p.root, r.QueryRef, files)
			if err != nil {
				yield(distill.TextPair{}, fmt.Errorf("%s:%d: %w", p.path, n, err))
				return
			}
			d, err := resolveRef(p.root, r.DocRef, files)
			if err != nil {
				yield(distill.TextPair{}, fmt.Errorf("%s:%d: %w", p.path, n, err))
				return
			}
			if !yield(distill.TextPair{Source: r.Source, Query: q, Doc: d}, nil) {
				return
			}
		}
	}
}

var spanRef = regexp.MustCompile(`^(.+):(\d+)-(\d+)$`)

// resolveRef reads a `path:first-last` span, lines 1-based and inclusive, from the file
// under root. files caches each file's lines. A graph node id has no resolver yet and
// is an error, never skipped.
func resolveRef(root, ref string, files map[string][]string) (string, error) {
	m := spanRef.FindStringSubmatch(ref)
	if m == nil {
		return "", fmt.Errorf("ref %q is not a path:first-last span, and graph node refs are not resolved yet", ref)
	}
	first, err1 := strconv.Atoi(m[2])
	last, err2 := strconv.Atoi(m[3])
	if err := errors.Join(err1, err2); err != nil || first < 1 || last < first {
		return "", fmt.Errorf("ref %q has a malformed line range", ref)
	}
	if filepath.IsAbs(m[1]) || !filepath.IsLocal(m[1]) {
		return "", fmt.Errorf("ref %q leaves the tree", ref)
	}
	lines, ok := files[m[1]]
	if !ok {
		b, err := os.ReadFile(filepath.Join(root, m[1]))
		if err != nil {
			return "", fmt.Errorf("ref %q: %w", ref, err)
		}
		lines = strings.Split(string(b), "\n")
		files[m[1]] = lines
	}
	if last > len(lines) {
		return "", fmt.Errorf("ref %q ends past the file's %d lines", ref, len(lines))
	}
	return strings.Join(lines[first-1:last], "\n"), nil
}

// jsonLines yields the 1-based number and bytes of each non-blank line of path. An open
// or read error goes to fail, which ends the caller's sequence.
func jsonLines[T any](path string, fail func(T, error) bool) iter.Seq2[int, []byte] {
	return func(yield func(int, []byte) bool) {
		var zero T
		f, err := os.Open(path)
		if err != nil {
			fail(zero, err)
			return
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<28)
		for n := 1; sc.Scan(); n++ {
			if len(bytes.TrimSpace(sc.Bytes())) == 0 {
				continue
			}
			if !yield(n, sc.Bytes()) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			fail(zero, fmt.Errorf("%s: %w", path, err))
		}
	}
}

// teacherRun is a finished teacher run read back: its spec, and one vector per input
// text in input order.
type teacherRun struct {
	spec teacher.RunSpec
	vecs [][]float32
}

// readTeacherRun reads the manifest.jsonl and objects/ under dir that teacher.Run wrote
// for a texts file of the given sha256 holding texts lines. The run must cover every
// text: the spec's texts digest must match, each shard object must hash to its name and
// hold rows×dim float32s, and the shards must tile the texts exactly. When a shard was
// encoded twice, the later record wins.
func readTeacherRun(dir, digest string, texts int) (teacherRun, error) {
	man := filepath.Join(dir, "manifest.jsonl")
	b, err := os.ReadFile(man)
	if err != nil {
		return teacherRun{}, err
	}
	var run teacherRun
	shards := map[int]teacher.Record{}
	for n, line := range bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n")) {
		var r teacher.Record
		if err := json.Unmarshal(line, &r); err != nil {
			return teacherRun{}, fmt.Errorf("%s:%d: %w", man, n+1, err)
		}
		switch {
		case n == 0 && (r.Kind != "run" || r.Spec == nil):
			return teacherRun{}, fmt.Errorf("%s: first line is not a run record", man)
		case n == 0:
			run.spec = *r.Spec
		case r.Kind == "shard":
			shards[r.Shard] = r
		default:
			return teacherRun{}, fmt.Errorf("%s:%d: record kind %q", man, n+1, r.Kind)
		}
	}
	if run.spec.TextsSHA256 != digest {
		return teacherRun{}, fmt.Errorf("%s embedded texts %s, not these texts %s", dir, run.spec.TextsSHA256, digest)
	}
	size := run.spec.ShardSize
	if size < 1 || run.spec.Dim < 1 {
		return teacherRun{}, fmt.Errorf("%s: run spec has shard size %d and dim %d", dir, size, run.spec.Dim)
	}
	want := (texts + size - 1) / size
	if len(shards) != want {
		return teacherRun{}, fmt.Errorf("%s holds %d of the %d shards %d texts need: finish the teacher run", dir, len(shards), want, texts)
	}
	run.vecs = make([][]float32, texts)
	for i := range want {
		r, ok := shards[i]
		if !ok || r.First != i*size || r.Rows != min(size, texts-i*size) {
			return teacherRun{}, fmt.Errorf("%s: shard %d is missing or does not start at row %d", dir, i, i*size)
		}
		obj, err := os.ReadFile(filepath.Join(dir, "objects", r.Object+".f32"))
		if err != nil {
			return teacherRun{}, err
		}
		sum := sha256.Sum256(obj)
		if hex.EncodeToString(sum[:]) != r.Object {
			return teacherRun{}, fmt.Errorf("%s: shard %d's object does not hash to its name", dir, i)
		}
		if len(obj) != 4*r.Rows*run.spec.Dim {
			return teacherRun{}, fmt.Errorf("%s: shard %d holds %d bytes, not %d rows of %d float32s", dir, i, len(obj), r.Rows, run.spec.Dim)
		}
		for k := range r.Rows {
			v := make([]float32, run.spec.Dim)
			for j := range v {
				v[j] = math.Float32frombits(binary.LittleEndian.Uint32(obj[4*(k*run.spec.Dim+j):]))
			}
			run.vecs[r.First+k] = v
		}
	}
	return run, nil
}

// tokenTexts is the teacher texts file of a vocabulary: one {"id", "text"} line per
// token, both the token, in rank order, so the teacher embeds every token on its own as
// model2vec does. The bytes depend only on the vocabulary, so a rebuilt vocabulary is
// checked against the run that embedded it by digest.
func tokenTexts(v *distill.Vocab) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, tok := range v.Tokens {
		if err := enc.Encode(teacher.Text{ID: tok, Text: tok}); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
