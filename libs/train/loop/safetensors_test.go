package loop

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type stTensor struct {
	name  string
	dtype string
	shape []int64
	data  []byte
}

func writeTestSafetensors(path string, tensors []stTensor, metadata map[string]string) error {
	hdr := map[string]any{}
	if metadata != nil {
		hdr["__metadata__"] = metadata
	}
	var data []byte
	for _, t := range tensors {
		hdr[t.name] = map[string]any{"dtype": t.dtype, "shape": t.shape, "data_offsets": []int{len(data), len(data) + len(t.data)}}
		data = append(data, t.data...)
	}
	raw, err := json.Marshal(hdr)
	if err != nil {
		return err
	}
	raw = append(raw, bytes.Repeat([]byte(" "), (8-len(raw)%8)%8)...)
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint64(len(raw))); err != nil {
		return err
	}
	buf.Write(raw)
	buf.Write(data)
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// readTestSafetensors parses a file without the package's reader, so a reader bug cannot
// hide a writer bug.
func readTestSafetensors(t *testing.T, path string) map[string]stTensor {
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	n := binary.LittleEndian.Uint64(raw)
	require.Zero(t, n%8, "header is padded to 8 bytes")
	var hdr map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw[8:8+n], &hdr))
	data := raw[8+n:]
	out := map[string]stTensor{}
	covered := 0
	for name, field := range hdr {
		if name == "__metadata__" {
			out[name] = stTensor{name: name, data: field}
			continue
		}
		var e struct {
			DType   string   `json:"dtype"`
			Shape   []int64  `json:"shape"`
			Offsets [2]int64 `json:"data_offsets"`
		}
		require.NoError(t, json.Unmarshal(field, &e))
		out[name] = stTensor{name: name, dtype: e.DType, shape: e.Shape, data: data[e.Offsets[0]:e.Offsets[1]]}
		covered += int(e.Offsets[1] - e.Offsets[0])
	}
	require.Equal(t, len(data), covered, "tensors cover the data section with no holes")
	return out
}

type SafetensorsSuite struct {
	suite.Suite
	dir  string
	plan Plan
}

func TestLoopSafetensors(t *testing.T) { suite.Run(t, new(SafetensorsSuite)) }

func (s *SafetensorsSuite) SetupTest() {
	s.dir = s.T().TempDir()
	var err error
	s.plan, err = NewPlan(6, 2, 4, 3)
	s.Require().NoError(err)
}

// sixLayers is a 6-layer checkpoint whose every tensor holds bytes unique to it, in
// BF16 and F32 so the copy is shown to be dtype-blind.
func sixLayers() []stTensor {
	fill := func(seed, n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(seed*31 + i)
		}
		return b
	}
	tensors := []stTensor{{name: "model.embed_tokens.weight", dtype: "BF16", shape: []int64{8, 4}, data: fill(100, 64)}}
	for l := range 6 {
		p := layerPrefix + strconv.Itoa(l) + "."
		tensors = append(tensors,
			stTensor{name: p + "self_attn.q_proj.weight", dtype: "BF16", shape: []int64{4, 4}, data: fill(l, 32)},
			stTensor{name: p + "input_layernorm.weight", dtype: "F32", shape: []int64{4}, data: fill(l+50, 16)})
	}
	return append(tensors, stTensor{name: "model.norm.weight", dtype: "F32", shape: []int64{4}, data: fill(200, 16)})
}

func (s *SafetensorsSuite) write(name string, tensors []stTensor) string {
	path := filepath.Join(s.dir, name)
	s.Require().NoError(writeTestSafetensors(path, tensors, map[string]string{"format": "mlx"}))
	return path
}

func (s *SafetensorsSuite) TestEveryUnrolledLayerIsItsSourceByteForByte() {
	src := s.write("src.safetensors", sixLayers())
	dst := filepath.Join(s.dir, "dst.safetensors")
	s.Require().NoError(UnrollSafetensors(dst, src, s.plan))
	in, out := readTestSafetensors(s.T(), src), readTestSafetensors(s.T(), dst)

	s.Len(out, 3+10*2, "metadata, embed, norm and two tensors for each of 10 layers")
	for i := range s.plan.Applications() {
		from, err := s.plan.Source(i)
		s.Require().NoError(err)
		for _, suffix := range []string{"self_attn.q_proj.weight", "input_layernorm.weight"} {
			want := in[layerPrefix+strconv.Itoa(from)+"."+suffix]
			got, ok := out[layerPrefix+strconv.Itoa(i)+"."+suffix]
			s.Require().True(ok, "layer %d %s", i, suffix)
			s.Equal(want.data, got.data, "layer %d copies layer %d", i, from)
			s.Equal(want.dtype, got.dtype)
			s.Equal(want.shape, got.shape)
		}
	}
	s.NotContains(out, layerPrefix+"10.input_layernorm.weight")
	for _, name := range []string{"model.embed_tokens.weight", "model.norm.weight", "__metadata__"} {
		s.Equal(in[name].data, out[name].data, name)
	}
}

func (s *SafetensorsSuite) TestPreludeAndCodaKeepTheirBytesAtTheirNewIndices() {
	src := s.write("src.safetensors", sixLayers())
	dst := filepath.Join(s.dir, "dst.safetensors")
	s.Require().NoError(UnrollSafetensors(dst, src, s.plan))
	in, out := readTestSafetensors(s.T(), src), readTestSafetensors(s.T(), dst)
	name := func(l int) string { return layerPrefix + strconv.Itoa(l) + ".self_attn.q_proj.weight" }
	s.Equal(in[name(0)].data, out[name(0)].data)
	s.Equal(in[name(1)].data, out[name(1)].data)
	s.Equal(in[name(4)].data, out[name(8)].data)
	s.Equal(in[name(5)].data, out[name(9)].data)
	s.NotEqual(in[name(4)].data, out[name(4)].data, "unrolled layer 4 is the second pass of layer 2")
}

func (s *SafetensorsSuite) TestRefusesALayerPastThePlan() {
	tensors := append(sixLayers(), stTensor{name: layerPrefix + "6.mlp.weight", dtype: "F32", shape: []int64{1}, data: make([]byte, 4)})
	src := s.write("src.safetensors", tensors)
	err := UnrollSafetensors(filepath.Join(s.dir, "dst.safetensors"), src, s.plan)
	s.ErrorIs(err, ErrIndex)
	s.NoFileExists(filepath.Join(s.dir, "dst.safetensors"))
}

func (s *SafetensorsSuite) TestRefusesASizeThatDisagreesWithTheShape() {
	src := s.write("src.safetensors", []stTensor{{name: "x", dtype: "F32", shape: []int64{3}, data: make([]byte, 8)}})
	s.ErrorIs(UnrollSafetensors(filepath.Join(s.dir, "dst"), src, s.plan), ErrHeader)
}

func (s *SafetensorsSuite) TestRefusesAnUnknownDType() {
	src := s.write("src.safetensors", []stTensor{{name: "x", dtype: "Q9", shape: []int64{1}, data: make([]byte, 1)}})
	s.ErrorIs(UnrollSafetensors(filepath.Join(s.dir, "dst"), src, s.plan), ErrDType)
}

func (s *SafetensorsSuite) TestRefusesWritingOverTheSource() {
	src := s.write("src.safetensors", sixLayers())
	s.ErrorIs(UnrollSafetensors(src, src, s.plan), ErrSameFile)
}

func (s *SafetensorsSuite) writeConfig(dir string, cfg string) string {
	path := filepath.Join(dir, "config.json")
	s.Require().NoError(os.WriteFile(path, []byte(cfg), 0o644))
	return path
}

func (s *SafetensorsSuite) readJSON(path string) map[string]any {
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	s.Require().NoError(dec.Decode(&out))
	return out
}

func (s *SafetensorsSuite) TestConfigRaisesTheLayerCountAndUnrollsPerLayerKeys() {
	src := s.writeConfig(s.dir, `{"model_type": "qwen2", "num_hidden_layers": 6, "rms_norm_eps": 1e-06,
		"use_sliding_window": false, "layer_types": ["a", "b", "c", "d", "e", "f"],
		"quantization": {"group_size": 64, "bits": 4, "model.layers.3.mlp.down_proj": {"bits": 8}}}`)
	n, err := ConfigLayers(src)
	s.Require().NoError(err)
	s.Equal(6, n)
	dst := filepath.Join(s.dir, "out.json")
	s.Require().NoError(UnrollConfig(dst, src, s.plan))
	got := s.readJSON(dst)
	s.Equal(json.Number("10"), got["num_hidden_layers"])
	s.Equal(json.Number("1e-06"), got["rms_norm_eps"], "numbers keep their source text")
	s.Equal([]any{"a", "b", "c", "d", "c", "d", "c", "d", "e", "f"}, got["layer_types"])
	q := got["quantization"].(map[string]any)
	s.Equal(json.Number("64"), q["group_size"])
	for _, l := range []string{"3", "5", "7"} {
		s.Contains(q, "model.layers."+l+".mlp.down_proj")
	}
	s.NotContains(q, "model.layers.4.mlp.down_proj")
}

func (s *SafetensorsSuite) TestConfigRefusesALayerCountOtherThanThePlans() {
	src := s.writeConfig(s.dir, `{"num_hidden_layers": 7}`)
	s.ErrorIs(UnrollConfig(filepath.Join(s.dir, "out.json"), src, s.plan), ErrLayers)
}

func (s *SafetensorsSuite) TestConfigRefusesSlidingWindow() {
	src := s.writeConfig(s.dir, `{"num_hidden_layers": 6, "use_sliding_window": true, "max_window_layers": 4}`)
	s.ErrorIs(UnrollConfig(filepath.Join(s.dir, "out.json"), src, s.plan), ErrSlidingWindow)
}

func (s *SafetensorsSuite) sourceDir(shards map[string][]stTensor, index bool) string {
	dir := filepath.Join(s.dir, "src")
	s.Require().NoError(os.Mkdir(dir, 0o755))
	s.writeConfig(dir, `{"num_hidden_layers": 6}`)
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "tokenizer.json"), []byte(`{"v": 1}`), 0o644))
	weightMap := map[string]string{}
	for name, tensors := range shards {
		s.Require().NoError(writeTestSafetensors(filepath.Join(dir, name), tensors, nil))
		for _, t := range tensors {
			weightMap[t.name] = name
		}
	}
	if index {
		raw, err := json.Marshal(map[string]any{"metadata": map[string]any{"total_size": 1}, "weight_map": weightMap})
		s.Require().NoError(err)
		s.Require().NoError(os.WriteFile(filepath.Join(dir, indexFile), raw, 0o644))
	}
	return dir
}

func (s *SafetensorsSuite) TestCheckpointUnrollsEveryShardAndCopiesTheRest() {
	all := sixLayers()
	src := s.sourceDir(map[string][]stTensor{"model-00001-of-00002.safetensors": all[:7], "model-00002-of-00002.safetensors": all[7:]}, true)
	dst := filepath.Join(s.dir, "dst")
	s.Require().NoError(UnrollCheckpoint(dst, src, s.plan))

	tok, err := os.ReadFile(filepath.Join(dst, "tokenizer.json"))
	s.Require().NoError(err)
	s.Equal(`{"v": 1}`, string(tok))
	s.Equal(json.Number("10"), s.readJSON(filepath.Join(dst, "config.json"))["num_hidden_layers"])

	merged := map[string]stTensor{}
	for _, shard := range []string{"model-00001-of-00002.safetensors", "model-00002-of-00002.safetensors"} {
		for name, t := range readTestSafetensors(s.T(), filepath.Join(dst, shard)) {
			merged[name] = t
		}
	}
	idx := s.readJSON(filepath.Join(dst, indexFile))
	weightMap := idx["weight_map"].(map[string]any)
	s.Len(weightMap, len(merged))
	var total int
	for name, t := range merged {
		s.Contains(weightMap, name)
		total += len(t.data)
	}
	s.Equal(json.Number(strconv.Itoa(total)), idx["metadata"].(map[string]any)["total_size"])
	for i := range s.plan.Applications() {
		s.Contains(merged, layerPrefix+strconv.Itoa(i)+".self_attn.q_proj.weight")
	}
}

func (s *SafetensorsSuite) TestCheckpointRefusesAMissingLayer() {
	var partial []stTensor
	for _, t := range sixLayers() {
		if l, _, ok := splitIndexed(t.name, layerPrefix); !ok || l != 5 {
			partial = append(partial, t)
		}
	}
	src := s.sourceDir(map[string][]stTensor{"model.safetensors": partial}, false)
	s.ErrorIs(UnrollCheckpoint(filepath.Join(s.dir, "dst"), src, s.plan), ErrMissingLayers)
}

func (s *SafetensorsSuite) TestCheckpointRefusesAnExistingDestination() {
	src := s.sourceDir(map[string][]stTensor{"model.safetensors": sixLayers()}, false)
	s.ErrorIs(UnrollCheckpoint(src, src, s.plan), ErrDestination)
}
