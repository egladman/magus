package teacher

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
)

// Text is one teacher input.
type Text struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// ReadTexts reads JSON lines of {"id", "text"}. An empty id or a repeated one is an error.
func ReadTexts(path string) ([]Text, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Text
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<28)
	for n := 1; sc.Scan(); n++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var t Text
		if err := json.Unmarshal(sc.Bytes(), &t); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if t.ID == "" || seen[t.ID] {
			return nil, fmt.Errorf("%s:%d: id %q is empty or repeated", path, n, t.ID)
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	return out, sc.Err()
}

// RunSpec names everything that decides a teacher vector besides the text itself. Two runs
// with equal specs over equal texts produce interchangeable shards.
type RunSpec struct {
	Model         string  `json:"model"`
	Revision      string  `json:"revision"`
	WeightsSHA256 string  `json:"weights_sha256"`
	VocabSHA256   string  `json:"vocab_sha256"`
	Prefix        string  `json:"prefix"`
	MaxLen        int     `json:"max_len"`
	Pooling       Pooling `json:"pooling"`
	Dim           int     `json:"dim"`
	ShardSize     int     `json:"shard_size"`
	// TextsSHA256 is the digest of the input file the shards index into.
	TextsSHA256 string `json:"texts_sha256"`
}

// Key is the run's directory name: a digest of the spec.
func (s RunSpec) Key() string {
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// Record is one manifest line. The first line of a manifest has Kind "run" and carries the
// spec; each later line has Kind "shard" and describes one finished object.
type Record struct {
	Kind string   `json:"kind"`
	Spec *RunSpec `json:"spec,omitempty"`
	// Shard is the zero-based shard index; rows [First, First+Rows) of the input texts.
	Shard int `json:"shard"`
	First int `json:"first"`
	Rows  int `json:"rows,omitempty"`
	// Input digests the spec plus every id and text of the shard; a resumed run skips a
	// shard whose Input already has an intact object.
	Input string `json:"input_sha256,omitempty"`
	// Object is the sha256 of the object bytes and its file name under objects/: Rows x Dim
	// little-endian float32, L2-normalized, input order.
	Object  string  `json:"object_sha256,omitempty"`
	Bytes   int64   `json:"bytes,omitempty"`
	Tokens  int     `json:"tokens,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
}

// Embedder turns texts into vectors and reports the tokens it has consumed so far.
type Embedder interface {
	Encode(texts []string) ([][]float32, error)
	Tokens() int
}

// RunStats summarizes one Run call.
type RunStats struct {
	Shards, Skipped, Encoded int
	Texts, Tokens            int
	Elapsed                  time.Duration
}

// openRun opens the run under dir for appending: it returns the shards its manifest
// records as done, by input digest, and the manifest, which it starts with spec's run
// record when it is new. The caller closes the manifest.
func openRun(dir string, spec RunSpec) (map[string]Record, *os.File, error) {
	if err := os.MkdirAll(filepath.Join(dir, "objects"), 0o755); err != nil {
		return nil, nil, err
	}
	done, err := readManifest(filepath.Join(dir, "manifest.jsonl"), spec)
	if err != nil {
		return nil, nil, err
	}
	man, err := os.OpenFile(filepath.Join(dir, "manifest.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	if done == nil {
		if err := appendRecord(man, Record{Kind: "run", Spec: &spec}); err != nil {
			man.Close()
			return nil, nil, err
		}
	}
	return done, man, nil
}

// Run encodes texts shard by shard under dir, which holds manifest.jsonl and objects/.
// Each finished shard is written atomically and then appended to the manifest, so a run
// stopped at any point resumes from its last whole shard. maxShards > 0 stops after that
// many newly encoded shards. A manifest whose spec differs from spec is an error.
func Run(dir string, spec RunSpec, texts []Text, enc Embedder, maxShards int, progress func(Record)) (RunStats, error) {
	if spec.ShardSize < 1 || spec.Dim < 1 {
		return RunStats{}, fmt.Errorf("run spec needs a shard size and dim: %+v", spec)
	}
	done, man, err := openRun(dir, spec)
	if err != nil {
		return RunStats{}, err
	}
	defer man.Close()

	var st RunStats
	start := time.Now()
	for first, shard := 0, 0; first < len(texts); first, shard = first+spec.ShardSize, shard+1 {
		st.Shards++
		part := texts[first:min(first+spec.ShardSize, len(texts))]
		input := shardDigest(spec, part)
		if rec, ok := done[input]; ok && objectIntact(dir, rec) {
			st.Skipped++
			continue
		}
		if maxShards > 0 && st.Encoded >= maxShards {
			continue
		}
		rec, err := encodeShard(dir, spec, part, enc)
		if err != nil {
			return st, fmt.Errorf("shard %d: %w", shard, err)
		}
		rec.Shard, rec.First, rec.Input = shard, first, input
		if err := appendRecord(man, rec); err != nil {
			return st, err
		}
		st.Encoded++
		st.Texts += rec.Rows
		st.Tokens += rec.Tokens
		if progress != nil {
			progress(rec)
		}
	}
	st.Elapsed = time.Since(start)
	return st, nil
}

func encodeShard(dir string, spec RunSpec, part []Text, enc Embedder) (Record, error) {
	strs := make([]string, len(part))
	for i, t := range part {
		strs[i] = t.Text
	}
	t0, tok0 := time.Now(), enc.Tokens()
	vecs, err := enc.Encode(strs)
	if err != nil {
		return Record{}, err
	}
	secs := time.Since(t0).Seconds()
	buf := make([]byte, 0, 4*spec.Dim*len(vecs))
	for i, v := range vecs {
		if len(v) != spec.Dim {
			return Record{}, fmt.Errorf("text %s embedded to %d floats, want %d", part[i].ID, len(v), spec.Dim)
		}
		for _, x := range v {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return Record{}, fmt.Errorf("text %s embedded to a non-finite value", part[i].ID)
			}
			buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(x))
		}
	}
	sum := sha256.Sum256(buf)
	obj := hex.EncodeToString(sum[:])
	if err := writeAtomic(filepath.Join(dir, "objects", obj+".f32"), buf); err != nil {
		return Record{}, err
	}
	return Record{Kind: "shard", Rows: len(part), Object: obj, Bytes: int64(len(buf)),
		Tokens: enc.Tokens() - tok0, Seconds: secs}, nil
}

func shardDigest(spec RunSpec, part []Text) string {
	h := sha256.New()
	b, _ := json.Marshal(spec)
	h.Write(b)
	for _, t := range part {
		h.Write([]byte(t.ID))
		h.Write([]byte{0})
		h.Write([]byte(t.Text))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// readManifest returns the finished shards by input digest, or nil when no manifest exists.
// A last line without its newline is what a crash mid-append leaves; it is cut off so the
// next append starts a fresh line.
func readManifest(path string, spec RunSpec) (map[string]Record, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if cut := bytes.LastIndexByte(b, '\n') + 1; cut < len(b) {
		if err := os.Truncate(path, int64(cut)); err != nil {
			return nil, err
		}
		b = b[:cut]
	}
	if len(b) == 0 {
		return nil, nil
	}
	done := map[string]Record{}
	for n, line := range bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n")) {
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n+1, err)
		}
		switch {
		case n == 0 && (r.Kind != "run" || r.Spec == nil):
			return nil, fmt.Errorf("%s: first line is not a run record", path)
		case n == 0 && *r.Spec != spec:
			return nil, fmt.Errorf("%s: run spec %+v differs from %+v", path, *r.Spec, spec)
		case r.Kind == "shard":
			done[r.Input] = r
		}
	}
	return done, nil
}

func objectIntact(dir string, r Record) bool {
	b, err := os.ReadFile(filepath.Join(dir, "objects", r.Object+".f32"))
	if err != nil {
		return false
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]) == r.Object
}

func appendRecord(w io.Writer, r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	if f, ok := w.(*os.File); ok {
		return f.Sync()
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Objects are meant to be published; CreateTemp makes them owner-only.
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// FileSHA256 is the hex sha256 of a file's bytes.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
