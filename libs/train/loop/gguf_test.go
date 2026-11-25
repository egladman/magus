package loop

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/suite"
)

type testKV struct {
	key   string
	vtype uint32
	value any
}

type testTensor struct {
	name  string
	dims  []uint64
	gtype uint32
	data  []byte
}

// ggufBuilder encodes a gguf v3 file without the package's writer, so a writer bug
// cannot hide a reader bug.
type ggufBuilder struct{ buf bytes.Buffer }

func (b *ggufBuilder) put(v any) { _ = binary.Write(&b.buf, binary.LittleEndian, v) }

func (b *ggufBuilder) writeString(s string) {
	b.put(uint64(len(s)))
	b.buf.WriteString(s)
}

func (b *ggufBuilder) value(vtype uint32, v any) {
	switch vtype {
	case ggufString:
		b.writeString(v.(string))
	case ggufArray:
		strs := v.([]string)
		b.put(ggufString)
		b.put(uint64(len(strs)))
		for _, s := range strs {
			b.writeString(s)
		}
	default:
		b.put(v)
	}
}

func buildGGUF(kvs []testKV, tensors []testTensor) []byte {
	b := &ggufBuilder{}
	b.put(uint32(ggufMagic))
	b.put(uint32(3))
	b.put(uint64(len(tensors)))
	b.put(uint64(len(kvs)))
	for _, kv := range kvs {
		b.writeString(kv.key)
		b.put(kv.vtype)
		b.value(kv.vtype, kv.value)
	}
	var offset uint64
	for _, t := range tensors {
		b.writeString(t.name)
		b.put(uint32(len(t.dims)))
		for _, d := range t.dims {
			b.put(d)
		}
		b.put(t.gtype)
		b.put(offset)
		offset += uint64(alignUp(int64(len(t.data)), ggufDefaultAlignment))
	}
	pad := func() {
		b.buf.Write(make([]byte, alignUp(int64(b.buf.Len()), ggufDefaultAlignment)-int64(b.buf.Len())))
	}
	pad()
	for _, t := range tensors {
		b.buf.Write(t.data)
		pad()
	}
	return b.buf.Bytes()
}

type GGUFSuite struct {
	suite.Suite
	dir  string
	plan Plan
}

func TestLoopGGUF(t *testing.T) { suite.Run(t, new(GGUFSuite)) }

func (s *GGUFSuite) SetupTest() {
	s.dir = s.T().TempDir()
	var err error
	s.plan, err = NewPlan(6, 2, 4, 3)
	s.Require().NoError(err)
}

func fill(seed, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(seed*37 + i*3)
	}
	return b
}

func qwenKVs(blocks uint32) []testKV {
	return []testKV{
		{"general.architecture", ggufString, "qwen2"},
		{"qwen2.block_count", ggufU32, blocks},
		{"qwen2.embedding_length", ggufU32, uint32(4)},
		{"qwen2.attention.layer_norm_rms_epsilon", ggufF32, float32(1e-6)},
		{"tokenizer.ggml.tokens", ggufArray, []string{"a", "bb", "ccc"}},
	}
}

// sixBlocks is a 6-block model. Its q8_0 tensor of 68 bytes is not a multiple of the
// alignment, so padding is exercised.
func sixBlocks() []testTensor {
	tensors := []testTensor{{name: "token_embd.weight", dims: []uint64{4, 3}, gtype: 0, data: fill(90, 48)}}
	for l := range 6 {
		p := ggufBlockPrefix + strconv.Itoa(l) + "."
		tensors = append(tensors,
			testTensor{name: p + "attn_q.weight", dims: []uint64{4, 4}, gtype: 0, data: fill(l, 64)},
			testTensor{name: p + "ffn_down.weight", dims: []uint64{32, 2}, gtype: 8, data: fill(l+40, 68)})
	}
	return append(tensors, testTensor{name: "output_norm.weight", dims: []uint64{4}, gtype: 0, data: fill(91, 16)})
}

func (s *GGUFSuite) write(name string, kvs []testKV, tensors []testTensor) string {
	path := filepath.Join(s.dir, name)
	s.Require().NoError(os.WriteFile(path, buildGGUF(kvs, tensors), 0o644))
	return path
}

func (s *GGUFSuite) read(path string) (ggufFile, map[string][]byte) {
	f, err := os.Open(path)
	s.Require().NoError(err)
	defer f.Close()
	gf, err := readGGUF(f)
	s.Require().NoError(err)
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	data := map[string][]byte{}
	for _, t := range gf.tensors {
		start := gf.dataStart + int64(t.offset)
		data[t.name] = raw[start : start+t.size]
	}
	return gf, data
}

func (s *GGUFSuite) TestUnrollRaisesTheBlockCountAndCopiesEveryBlock() {
	src := s.write("src.gguf", qwenKVs(6), sixBlocks())
	dst := filepath.Join(s.dir, "dst.gguf")
	s.Require().NoError(UnrollGGUF(dst, src, s.plan))
	n, err := GGUFLayers(dst)
	s.Require().NoError(err)
	s.Equal(10, n)

	inF, in := s.read(src)
	outF, out := s.read(dst)
	s.Len(out, 2+10*2)
	for i := range s.plan.Applications() {
		from, err := s.plan.Source(i)
		s.Require().NoError(err)
		for _, suffix := range []string{"attn_q.weight", "ffn_down.weight"} {
			got, ok := out[ggufBlockPrefix+strconv.Itoa(i)+"."+suffix]
			s.Require().True(ok, "block %d %s", i, suffix)
			s.Equal(in[ggufBlockPrefix+strconv.Itoa(from)+"."+suffix], got, "block %d copies block %d", i, from)
		}
	}
	for _, name := range []string{"token_embd.weight", "output_norm.weight"} {
		s.Equal(in[name], out[name], name)
	}
	s.Equal("token_embd.weight", outF.tensors[0].name)
	s.Equal("output_norm.weight", outF.tensors[len(outF.tensors)-1].name)

	s.Require().Len(outF.kvs, len(inF.kvs))
	for i, kv := range outF.kvs {
		s.Equal(inF.kvs[i].key, kv.key)
		s.Equal(inF.kvs[i].vtype, kv.vtype)
		if kv.key != "qwen2.block_count" {
			s.Equal(inF.kvs[i].raw, kv.raw, kv.key)
		}
	}
}

func (s *GGUFSuite) TestShapesAndTypesFollowTheSource() {
	src := s.write("src.gguf", qwenKVs(6), sixBlocks())
	dst := filepath.Join(s.dir, "dst.gguf")
	s.Require().NoError(UnrollGGUF(dst, src, s.plan))
	inF, _ := s.read(src)
	outF, _ := s.read(dst)
	byName := map[string]ggufTensor{}
	for _, t := range inF.tensors {
		byName[t.name] = t
	}
	for _, t := range outF.tensors {
		name := t.name
		if b, suffix, ok := splitIndexed(t.name, ggufBlockPrefix); ok {
			from, err := s.plan.Source(b)
			s.Require().NoError(err)
			name = ggufBlockPrefix + strconv.Itoa(from) + "." + suffix
		}
		s.Equal(byName[name].dims, t.dims, t.name)
		s.Equal(byName[name].gtype, t.gtype, t.name)
	}
}

func (s *GGUFSuite) TestRefusesABlockCountThatDisagreesWithItsTensors() {
	src := s.write("src.gguf", qwenKVs(7), sixBlocks())
	_, err := GGUFLayers(src)
	s.ErrorIs(err, ErrBlockCount)
	s.ErrorIs(UnrollGGUF(filepath.Join(s.dir, "dst.gguf"), src, s.plan), ErrBlockCount)
}

func (s *GGUFSuite) TestRefusesAPlanForAnotherDepth() {
	src := s.write("src.gguf", qwenKVs(6), sixBlocks())
	other, err := NewPlan(8, 2, 4, 2)
	s.Require().NoError(err)
	s.ErrorIs(UnrollGGUF(filepath.Join(s.dir, "dst.gguf"), src, other), ErrLayers)
}

func (s *GGUFSuite) TestRefusesAPerLayerMetadataArray() {
	kvs := append(qwenKVs(6), testKV{"qwen2.attention.head_count", ggufArray, []string{"1", "2", "3", "4", "5", "6"}})
	src := s.write("src.gguf", kvs, sixBlocks())
	s.ErrorIs(UnrollGGUF(filepath.Join(s.dir, "dst.gguf"), src, s.plan), ErrPerLayerMetadata)
}

func (s *GGUFSuite) TestRefusesAnUnknownTensorType() {
	tensors := append(sixBlocks(), testTensor{name: "odd.weight", dims: []uint64{256}, gtype: 99, data: make([]byte, 32)})
	src := s.write("src.gguf", qwenKVs(6), tensors)
	s.ErrorIs(UnrollGGUF(filepath.Join(s.dir, "dst.gguf"), src, s.plan), ErrGGMLType)
}
