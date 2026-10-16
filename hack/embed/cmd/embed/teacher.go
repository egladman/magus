package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/hack/embed/teacher"
	"github.com/egladman/magus/libs/xdg"
	"github.com/gomlx/compute"
	"github.com/gomlx/compute/gobackend"
	_ "github.com/gomlx/go-xla/compute/xla"
)

// docPrefix is each teacher's document prompt, from its model card. CodeRankEmbed prompts
// only queries.
var docPrefix = map[string]string{
	"CodeRankEmbed":         "",
	"nomic-embed-text-v1.5": "search_document: ",
}

// inputRecord is one line of inputs.jsonl: a pinned file, where it comes from and where it
// lives under the cache root.
type inputRecord struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	License  string `json:"license"`
	Path     string `json:"path"`
}

func readInputs(path string) ([]inputRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []inputRecord
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		var in inputRecord
		if err := json.Unmarshal(sc.Bytes(), &in); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if in.Name == "" || in.Source == "" || len(in.SHA256) != 64 || in.Path == "" || filepath.IsAbs(in.Path) {
			return nil, fmt.Errorf("%s:%d: needs name, source, sha256 and a relative path", path, n)
		}
		out = append(out, in)
	}
	return out, sc.Err()
}

func findInput(inputs []inputRecord, name string) (inputRecord, error) {
	for _, in := range inputs {
		if in.Name == name {
			return in, nil
		}
	}
	return inputRecord{}, fmt.Errorf("inputs.jsonl pins no %s", name)
}

// verify fails unless the input's file under cache matches its pinned digest.
func verify(cache string, in inputRecord) (string, error) {
	path := filepath.Join(cache, in.Path)
	sum, err := teacher.FileSHA256(path)
	if err != nil {
		return "", fmt.Errorf("%w (run: embed fetch %s)", err, in.Name)
	}
	if sum != in.SHA256 {
		return "", fmt.Errorf("%s has sha256 %s, inputs.jsonl pins %s", path, sum, in.SHA256)
	}
	return path, nil
}

type common struct {
	inputs  string
	cache   string
	model   string
	backend string
	threads int
	batch   int
	bucket  int
	maxLen  int
}

func (c *common) register(fs *flag.FlagSet) {
	cache := ""
	if dir, err := xdg.CacheDir(); err == nil {
		cache = filepath.Join(dir, "magus-embed")
	}
	fs.StringVar(&c.inputs, "inputs", "inputs.jsonl", "the pinned inputs file")
	fs.StringVar(&c.cache, "cache", cache, "the cache root inputs.jsonl paths are relative to")
	fs.StringVar(&c.model, "model", "", "the teacher: CodeRankEmbed or nomic-embed-text-v1.5")
	fs.StringVar(&c.backend, "backend", "xla", "xla (PJRT CPU plugin) or go (pure Go, no cgo kernels)")
	fs.IntVar(&c.threads, "threads", 6, "CPU threads for the backend and the Go runtime")
	fs.IntVar(&c.batch, "batch", 16, "texts per executed batch")
	fs.IntVar(&c.bucket, "bucket", 32, "padded lengths round up to a multiple of this")
	fs.IntVar(&c.maxLen, "max-len", 512, "token cap, [CLS] and [SEP] included")
}

type loaded struct {
	enc  *teacher.Encoder
	tok  *teacher.WordPiece
	spec teacher.RunSpec
}

func (c *common) load() (*loaded, error) {
	prefix, ok := docPrefix[c.model]
	if !ok {
		return nil, fmt.Errorf("-model %q is not a known teacher", c.model)
	}
	if c.cache == "" {
		return nil, fmt.Errorf("-cache is empty and no cache directory resolved")
	}
	inputs, err := readInputs(c.inputs)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	var rev string
	for _, f := range []string{"config.json", "1_Pooling/config.json", "vocab.txt", "model.safetensors"} {
		in, err := findInput(inputs, c.model+"/"+f)
		if err != nil {
			return nil, err
		}
		if paths[f], err = verify(c.cache, in); err != nil {
			return nil, err
		}
		rev = in.Revision
	}
	weights, _ := findInput(inputs, c.model+"/model.safetensors")
	vocab, _ := findInput(inputs, c.model+"/vocab.txt")
	tok, err := teacher.LoadWordPiece(paths["vocab.txt"])
	if err != nil {
		return nil, err
	}
	m, err := teacher.LoadModel(filepath.Dir(paths["config.json"]))
	if err != nil {
		return nil, err
	}
	runtime.GOMAXPROCS(c.threads)
	be, err := c.newBackend(inputs)
	if err != nil {
		return nil, err
	}
	enc, err := teacher.NewEncoder(be, m, tok, teacher.EncoderOptions{Prefix: prefix, MaxLen: c.maxLen, Batch: c.batch, Bucket: c.bucket})
	if err != nil {
		return nil, err
	}
	spec := teacher.RunSpec{Model: c.model, Revision: rev, WeightsSHA256: weights.SHA256, VocabSHA256: vocab.SHA256,
		Prefix: prefix, MaxLen: c.maxLen, Pooling: m.Config.Pooling, Dim: m.Config.Hidden}
	return &loaded{enc: enc, tok: tok, spec: spec}, nil
}

func (c *common) newBackend(inputs []inputRecord) (compute.Backend, error) {
	switch c.backend {
	case "go":
		return gobackend.New("")
	case "xla":
		in, err := findInput(inputs, "pjrt-cpu/"+runtime.GOOS+"_"+runtime.GOARCH)
		if err != nil {
			return nil, err
		}
		plugin, err := pluginPath(c.cache, in)
		if err != nil {
			return nil, err
		}
		// The PJRT CPU client sizes its thread pool from PJRT_NPROC, else every core. An
		// absolute plugin path skips go-xla's search, which reads its environment at init.
		os.Setenv("PJRT_NPROC", strconv.Itoa(c.threads))
		return compute.NewWithConfig("xla:" + plugin)
	}
	return nil, fmt.Errorf("-backend %q is neither xla nor go", c.backend)
}

// pluginPath returns the extracted PJRT CPU plugin, extracting the verified release
// archive on first use.
func pluginPath(cache string, in inputRecord) (string, error) {
	dir := filepath.Join(cache, "xla", in.Revision)
	glob := filepath.Join(dir, "pjrt_c_api_cpu_*_plugin.so")
	if matches, _ := filepath.Glob(glob); len(matches) == 1 {
		return matches[0], nil
	}
	archive, err := verify(cache, in)
	if err != nil {
		return "", err
	}
	if err := untarGz(archive, dir); err != nil {
		return "", err
	}
	matches, _ := filepath.Glob(glob)
	if len(matches) != 1 {
		return "", fmt.Errorf("%s holds %d plugins matching %s, want 1", archive, len(matches), filepath.Base(glob))
	}
	return matches[0], nil
}

func untarGz(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Base(h.Name)
		if h.Typeflag != tar.TypeReg || name != h.Name {
			return fmt.Errorf("%s: unexpected entry %q", archive, h.Name)
		}
		tmp := filepath.Join(dir, "."+name+".tmp")
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
}

func runFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	var c common
	c.register(fs)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: embed fetch [flags] [name-prefix ...]\n\nwith no prefix, fetches every input")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	inputs, err := readInputs(c.inputs)
	if err != nil {
		return err
	}
	for _, in := range inputs {
		if fs.NArg() > 0 && !slices.ContainsFunc(fs.Args(), func(p string) bool { return strings.HasPrefix(in.Name, p) }) {
			continue
		}
		if _, err := verify(c.cache, in); err == nil {
			fmt.Printf("ok      %s\n", in.Name)
			continue
		}
		if err := download(c.cache, in); err != nil {
			return fmt.Errorf("%s: %w", in.Name, err)
		}
		fmt.Printf("fetched %s (%d bytes)\n", in.Name, in.Size)
	}
	return nil
}

func download(cache string, in inputRecord) error {
	dst := filepath.Join(cache, in.Path)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	resp, err := http.Get(in.Source)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", in.Source, resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".fetch-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != in.SHA256 || n != in.Size {
		return fmt.Errorf("GET %s: got %d bytes with sha256 %s, want %d bytes with %s", in.Source, n, sum, in.Size, in.SHA256)
	}
	return os.Rename(tmp.Name(), dst)
}

func runTeacher(args []string) error {
	fs := flag.NewFlagSet("teacher", flag.ExitOnError)
	var c common
	c.register(fs)
	texts := fs.String("texts", "", "JSON lines of {\"id\", \"text\"} to embed")
	out := fs.String("out", "", "run root; default <cache>/teacher")
	shardSize := fs.Int("shard-size", 4096, "texts per shard")
	maxShards := fs.Int("max-shards", 0, "stop after encoding this many new shards; 0 runs to the end")
	_ = fs.Parse(args)
	if *texts == "" {
		return fmt.Errorf("-texts is required")
	}
	if *out == "" {
		*out = filepath.Join(c.cache, "teacher")
	}
	l, err := c.load()
	if err != nil {
		return err
	}
	defer l.enc.Close()
	in, err := teacher.ReadTexts(*texts)
	if err != nil {
		return err
	}
	if l.spec.TextsSHA256, err = teacher.FileSHA256(*texts); err != nil {
		return err
	}
	l.spec.ShardSize = *shardSize
	dir := filepath.Join(*out, c.model, l.spec.Key())
	fmt.Printf("run %s: %d texts, %d per shard, backend %s, %d threads\n", dir, len(in), *shardSize, c.backend, c.threads)
	st, err := teacher.Run(dir, l.spec, in, l.enc, *maxShards, func(r teacher.Record) {
		fmt.Printf("shard %d: %d texts in %.1fs (%.1f texts/s), object %s\n", r.Shard, r.Rows, r.Seconds,
			float64(r.Rows)/r.Seconds, r.Object[:16])
	})
	if err != nil {
		return err
	}
	rate := float64(st.Texts) / st.Elapsed.Seconds()
	fmt.Printf("%d shards: %d skipped, %d encoded; %d texts, %d tokens in %s (%.1f texts/s)\n",
		st.Shards, st.Skipped, st.Encoded, st.Texts, st.Tokens, st.Elapsed.Round(time.Second), rate)
	return nil
}

type parityResult struct {
	Model         string             `json:"model"`
	Backend       string             `json:"backend"`
	Threads       int                `json:"threads"`
	Batch         int                `json:"batch"`
	Bucket        int                `json:"bucket"`
	Texts         int                `json:"texts"`
	Tokens        int                `json:"tokens"`
	TokenMismatch int                `json:"token_mismatch"`
	MinCos        float64            `json:"min_cos"`
	MeanCos       float64            `json:"mean_cos"`
	P01Cos        float64            `json:"p01_cos"`
	Below         int                `json:"below_threshold"`
	MinCosSubset  map[string]float64 `json:"min_cos_by_subset"`
	PassSeconds   []float64          `json:"pass_seconds"`
	TextsPerSec   float64            `json:"texts_per_s"`
	TokensPerSec  float64            `json:"tokens_per_s"`
	LoadSeconds   float64            `json:"load_seconds"`
}

type sampleRow struct {
	Subset string `json:"subset"`
	ID     string `json:"id"`
	Text   string `json:"text"`
}

func runParity(args []string) error {
	fs := flag.NewFlagSet("teacher-parity", flag.ExitOnError)
	var c common
	c.register(fs)
	dir := fs.String("dir", "", "parity directory with sample.jsonl, ref-<model>.f32, tokens-<model>.jsonl; default <cache>/parity")
	passes := fs.Int("passes", 2, "encode passes; every pass after the first is timed without compiles")
	minCos := fs.Float64("min-cos", 0.999, "the per-row cosine every row must reach")
	limit := fs.Int("limit", 0, "use only the first N sample texts; 0 uses all")
	_ = fs.Parse(args)
	if *dir == "" {
		*dir = filepath.Join(c.cache, "parity")
	}
	sample, err := readSample(filepath.Join(*dir, "sample.jsonl"))
	if err != nil {
		return err
	}
	refTokens, err := readTokens(filepath.Join(*dir, "tokens-"+c.model+".jsonl"))
	if err != nil {
		return err
	}
	t0 := time.Now()
	l, err := c.load()
	if err != nil {
		return err
	}
	defer l.enc.Close()
	refs, err := readF32(filepath.Join(*dir, "ref-"+c.model+".f32"), len(sample), l.spec.Dim)
	if err != nil {
		return err
	}
	if *limit > 0 && *limit < len(sample) {
		sample, refTokens, refs = sample[:*limit], refTokens[:*limit], refs[:*limit]
	}
	res := parityResult{Model: c.model, Backend: c.backend, Threads: c.threads, Batch: c.batch, Bucket: c.bucket,
		Texts: len(sample), LoadSeconds: time.Since(t0).Seconds(), MinCosSubset: map[string]float64{}}

	texts := make([]string, len(sample))
	for i, s := range sample {
		texts[i] = s.Text
		if got := l.tok.Encode(l.spec.Prefix+s.Text, c.maxLen); !slices.Equal(got, refTokens[i]) {
			if res.TokenMismatch < 3 {
				fmt.Printf("token mismatch %s/%s: go %d ids, reference %d ids\n", s.Subset, s.ID, len(got), len(refTokens[i]))
			}
			res.TokenMismatch++
		}
	}

	var vecs [][]float32
	for p := range *passes {
		before := l.enc.Tokens()
		t := time.Now()
		if vecs, err = l.enc.Encode(texts); err != nil {
			return err
		}
		res.PassSeconds = append(res.PassSeconds, time.Since(t).Seconds())
		res.Tokens = l.enc.Tokens() - before
		fmt.Printf("pass %d: %d texts in %.1fs\n", p, len(texts), res.PassSeconds[p])
	}
	timed := res.PassSeconds[len(res.PassSeconds)-1]
	res.TextsPerSec = float64(len(texts)) / timed
	res.TokensPerSec = float64(res.Tokens) / timed

	cos := make([]float64, len(vecs))
	res.MinCos = 2
	for i, v := range vecs {
		cos[i] = cosine(v, refs[i])
		res.MeanCos += cos[i] / float64(len(vecs))
		res.MinCos = math.Min(res.MinCos, cos[i])
		if m, ok := res.MinCosSubset[sample[i].Subset]; !ok || cos[i] < m {
			res.MinCosSubset[sample[i].Subset] = cos[i]
		}
		if cos[i] < *minCos {
			res.Below++
		}
	}
	sorted := slices.Clone(cos)
	sort.Float64s(sorted)
	res.P01Cos = sorted[len(sorted)/100]

	b, _ := json.MarshalIndent(res, "", " ")
	fmt.Println(string(b))
	name := fmt.Sprintf("result-%s-%s.json", c.model, c.backend)
	if err := os.WriteFile(filepath.Join(*dir, name), append(b, '\n'), 0o644); err != nil {
		return err
	}
	if res.Below > 0 || res.TokenMismatch > 0 {
		return fmt.Errorf("%d rows below cosine %g, %d token mismatches", res.Below, *minCos, res.TokenMismatch)
	}
	return nil
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}

func readSample(path string) ([]sampleRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []sampleRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<28)
	for sc.Scan() {
		var r sampleRow
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

func readTokens(path string) ([][]int32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out [][]int32
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var ids []int32
		if err := json.Unmarshal(sc.Bytes(), &ids); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, ids)
	}
	return out, sc.Err()
}

func readF32(path string, rows, dim int) ([][]float32, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) != 4*rows*dim {
		return nil, fmt.Errorf("%s holds %d bytes, want %d rows of %d float32", path, len(b), rows, dim)
	}
	out := make([][]float32, rows)
	for r := range out {
		out[r] = make([]float32, dim)
		for i := range out[r] {
			out[r][i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*(r*dim+i):]))
		}
	}
	return out, nil
}
