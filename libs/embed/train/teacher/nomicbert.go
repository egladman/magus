package teacher

import (
	"cmp"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/gomlx/compute"
	"github.com/gomlx/compute/dtypes"
	"github.com/gomlx/compute/dtypes/float16"
	"github.com/gomlx/gomlx/core/graph"
	"github.com/gomlx/gomlx/core/tensors"
)

// Pooling turns the last hidden state into one vector per text.
type Pooling string

const (
	PoolMean Pooling = "mean"
	PoolCLS  Pooling = "cls"
)

// Config is the subset of a nomic-bert config.json the forward pass honors. LoadConfig
// refuses any setting it would otherwise ignore.
type Config struct {
	Hidden     int     `json:"n_embd"`
	Heads      int     `json:"n_head"`
	Layers     int     `json:"n_layer"`
	Inner      int     `json:"n_inner"`
	Eps        float64 `json:"layer_norm_epsilon"`
	RotaryBase float64 `json:"rotary_emb_base"`
	Vocab      int     `json:"vocab_size"`
	TypeVocab  int     `json:"type_vocab_size"`
	Pooling    Pooling `json:"-"`
}

type rawConfig struct {
	Config
	ModelType      string   `json:"model_type"`
	Activation     string   `json:"activation_function"`
	Prenorm        bool     `json:"prenorm"`
	Interleaved    bool     `json:"rotary_emb_interleaved"`
	RotaryFraction float64  `json:"rotary_emb_fraction"`
	RotaryScaling  *float64 `json:"rotary_scaling_factor"`
	QKVBias        bool     `json:"qkv_proj_bias"`
	FC1Bias        bool     `json:"mlp_fc1_bias"`
	FC2Bias        bool     `json:"mlp_fc2_bias"`
	ParallelBlock  bool     `json:"parallel_block"`
	Causal         bool     `json:"causal"`
	RMSNorm        bool     `json:"use_rms_norm"`
}

type poolingConfig struct {
	CLS          bool `json:"pooling_mode_cls_token"`
	Mean         bool `json:"pooling_mode_mean_tokens"`
	Max          bool `json:"pooling_mode_max_tokens"`
	MeanSqrt     bool `json:"pooling_mode_mean_sqrt_len_tokens"`
	WeightedMean bool `json:"pooling_mode_weightedmean_tokens"`
	LastToken    bool `json:"pooling_mode_lasttoken"`
}

// LoadConfig reads config.json and the sentence-transformers 1_Pooling/config.json from a
// model snapshot directory.
func LoadConfig(dir string) (Config, error) {
	var raw rawConfig
	if err := readJSON(filepath.Join(dir, "config.json"), &raw); err != nil {
		return Config{}, err
	}
	var bad []string
	check := func(ok bool, what string) {
		if !ok {
			bad = append(bad, what)
		}
	}
	check(raw.ModelType == "nomic_bert", "model_type "+raw.ModelType)
	check(raw.Activation == "swiglu", "activation_function "+raw.Activation)
	check(!raw.Prenorm, "prenorm")
	check(!raw.Interleaved, "rotary_emb_interleaved")
	check(raw.RotaryFraction == 1, "rotary_emb_fraction")
	check(raw.RotaryScaling == nil, "rotary_scaling_factor")
	check(!raw.QKVBias && !raw.FC1Bias && !raw.FC2Bias, "projection biases")
	check(!raw.ParallelBlock && !raw.Causal && !raw.RMSNorm, "parallel_block, causal or use_rms_norm")
	check(raw.Hidden > 0 && raw.Heads > 0 && raw.Hidden%raw.Heads == 0 && (raw.Hidden/raw.Heads)%2 == 0, "n_embd/n_head")
	if len(bad) > 0 {
		return Config{}, fmt.Errorf("%s: unsupported nomic-bert settings: %v", dir, bad)
	}
	var pool poolingConfig
	if err := readJSON(filepath.Join(dir, "1_Pooling", "config.json"), &pool); err != nil {
		return Config{}, err
	}
	switch {
	case pool.Max || pool.MeanSqrt || pool.WeightedMean || pool.LastToken || pool.CLS == pool.Mean:
		return Config{}, fmt.Errorf("%s: pooling must be exactly one of cls or mean: %+v", dir, pool)
	case pool.CLS:
		raw.Pooling = PoolCLS
	default:
		raw.Pooling = PoolMean
	}
	return raw.Config, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

type param struct {
	name string
	dims []int
}

// params lists the weights the forward pass reads, in the order Model holds them.
func (c Config) params() []param {
	h, ff := c.Hidden, c.Inner
	ps := []param{
		{"embeddings.word_embeddings.weight", []int{c.Vocab, h}},
		{"embeddings.token_type_embeddings.weight", []int{c.TypeVocab, h}},
		{"emb_ln.weight", []int{h}}, {"emb_ln.bias", []int{h}},
	}
	for i := range c.Layers {
		for _, p := range []param{
			{"attn.Wqkv.weight", []int{3 * h, h}}, {"attn.out_proj.weight", []int{h, h}},
			{"norm1.weight", []int{h}}, {"norm1.bias", []int{h}},
			{"mlp.fc11.weight", []int{ff, h}}, {"mlp.fc12.weight", []int{ff, h}}, {"mlp.fc2.weight", []int{h, ff}},
			{"norm2.weight", []int{h}}, {"norm2.bias", []int{h}},
		} {
			ps = append(ps, param{fmt.Sprintf("encoder.layers.%d.%s", i, p.name), p.dims})
		}
	}
	return ps
}

// Model is a nomic-bert encoder with its weights resident on the host.
type Model struct {
	Config Config
	params []*tensors.Tensor
}

// NewModel checks every tensor in weights against cfg. A missing tensor, a wrong shape or a
// tensor the forward pass would not read is an error.
func NewModel(cfg Config, weights map[string]*tensors.Tensor) (*Model, error) {
	m := &Model{Config: cfg}
	seen := map[string]bool{}
	for _, p := range cfg.params() {
		t, ok := weights[p.name]
		if !ok {
			return nil, fmt.Errorf("weights have no %s", p.name)
		}
		if !slices.Equal(t.Shape().Dimensions, p.dims) || t.DType() != dtypes.Float32 {
			return nil, fmt.Errorf("%s is %s, want float32%v", p.name, t.Shape(), p.dims)
		}
		seen[p.name] = true
		m.params = append(m.params, t)
	}
	for name := range weights {
		if !seen[name] {
			return nil, fmt.Errorf("weights hold %s, which the forward pass does not read", name)
		}
	}
	return m, nil
}

// LoadModel reads config.json, 1_Pooling/config.json and model.safetensors from dir.
func LoadModel(dir string) (*Model, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	w, err := ReadSafetensors(filepath.Join(dir, "model.safetensors"))
	if err != nil {
		return nil, err
	}
	return NewModel(cfg, w)
}

type safetensorsEntry struct {
	DType   string   `json:"dtype"`
	Shape   []int    `json:"shape"`
	Offsets [2]int64 `json:"data_offsets"`
}

// elemSize is the byte width of each safetensors dtype ReadSafetensors reads.
var elemSize = map[string]int{"F32": 4, "F16": 2}

// ReadSafetensors loads every tensor of a safetensors file as float32. F16 widens
// exactly. Any dtype other than F32 or F16 is an error.
func ReadSafetensors(path string) (map[string]*tensors.Tensor, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var n uint64
	if err := binary.Read(f, binary.LittleEndian, &n); err != nil {
		return nil, fmt.Errorf("%s: header length: %w", path, err)
	}
	if n > 1<<26 {
		return nil, fmt.Errorf("%s: header of %d bytes", path, n)
	}
	hdr := make([]byte, n)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return nil, fmt.Errorf("%s: header: %w", path, err)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(hdr, &entries); err != nil {
		return nil, fmt.Errorf("%s: header: %w", path, err)
	}
	delete(entries, "__metadata__")
	out := make(map[string]*tensors.Tensor, len(entries))
	base := int64(8 + n)
	for name, raw := range entries {
		var e safetensorsEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, name, err)
		}
		size, ok := elemSize[e.DType]
		if !ok {
			return nil, fmt.Errorf("%s: %s is %s, want F32 or F16", path, name, e.DType)
		}
		count := 1
		for _, d := range e.Shape {
			count *= d
		}
		if e.Offsets[1]-e.Offsets[0] != int64(size*count) {
			return nil, fmt.Errorf("%s: %s spans %d bytes for shape %v", path, name, e.Offsets[1]-e.Offsets[0], e.Shape)
		}
		buf := make([]byte, size*count)
		if _, err := f.ReadAt(buf, base+e.Offsets[0]); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, name, err)
		}
		out[name] = tensors.FromFlatDataAndDimensions(decodeFloats(buf, size), e.Shape...)
	}
	return out, nil
}

// decodeFloats widens little-endian elements of size bytes, 2 for F16 and 4 for F32.
func decodeFloats(buf []byte, size int) []float32 {
	vals := make([]float32, len(buf)/size)
	for i := range vals {
		if size == 2 {
			vals[i] = float16.FromBits(binary.LittleEndian.Uint16(buf[2*i:])).Float32()
		} else {
			vals[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4*i:]))
		}
	}
	return vals
}

// forward builds the encoder over ids [B, L] int32 and mask [B, L] float32 (1 for a token,
// 0 for padding) and returns L2-normalized pooled vectors [B, Hidden]. params follow
// Config.params. Mirrors contrastors' NomicBertModel at prenorm=false: post-LayerNorm,
// non-interleaved rotary on q and k, SwiGLU fc11 * silu(fc12), no biases.
func (c Config) forward(ids, mask *graph.Node, params []*graph.Node) *graph.Node {
	g := ids.Graph()
	b, l := ids.Shape().Dimensions[0], ids.Shape().Dimensions[1]
	h, nh := c.Hidden, c.Heads
	hd := h / nh
	index := map[string]int{}
	for i, p := range c.params() {
		index[p.name] = i
	}
	p := func(name string) *graph.Node { return params[index[name]] }
	ln := func(x, w, bias *graph.Node) *graph.Node { return layerNorm(x, w, bias, c.Eps) }
	proj := func(x, w *graph.Node) *graph.Node { return graph.Einsum("blh,oh->blo", x, w) }

	x := graph.Gather(p("embeddings.word_embeddings.weight"), graph.ExpandAxes(ids, -1))
	tt := graph.Slice(p("embeddings.token_type_embeddings.weight"), graph.AxisElem(0))
	x = ln(graph.Add(x, graph.Reshape(tt, 1, 1, h)), p("emb_ln.weight"), p("emb_ln.bias"))

	cos, sin := c.rotary(l)
	cosN := graph.Reshape(graph.Const(g, cos), 1, l, 1, hd/2)
	sinN := graph.Reshape(graph.Const(g, sin), 1, l, 1, hd/2)
	rot := func(t *graph.Node) *graph.Node { return rope(t, cosN, sinN) }
	// Padding keys get -1e9 rather than -inf: softmax still gives them exactly 0, and a
	// fully padded filler row stays finite.
	keyBias := graph.Reshape(graph.MulScalar(graph.AddScalar(mask, -1), 1e9), b, 1, 1, l)

	for i := range c.Layers {
		lp := func(n string) *graph.Node { return p(fmt.Sprintf("encoder.layers.%d.%s", i, n)) }
		qkv := graph.Reshape(proj(x, lp("attn.Wqkv.weight")), b, l, 3, nh, hd)
		part := func(j int) *graph.Node {
			return graph.Reshape(graph.Slice(qkv, graph.AxisRange(), graph.AxisRange(), graph.AxisElem(j)), b, l, nh, hd)
		}
		q, k, v := rot(part(0)), rot(part(1)), part(2)
		scores := graph.MulScalar(graph.Einsum("bqnd,bknd->bnqk", q, k), 1/math.Sqrt(float64(hd)))
		attn := graph.Softmax(graph.Add(scores, keyBias), -1)
		a := graph.Reshape(graph.Einsum("bnqk,bknd->bqnd", attn, v), b, l, h)
		x = ln(graph.Add(proj(a, lp("attn.out_proj.weight")), x), lp("norm1.weight"), lp("norm1.bias"))
		y := proj(x, lp("mlp.fc11.weight"))
		gate := proj(x, lp("mlp.fc12.weight"))
		m := proj(graph.Mul(y, graph.Mul(gate, graph.Sigmoid(gate))), lp("mlp.fc2.weight"))
		x = ln(graph.Add(m, x), lp("norm2.weight"), lp("norm2.bias"))
	}
	pooled := c.pool(x, mask)
	return graph.Mul(pooled, graph.Rsqrt(graph.ReduceAndKeep(graph.Square(pooled), graph.ReduceSum, -1)))
}

// layerNorm normalizes x [B, L, H] over its last axis, then scales by w and shifts by
// bias, both [H].
func layerNorm(x, w, bias *graph.Node, eps float64) *graph.Node {
	h := x.Shape().Dimensions[2]
	mu := graph.ReduceAndKeep(x, graph.ReduceMean, -1)
	d := graph.Sub(x, mu)
	v := graph.ReduceAndKeep(graph.Square(d), graph.ReduceMean, -1)
	return graph.Add(graph.Mul(graph.Mul(d, graph.Rsqrt(graph.AddScalar(v, eps))), graph.Reshape(w, 1, 1, h)),
		graph.Reshape(bias, 1, 1, h))
}

// rope applies non-interleaved rotary position embedding to t [B, L, heads, hd]: its
// first and second halves are rotated as pairs by the angles cos and sin [1, L, 1, hd/2]
// hold.
func rope(t, cos, sin *graph.Node) *graph.Node {
	hd := t.Shape().Dimensions[3]
	t1 := graph.Slice(t, graph.AxisRange(), graph.AxisRange(), graph.AxisRange(), graph.AxisRange(0, hd/2))
	t2 := graph.Slice(t, graph.AxisRange(), graph.AxisRange(), graph.AxisRange(), graph.AxisRange(hd/2, hd))
	return graph.Concatenate([]*graph.Node{
		graph.Sub(graph.Mul(t1, cos), graph.Mul(t2, sin)),
		graph.Add(graph.Mul(t1, sin), graph.Mul(t2, cos)),
	}, -1)
}

// pool reduces the encoder output x [B, L, H] to one vector per text [B, H]: the CLS
// position, or the mean over the tokens mask marks.
func (c Config) pool(x, mask *graph.Node) *graph.Node {
	if c.Pooling == PoolCLS {
		b, h := x.Shape().Dimensions[0], x.Shape().Dimensions[2]
		return graph.Reshape(graph.Slice(x, graph.AxisRange(), graph.AxisElem(0)), b, h)
	}
	m := graph.ExpandAxes(mask, -1)
	return graph.Div(graph.ReduceSum(graph.Mul(x, m), 1), graph.ReduceSum(m, 1))
}

// rotary returns cos and sin tables [l][hd/2] computed the way torch does in float32:
// inv = 1 / base^(2i/hd), angle = pos * inv, each step rounded to float32.
func (c Config) rotary(l int) (cos, sin [][]float32) {
	hd := c.Hidden / c.Heads
	inv := make([]float32, hd/2)
	for i := range inv {
		e := float32(2*i) / float32(hd)
		inv[i] = 1 / float32(math.Pow(c.RotaryBase, float64(e)))
	}
	cos, sin = make([][]float32, l), make([][]float32, l)
	for pos := range l {
		cos[pos], sin[pos] = make([]float32, hd/2), make([]float32, hd/2)
		for i, f := range inv {
			a := float64(float32(pos) * f)
			cos[pos][i], sin[pos][i] = float32(math.Cos(a)), float32(math.Sin(a))
		}
	}
	return cos, sin
}

// Encoder embeds texts with one Model on one backend. Not safe for concurrent use.
type Encoder struct {
	model  *Model
	tok    *WordPiece
	exec   *graph.Exec
	opts   EncoderOptions
	tokens int
}

// EncoderOptions fix how texts reach the model.
type EncoderOptions struct {
	// Prefix is prepended to every text before tokenizing, e.g. "search_document: ".
	Prefix string
	// MaxLen caps the token count, [CLS] and [SEP] included.
	MaxLen int
	// Batch is the row count of every executed batch; a short batch is padded to it.
	Batch int
	// Bucket rounds every padded length up to a multiple of it, so the number of compiled
	// shapes stays near MaxLen/Bucket.
	Bucket int
}

// NewEncoder compiles lazily: the first batch of each padded shape pays the compile.
func NewEncoder(backend compute.Backend, m *Model, tok *WordPiece, opts EncoderOptions) (*Encoder, error) {
	if opts.MaxLen < 2 || opts.Batch < 1 || opts.Bucket < 1 {
		return nil, fmt.Errorf("encoder options out of range: %+v", opts)
	}
	exec, err := graph.NewExec(backend, func(_ *graph.Graph, in []*graph.Node) *graph.Node {
		return m.Config.forward(in[0], in[1], in[2:])
	})
	if err != nil {
		return nil, err
	}
	exec.SetMaxCache(opts.MaxLen/opts.Bucket + 2)
	return &Encoder{model: m, tok: tok, exec: exec, opts: opts}, nil
}

// Tokens is the count of non-padding tokens encoded so far.
func (e *Encoder) Tokens() int { return e.tokens }

// Close frees the compiled executables.
func (e *Encoder) Close() { e.exec.Finalize() }

// Encode returns one L2-normalized row of Hidden floats per text, in input order.
func (e *Encoder) Encode(texts []string) ([][]float32, error) {
	ids := make([][]int32, len(texts))
	for i, t := range texts {
		ids[i] = e.tok.Encode(e.opts.Prefix+t, e.opts.MaxLen)
		e.tokens += len(ids[i])
	}
	order := make([]int, len(texts))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(len(ids[a]), len(ids[b])) })
	out := make([][]float32, len(texts))
	for s := 0; s < len(order); s += e.opts.Batch {
		rows := order[s:min(s+e.opts.Batch, len(order))]
		vecs, err := e.run(ids, rows)
		if err != nil {
			return nil, err
		}
		for j, r := range rows {
			out[r] = vecs[j]
		}
	}
	return out, nil
}

func (e *Encoder) run(ids [][]int32, rows []int) ([][]float32, error) {
	l := 0
	for _, r := range rows {
		l = max(l, len(ids[r]))
	}
	l = min((l+e.opts.Bucket-1)/e.opts.Bucket*e.opts.Bucket, max(e.opts.MaxLen, l))
	b := e.opts.Batch
	flatIDs := make([]int32, b*l)
	flatMask := make([]float32, b*l)
	for j := range b {
		src := ids[rows[min(j, len(rows)-1)]]
		copy(flatIDs[j*l:], src)
		for k := range src {
			flatMask[j*l+k] = 1
		}
	}
	args := []any{tensors.FromFlatDataAndDimensions(flatIDs, b, l), tensors.FromFlatDataAndDimensions(flatMask, b, l)}
	for _, t := range e.model.params {
		args = append(args, t)
	}
	res, err := e.exec.Exec1(args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.FinalizeAll() }()
	flat, err := tensors.CopyFlatData[float32](res)
	if err != nil {
		return nil, err
	}
	h := e.model.Config.Hidden
	vecs := make([][]float32, len(rows))
	for j := range rows {
		vecs[j] = flat[j*h : (j+1)*h]
	}
	return vecs, nil
}
