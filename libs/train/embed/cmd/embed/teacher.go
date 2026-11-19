package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
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

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/train/embed/teacher"
	"github.com/gomlx/compute"
	"github.com/gomlx/compute/gobackend"
	_ "github.com/gomlx/go-xla/compute/xla"
	"gopkg.in/yaml.v3"
)

// docPrefix is each teacher's document prompt, from its model card. CodeRankEmbed prompts
// only queries.
var docPrefix = map[string]string{
	"CodeRankEmbed":         "",
	"nomic-embed-text-v1.5": "search_document: ",
}

// inputRecord is one entry of inputs.yaml: a pinned file, where it comes from and where it
// lives under the cache root.
type inputRecord struct {
	Name     string `yaml:"name"`
	Source   string `yaml:"source"`
	Revision string `yaml:"revision"`
	SHA256   string `yaml:"sha256"`
	Size     int64  `yaml:"size"`
	License  string `yaml:"license"`
	Path     string `yaml:"path"`
}

func readInputs(path string) ([]inputRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []inputRecord
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for n, in := range out {
		if in.Name == "" || in.Source == "" || len(in.SHA256) != 64 || in.Path == "" || filepath.IsAbs(in.Path) {
			return nil, fmt.Errorf("%s: entry %d (%q) needs name, source, sha256 and a relative path", path, n+1, in.Name)
		}
	}
	return out, nil
}

func findInput(inputs []inputRecord, name string) (inputRecord, error) {
	for _, in := range inputs {
		if in.Name == name {
			return in, nil
		}
	}
	return inputRecord{}, fmt.Errorf("inputs.yaml pins no %s", name)
}

// verify fails unless the input's file under cache matches its pinned digest.
func verify(cache string, in inputRecord) (string, error) {
	path := filepath.Join(cache, in.Path)
	sum, err := teacher.FileSHA256(path)
	if err != nil {
		return "", fmt.Errorf("input %s (embed fetch %s fetches it): %w", in.Name, in.Name, err)
	}
	if sum != in.SHA256 {
		return "", fmt.Errorf("%s has sha256 %s, inputs.yaml pins %s", path, sum, in.SHA256)
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

func runFetch(c *common, args []string) error {
	inputs, err := readInputs(c.inputs)
	if err != nil {
		return err
	}
	for _, in := range inputs {
		if len(args) > 0 && !slices.ContainsFunc(args, func(p string) bool { return strings.HasPrefix(in.Name, p) }) {
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

type teacherOptions struct {
	texts, out           string
	shardSize, maxShards int
}

func runTeacher(c *common, o *teacherOptions) error {
	texts, out, shardSize, maxShards := &o.texts, &o.out, &o.shardSize, &o.maxShards
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

type parityOptions struct {
	dir, minCos   string
	passes, limit int
}

func runParity(c *common, o *parityOptions) error {
	dir, passes, limit := &o.dir, &o.passes, &o.limit
	minCosValue, err := parseFloat("min-cos", o.minCos)
	if err != nil {
		return err
	}
	minCos := &minCosValue
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
	res.scoreCosines(vecs, refs, sample, *minCos)

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

// scoreCosines fills res's cosine agreement between each encoded row and its
// reference: the mean, the minimum overall and per subset, the 1st percentile, and the
// count of rows below minCos.
func (res *parityResult) scoreCosines(vecs, refs [][]float32, sample []sampleRow, minCos float64) {
	cos := make([]float64, len(vecs))
	res.MinCos = 2
	for i, v := range vecs {
		cos[i] = cosine(v, refs[i])
		res.MeanCos += cos[i] / float64(len(vecs))
		res.MinCos = math.Min(res.MinCos, cos[i])
		if m, ok := res.MinCosSubset[sample[i].Subset]; !ok || cos[i] < m {
			res.MinCosSubset[sample[i].Subset] = cos[i]
		}
		if cos[i] < minCos {
			res.Below++
		}
	}
	sorted := slices.Clone(cos)
	sort.Float64s(sorted)
	res.P01Cos = sorted[len(sorted)/100]
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

var fetchCmd = command.Command{
	Name:   "fetch",
	Short:  "download the pinned inputs named in inputs.yaml and verify their sha256",
	Usage:  "embed fetch [flags] [name-prefix ...]",
	Long:   "With no prefix, fetches every input.",
	Effect: command.EffectLocalWrite,
	Flags:  commonFlags,
	Run:    func(_ context.Context, inv command.Invocation) error { return runFetch(&shared, inv.Args) },
}

// teacherOpts holds the flags of the one command a process runs.
var teacherOpts teacherOptions

var teacherCmd = command.Command{
	Name:   "teacher",
	Short:  "embed a texts file with a teacher model into resumable, content-addressed shards",
	Effect: command.EffectLocalWrite,
	Flags: slices.Concat(commonFlags, []command.Flag{
		{Name: "texts", Kind: command.String, Doc: "JSON lines of {\"id\", \"text\"} to embed", Target: &teacherOpts.texts},
		{Name: "out", Kind: command.String, Doc: "run root; default <cache>/teacher", Target: &teacherOpts.out},
		{Name: "shard-size", Kind: command.Int, Default: 4096, Doc: "texts per shard", Target: &teacherOpts.shardSize},
		{Name: "max-shards", Kind: command.Int, Default: 0, Doc: "stop after encoding this many new shards; 0 runs to the end", Target: &teacherOpts.maxShards},
	}),
	Run: func(_ context.Context, _ command.Invocation) error { return runTeacher(&shared, &teacherOpts) },
}

// parityOpts holds the flags of the one command a process runs.
var parityOpts parityOptions

var parityCmd = command.Command{
	Name:   "teacher-parity",
	Short:  "check the Go teacher against the reference vectors and report texts/s",
	Effect: command.EffectRead,
	Flags: slices.Concat(commonFlags, []command.Flag{
		{Name: "dir", Kind: command.String, Doc: "parity directory with sample.jsonl, ref-<model>.f32, tokens-<model>.jsonl; default <cache>/parity", Target: &parityOpts.dir},
		{Name: "passes", Kind: command.Int, Default: 2, Doc: "encode passes; every pass after the first is timed without compiles", Target: &parityOpts.passes},
		{Name: "min-cos", Kind: command.String, Default: "0.999", Doc: "the per-row cosine every row must reach", Target: &parityOpts.minCos},
		{Name: "limit", Kind: command.Int, Default: 0, Doc: "use only the first N sample texts; 0 uses all", Target: &parityOpts.limit},
	}),
	Run: func(_ context.Context, _ command.Invocation) error { return runParity(&shared, &parityOpts) },
}

// shared holds the flags every teacher command takes; only one command runs per process.
var shared common

var commonFlags = []command.Flag{
	{Name: "inputs", Kind: command.String, Default: "embed/inputs.yaml", Doc: "the pinned inputs file", Target: &shared.inputs},
	{Name: "cache", Kind: command.String, Default: cacheRoot(), Doc: "the cache root inputs.yaml paths are relative to", Target: &shared.cache},
	{Name: "model", Kind: command.String, Doc: "the teacher: CodeRankEmbed or nomic-embed-text-v1.5", Target: &shared.model},
	{Name: "backend", Kind: command.String, Default: "xla", Doc: "xla (PJRT CPU plugin) or go (pure Go, no cgo kernels)", Target: &shared.backend},
	{Name: "threads", Kind: command.Int, Default: 6, Doc: "CPU threads for the backend and the Go runtime", Target: &shared.threads},
	{Name: "batch", Kind: command.Int, Default: 16, Doc: "texts per executed batch", Target: &shared.batch},
	{Name: "bucket", Kind: command.Int, Default: 32, Doc: "padded lengths round up to a multiple of this", Target: &shared.bucket},
	{Name: "max-len", Kind: command.Int, Default: 512, Doc: "token cap, [CLS] and [SEP] included", Target: &shared.maxLen},
}
