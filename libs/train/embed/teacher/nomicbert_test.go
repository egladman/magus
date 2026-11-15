package teacher

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gomlx/compute/dtypes/float16"
	"github.com/gomlx/compute/gobackend"
	"github.com/gomlx/gomlx/core/tensors"
)

var tinyVocab = []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]", "[MASK]", "the", "cat", "sat", "on", "mat",
	"a", "dog", "ran", "##s", ".", ",", "big", "red", "x", "y"}

func tinyConfig(pool Pooling) Config {
	return Config{Hidden: 8, Heads: 2, Layers: 2, Inner: 12, Eps: 1e-12, RotaryBase: 1000,
		Vocab: len(tinyVocab), TypeVocab: 2, Pooling: pool}
}

// tinyWeights fills every parameter with seeded values; LayerNorm gains sit near 1 so the
// activations stay in a range where float32 and float64 agree.
func tinyWeights(cfg Config, seed uint64) map[string][]float32 {
	r := rand.New(rand.NewPCG(seed, 1))
	w := map[string][]float32{}
	for _, p := range cfg.params() {
		n := 1
		for _, d := range p.dims {
			n *= d
		}
		v := make([]float32, n)
		for i := range v {
			v[i] = float32(r.NormFloat64() * 0.4)
			if strings.HasSuffix(p.name, "norm1.weight") || strings.HasSuffix(p.name, "norm2.weight") || p.name == "emb_ln.weight" {
				v[i] = 1 + v[i]/4
			}
		}
		w[p.name] = v
	}
	return w
}

func tinyModel(t *testing.T, cfg Config, w map[string][]float32) *Model {
	t.Helper()
	ts := map[string]*tensors.Tensor{}
	for _, p := range cfg.params() {
		ts[p.name] = tensors.FromFlatDataAndDimensions(w[p.name], p.dims...)
	}
	m, err := NewModel(cfg, ts)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// naiveForward is the reference: one text, no padding, float64, loops only. It follows
// bench/nomic_local.py line for line.
func naiveForward(cfg Config, w map[string][]float32, ids []int32) []float64 {
	m := naive{cfg: cfg, w: w}
	h := cfg.Hidden
	x := make([][]float64, len(ids))
	for p, id := range ids {
		x[p] = make([]float64, h)
		for j := range h {
			x[p][j] = m.at("embeddings.word_embeddings.weight", int(id), j, h) + m.at("embeddings.token_type_embeddings.weight", 0, j, h)
		}
	}
	m.layerNorm(x, "emb_ln.weight", "emb_ln.bias")
	for l := range cfg.Layers {
		m.layer(x, fmt.Sprintf("encoder.layers.%d.", l))
	}
	return m.pool(x)
}

// naive holds the reference encoder's config and weights.
type naive struct {
	cfg Config
	w   map[string][]float32
}

func (m naive) at(name string, i, j, cols int) float64 { return float64(m.w[name][i*cols+j]) }

// layerNorm normalizes each row of x in place, then scales by weight wn and shifts by
// bias bn.
func (m naive) layerNorm(x [][]float64, wn, bn string) {
	h := float64(m.cfg.Hidden)
	for _, row := range x {
		var mu, v float64
		for _, e := range row {
			mu += e / h
		}
		for _, e := range row {
			v += (e - mu) * (e - mu) / h
		}
		for j := range row {
			row[j] = (row[j]-mu)/math.Sqrt(v+m.cfg.Eps)*float64(m.w[wn][j]) + float64(m.w[bn][j])
		}
	}
}

// linear is x times the transpose of the [out, in] weight name.
func (m naive) linear(x [][]float64, name string, out int) [][]float64 {
	in := len(x[0])
	y := make([][]float64, len(x))
	for r := range x {
		y[r] = make([]float64, out)
		for o := range out {
			for i := range in {
				y[r][o] += x[r][i] * m.at(name, o, i, in)
			}
		}
	}
	return y
}

// rope rotates the head vector v at position pos, its halves paired, not interleaved.
func (m naive) rope(v []float64, pos int) []float64 {
	hd := len(v)
	out := make([]float64, hd)
	for i := range hd / 2 {
		inv := 1 / math.Pow(m.cfg.RotaryBase, float64(2*i)/float64(hd))
		c, s := math.Cos(float64(pos)*inv), math.Sin(float64(pos)*inv)
		out[i] = v[i]*c - v[i+hd/2]*s
		out[i+hd/2] = v[i]*s + v[i+hd/2]*c
	}
	return out
}

// attention is multi-head self-attention over x through the layer's fused qkv weight,
// before the output projection.
func (m naive) attention(x [][]float64, pre string) [][]float64 {
	h, nh, n := m.cfg.Hidden, m.cfg.Heads, len(x)
	hd := h / nh
	qkv := m.linear(x, pre+"attn.Wqkv.weight", 3*h)
	a := make([][]float64, n)
	for p := range a {
		a[p] = make([]float64, h)
	}
	for head := range nh {
		q, k, v := make([][]float64, n), make([][]float64, n), make([][]float64, n)
		for p := range n {
			q[p] = m.rope(qkv[p][head*hd:(head+1)*hd], p)
			k[p] = m.rope(qkv[p][h+head*hd:h+(head+1)*hd], p)
			v[p] = qkv[p][2*h+head*hd : 2*h+(head+1)*hd]
		}
		for i := range n {
			scores := make([]float64, n)
			mx := math.Inf(-1)
			for j := range n {
				for d := range hd {
					scores[j] += q[i][d] * k[j][d]
				}
				scores[j] /= math.Sqrt(float64(hd))
				mx = math.Max(mx, scores[j])
			}
			var z float64
			for j := range scores {
				scores[j] = math.Exp(scores[j] - mx)
				z += scores[j]
			}
			for j := range n {
				for d := range hd {
					a[i][head*hd+d] += scores[j] / z * v[j][d]
				}
			}
		}
	}
	return a
}

// layer is one post-LayerNorm encoder layer over x, in place: attention, then the
// SwiGLU MLP fc11 * silu(fc12), each added to its input before its LayerNorm.
func (m naive) layer(x [][]float64, pre string) {
	h, ff := m.cfg.Hidden, m.cfg.Inner
	addRows(x, m.linear(m.attention(x, pre), pre+"attn.out_proj.weight", h))
	m.layerNorm(x, pre+"norm1.weight", pre+"norm1.bias")
	y, g := m.linear(x, pre+"mlp.fc11.weight", ff), m.linear(x, pre+"mlp.fc12.weight", ff)
	for p := range y {
		for j := range y[p] {
			y[p][j] *= g[p][j] / (1 + math.Exp(-g[p][j]))
		}
	}
	addRows(x, m.linear(y, pre+"mlp.fc2.weight", h))
	m.layerNorm(x, pre+"norm2.weight", pre+"norm2.bias")
}

func addRows(x, y [][]float64) {
	for p := range x {
		for j := range x[p] {
			x[p][j] += y[p][j]
		}
	}
}

// pool is the CLS row or the mean row of x, L2-normalized.
func (m naive) pool(x [][]float64) []float64 {
	pooled := make([]float64, m.cfg.Hidden)
	if m.cfg.Pooling == PoolCLS {
		copy(pooled, x[0])
	} else {
		for p := range x {
			for j := range pooled {
				pooled[j] += x[p][j] / float64(len(x))
			}
		}
	}
	var norm float64
	for _, e := range pooled {
		norm += e * e
	}
	for j := range pooled {
		pooled[j] /= math.Sqrt(norm)
	}
	return pooled
}

func TestEncoderMatchesNaiveForward(t *testing.T) {
	tok, err := NewWordPiece(tinyVocab)
	if err != nil {
		t.Fatal(err)
	}
	// Lengths 3 to 12 tokens across batches of 2 with bucket 4: padded keys, a filler row in
	// the last batch and two compiled shapes.
	texts := []string{"the cat sat on the mat.", "dog", "a big red dog ran, the cats sat", "x y", "mat"}
	be, err := gobackend.New("")
	if err != nil {
		t.Fatal(err)
	}
	for _, pool := range []Pooling{PoolMean, PoolCLS} {
		t.Run(string(pool), func(t *testing.T) {
			cfg := tinyConfig(pool)
			w := tinyWeights(cfg, 7)
			enc, err := NewEncoder(be, tinyModel(t, cfg, w), tok, EncoderOptions{MaxLen: 16, Batch: 2, Bucket: 4})
			if err != nil {
				t.Fatal(err)
			}
			defer enc.Close()
			got, err := enc.Encode(texts)
			if err != nil {
				t.Fatal(err)
			}
			for i, text := range texts {
				want := naiveForward(cfg, w, tok.Encode(text, 16))
				var maxDiff float64
				for j := range want {
					maxDiff = math.Max(maxDiff, math.Abs(float64(got[i][j])-want[j]))
				}
				if maxDiff > 1e-4 {
					t.Errorf("text %q: max |go - naive| = %g\n go    %v\n naive %v", text, maxDiff, got[i], want)
				}
			}
		})
	}
}

func TestEncoderPrefixAndTruncation(t *testing.T) {
	tok, err := NewWordPiece(tinyVocab)
	if err != nil {
		t.Fatal(err)
	}
	be, err := gobackend.New("")
	if err != nil {
		t.Fatal(err)
	}
	cfg := tinyConfig(PoolMean)
	w := tinyWeights(cfg, 3)
	enc, err := NewEncoder(be, tinyModel(t, cfg, w), tok, EncoderOptions{Prefix: "the ", MaxLen: 5, Batch: 1, Bucket: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	got, err := enc.Encode([]string{"cat sat on the mat"})
	if err != nil {
		t.Fatal(err)
	}
	// [CLS] the cat sat [SEP]: the prefix counts against the cap, and the bucket of 8 is
	// clamped to the cap of 5.
	want := naiveForward(cfg, w, []int32{2, 5, 6, 7, 3})
	for j := range want {
		if math.Abs(float64(got[0][j])-want[j]) > 1e-4 {
			t.Fatalf("got %v, want %v", got[0], want)
		}
	}
	if enc.Tokens() != 5 {
		t.Errorf("Tokens() = %d, want 5", enc.Tokens())
	}
}

func writeSafetensors(t *testing.T, path string, cfg Config, w map[string][]float32, dtype string) {
	t.Helper()
	hdr := map[string]any{"__metadata__": map[string]string{"format": "pt"}}
	var data []byte
	for _, p := range cfg.params() {
		start := len(data)
		for _, v := range w[p.name] {
			if dtype == "F16" {
				data = binary.LittleEndian.AppendUint16(data, float16.FromFloat32(v).Bits())
			} else {
				data = binary.LittleEndian.AppendUint32(data, math.Float32bits(v))
			}
		}
		hdr[p.name] = map[string]any{"dtype": dtype, "shape": p.dims, "data_offsets": []int{start, len(data)}}
	}
	hb, err := json.Marshal(hdr)
	if err != nil {
		t.Fatal(err)
	}
	out := binary.LittleEndian.AppendUint64(nil, uint64(len(hb)))
	if err := os.WriteFile(path, append(append(out, hb...), data...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeModelDir(t *testing.T, cfgJSON, poolJSON string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "1_Pooling"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfgJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1_Pooling", "config.json"), []byte(poolJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const tinyConfigJSON = `{"model_type": "nomic_bert", "activation_function": "swiglu", "n_embd": 8, "n_head": 2,
	"n_layer": 2, "n_inner": 12, "layer_norm_epsilon": 1e-12, "rotary_emb_base": 1000, "rotary_emb_fraction": 1.0,
	"rotary_emb_interleaved": false, "rotary_scaling_factor": null, "prenorm": false, "qkv_proj_bias": false,
	"mlp_fc1_bias": false, "mlp_fc2_bias": false, "vocab_size": 20, "type_vocab_size": 2}`

func TestLoadModelReadsSafetensors(t *testing.T) {
	dir := writeModelDir(t, tinyConfigJSON, `{"pooling_mode_cls_token": true, "pooling_mode_mean_tokens": false}`)
	cfg := tinyConfig(PoolCLS)
	w := tinyWeights(cfg, 11)
	for _, dtype := range []string{"F32", "F16"} {
		writeSafetensors(t, filepath.Join(dir, "model.safetensors"), cfg, w, dtype)
		m, err := LoadModel(dir)
		if err != nil {
			t.Fatalf("%s: %v", dtype, err)
		}
		if m.Config != cfg {
			t.Errorf("%s: config = %+v, want %+v", dtype, m.Config, cfg)
		}
		for i, p := range cfg.params() {
			got, err := tensors.CopyFlatData[float32](m.params[i])
			if err != nil {
				t.Fatal(err)
			}
			for j := range got {
				want := w[p.name][j]
				if dtype == "F16" {
					want = float16.FromFloat32(want).Float32()
				}
				if got[j] != want {
					t.Fatalf("%s: %s[%d] = %g, want %g", dtype, p.name, j, got[j], want)
				}
			}
		}
	}

	writeSafetensors(t, filepath.Join(dir, "model.safetensors"), cfg, w, "BF16")
	if _, err := LoadModel(dir); err == nil || !strings.Contains(err.Error(), "want F32 or F16") {
		t.Errorf("a BF16 tensor loaded: %v", err)
	}
}

func TestLoadConfigRefusesWhatItWouldIgnore(t *testing.T) {
	mean := `{"pooling_mode_cls_token": false, "pooling_mode_mean_tokens": true}`
	cases := map[string][2]string{
		"prenorm":     {strings.Replace(tinyConfigJSON, `"prenorm": false`, `"prenorm": true`, 1), mean},
		"interleaved": {strings.Replace(tinyConfigJSON, `"rotary_emb_interleaved": false`, `"rotary_emb_interleaved": true`, 1), mean},
		"qkv bias":    {strings.Replace(tinyConfigJSON, `"qkv_proj_bias": false`, `"qkv_proj_bias": true`, 1), mean},
		"no pooling":  {tinyConfigJSON, `{"pooling_mode_cls_token": false, "pooling_mode_mean_tokens": false}`},
		"two pools":   {tinyConfigJSON, `{"pooling_mode_cls_token": true, "pooling_mode_mean_tokens": true}`},
		"max pooling": {tinyConfigJSON, `{"pooling_mode_mean_tokens": true, "pooling_mode_max_tokens": true}`},
	}
	for name, c := range cases {
		if _, err := LoadConfig(writeModelDir(t, c[0], c[1])); err == nil {
			t.Errorf("%s: LoadConfig accepted it", name)
		}
	}
	if cfg, err := LoadConfig(writeModelDir(t, tinyConfigJSON, mean)); err != nil || cfg.Pooling != PoolMean {
		t.Errorf("LoadConfig = %+v, %v; want mean pooling", cfg, err)
	}
}

func TestNewModelRefusesMissingOrExtraTensors(t *testing.T) {
	cfg := tinyConfig(PoolMean)
	w := tinyWeights(cfg, 5)
	build := func(edit func(map[string]*tensors.Tensor)) error {
		ts := map[string]*tensors.Tensor{}
		for _, p := range cfg.params() {
			ts[p.name] = tensors.FromFlatDataAndDimensions(w[p.name], p.dims...)
		}
		edit(ts)
		_, err := NewModel(cfg, ts)
		return err
	}
	if err := build(func(ts map[string]*tensors.Tensor) { delete(ts, "encoder.layers.1.mlp.fc2.weight") }); err == nil {
		t.Error("a missing tensor was accepted")
	}
	if err := build(func(ts map[string]*tensors.Tensor) {
		ts["pooler.dense.weight"] = tensors.FromFlatDataAndDimensions([]float32{1}, 1)
	}); err == nil {
		t.Error("an unread tensor was accepted")
	}
	if err := build(func(ts map[string]*tensors.Tensor) {
		ts["emb_ln.bias"] = tensors.FromFlatDataAndDimensions(make([]float32, 9), 9)
	}); err == nil {
		t.Error("a misshapen tensor was accepted")
	}
}
