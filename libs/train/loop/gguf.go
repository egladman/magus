package loop

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

var (
	ErrGGUF              = errors.New("loop: malformed GGUF")
	ErrGGMLType          = errors.New("loop: unknown GGML tensor type")
	ErrBlockCount        = errors.New("loop: the GGUF block count disagrees with its blk.N tensors")
	ErrPerLayerMetadata  = errors.New("loop: the GGUF holds a per-layer metadata array")
	ErrUnsupportedFormat = errors.New("loop: GGUF version is not 2 or 3")
)

const (
	ggufMagic            = 0x46554747 // "GGUF" read little-endian
	ggufDefaultAlignment = 32
	ggufBlockPrefix      = "blk."
	ggmlMaxDims          = 4
	ggufMaxString        = 1 << 30
)

// Metadata value types, numbered as the gguf spec numbers them.
const (
	ggufU8 uint32 = iota
	ggufI8
	ggufU16
	ggufI16
	ggufU32
	ggufI32
	ggufF32
	ggufBool
	ggufString
	ggufArray
	ggufU64
	ggufI64
	ggufF64
)

var ggufScalarSize = map[uint32]int64{
	ggufU8: 1, ggufI8: 1, ggufBool: 1, ggufU16: 2, ggufI16: 2,
	ggufU32: 4, ggufI32: 4, ggufF32: 4, ggufU64: 8, ggufI64: 8, ggufF64: 8,
}

// ggmlBlock is a tensor type's block: elems elements stored in bytes bytes. The table
// holds the types whose layout ggml.c's type_traits pins; any other refuses with
// ErrGGMLType rather than guessing a size.
type ggmlBlock struct{ elems, bytes int64 }

var ggmlBlocks = map[uint32]ggmlBlock{
	0: {1, 4}, 1: {1, 2}, 2: {32, 18}, 3: {32, 20}, 6: {32, 22}, 7: {32, 24}, 8: {32, 34}, 9: {32, 36},
	10: {256, 84}, 11: {256, 110}, 12: {256, 144}, 13: {256, 176}, 14: {256, 210}, 15: {256, 292},
	24: {1, 1}, 25: {1, 2}, 26: {1, 4}, 27: {1, 8}, 28: {1, 8}, 30: {1, 2},
}

type ggufKV struct {
	key   string
	vtype uint32
	raw   []byte // the encoded value after its type
}

type ggufTensor struct {
	name   string
	dims   []uint64
	gtype  uint32
	offset uint64
	size   int64
}

type ggufFile struct {
	version    uint32
	kvs        []ggufKV
	tensors    []ggufTensor
	alignment  int64
	blockKey   int // index into kvs
	blockCount int
	dataStart  int64
}

type ggufReader struct {
	r   *bufio.Reader
	n   int64
	tee *bytes.Buffer
}

func (g *ggufReader) Read(p []byte) (int, error) {
	n, err := g.r.Read(p)
	g.n += int64(n)
	if g.tee != nil {
		g.tee.Write(p[:n])
	}
	return n, err
}

func (g *ggufReader) u32() (uint32, error) {
	var v uint32
	return v, binary.Read(g, binary.LittleEndian, &v)
}

func (g *ggufReader) u64() (uint64, error) {
	var v uint64
	return v, binary.Read(g, binary.LittleEndian, &v)
}

func (g *ggufReader) readString() (string, error) {
	n, err := g.u64()
	if err != nil {
		return "", err
	}
	if n > ggufMaxString {
		return "", fmt.Errorf("%w: string of %d bytes", ErrGGUF, n)
	}
	b := make([]byte, n)
	_, err = io.ReadFull(g, b)
	return string(b), err
}

func (g *ggufReader) skip(vtype uint32) error {
	if size, ok := ggufScalarSize[vtype]; ok {
		_, err := io.CopyN(io.Discard, g, size)
		return err
	}
	switch vtype {
	case ggufString:
		_, err := g.readString()
		return err
	case ggufArray:
		elem, err := g.u32()
		if err != nil {
			return err
		}
		count, err := g.u64()
		if err != nil {
			return err
		}
		if size, ok := ggufScalarSize[elem]; ok {
			_, err := io.CopyN(io.Discard, g, size*int64(count))
			return err
		}
		for range count {
			if err := g.skip(elem); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("%w: metadata value type %d", ErrGGUF, vtype)
}

func readGGUF(f *os.File) (ggufFile, error) {
	info, err := f.Stat()
	if err != nil {
		return ggufFile{}, err
	}
	g := &ggufReader{r: bufio.NewReaderSize(f, 1<<20)}
	var gf ggufFile
	magic, err := g.u32()
	if err != nil {
		return ggufFile{}, fmt.Errorf("%w: %w", ErrGGUF, err)
	}
	if magic != ggufMagic {
		return ggufFile{}, fmt.Errorf("%w: magic %#x", ErrGGUF, magic)
	}
	if gf.version, err = g.u32(); err != nil {
		return ggufFile{}, fmt.Errorf("%w: %w", ErrGGUF, err)
	}
	if gf.version != 2 && gf.version != 3 {
		return ggufFile{}, fmt.Errorf("%w: version %d", ErrUnsupportedFormat, gf.version)
	}
	nTensors, err := g.u64()
	if err != nil {
		return ggufFile{}, fmt.Errorf("%w: %w", ErrGGUF, err)
	}
	nKV, err := g.u64()
	if err != nil {
		return ggufFile{}, fmt.Errorf("%w: %w", ErrGGUF, err)
	}
	if nTensors > 1<<24 || nKV > 1<<24 {
		return ggufFile{}, fmt.Errorf("%w: %d tensors, %d keys", ErrGGUF, nTensors, nKV)
	}
	for range nKV {
		kv, err := g.kv()
		if err != nil {
			return ggufFile{}, fmt.Errorf("%w: %w", ErrGGUF, err)
		}
		gf.kvs = append(gf.kvs, kv)
	}
	for range nTensors {
		t, err := g.tensorInfo()
		if err != nil {
			return ggufFile{}, err
		}
		gf.tensors = append(gf.tensors, t)
	}
	if err := gf.settle(g.n, info.Size()); err != nil {
		return ggufFile{}, err
	}
	return gf, nil
}

func (g *ggufReader) kv() (ggufKV, error) {
	key, err := g.readString()
	if err != nil {
		return ggufKV{}, err
	}
	vtype, err := g.u32()
	if err != nil {
		return ggufKV{}, err
	}
	g.tee = &bytes.Buffer{}
	err = g.skip(vtype)
	raw := g.tee.Bytes()
	g.tee = nil
	return ggufKV{key: key, vtype: vtype, raw: raw}, err
}

func (g *ggufReader) tensorInfo() (ggufTensor, error) {
	var t ggufTensor
	var err error
	if t.name, err = g.readString(); err != nil {
		return t, fmt.Errorf("%w: tensor name: %w", ErrGGUF, err)
	}
	nDims, err := g.u32()
	if err != nil || nDims == 0 || nDims > ggmlMaxDims {
		return t, fmt.Errorf("%w: %s: %d dims: %w", ErrGGUF, t.name, nDims, err)
	}
	count := int64(1)
	for range nDims {
		d, err := g.u64()
		if err != nil || d > 1<<40 {
			return t, fmt.Errorf("%w: %s: dim %d: %w", ErrGGUF, t.name, d, err)
		}
		t.dims = append(t.dims, d)
		count *= int64(d)
	}
	if t.gtype, err = g.u32(); err != nil {
		return t, fmt.Errorf("%w: %s: %w", ErrGGUF, t.name, err)
	}
	if t.offset, err = g.u64(); err != nil {
		return t, fmt.Errorf("%w: %s: %w", ErrGGUF, t.name, err)
	}
	block, ok := ggmlBlocks[t.gtype]
	if !ok {
		return t, fmt.Errorf("%w: %d: %s", ErrGGMLType, t.gtype, t.name)
	}
	if int64(t.dims[0])%block.elems != 0 {
		return t, fmt.Errorf("%w: %s: row of %d is not whole blocks of %d", ErrGGUF, t.name, t.dims[0], block.elems)
	}
	t.size = count / block.elems * block.bytes
	return t, nil
}

// settle reads the alignment and block count from the metadata and checks every tensor
// against them and the file's length.
func (gf *ggufFile) settle(headerEnd, fileSize int64) error {
	gf.alignment = ggufDefaultAlignment
	arch := ""
	for _, kv := range gf.kvs {
		switch {
		case kv.key == "general.alignment":
			a, err := kvInt(kv)
			if err != nil || a <= 0 || a%8 != 0 {
				return fmt.Errorf("%w: general.alignment %d: %w", ErrGGUF, a, err)
			}
			gf.alignment = a
		case kv.key == "general.architecture" && kv.vtype == ggufString && len(kv.raw) >= 8:
			arch = string(kv.raw[8:])
		}
	}
	if arch == "" {
		return fmt.Errorf("%w: no general.architecture", ErrGGUF)
	}
	gf.blockKey = -1
	for i, kv := range gf.kvs {
		if kv.key == arch+".block_count" {
			gf.blockKey = i
		}
	}
	if gf.blockKey < 0 {
		return fmt.Errorf("%w: no %s.block_count", ErrGGUF, arch)
	}
	n, err := kvInt(gf.kvs[gf.blockKey])
	if err != nil {
		return fmt.Errorf("%w: %s.block_count: %w", ErrGGUF, arch, err)
	}
	gf.blockCount = int(n)
	for _, kv := range gf.kvs {
		if strings.HasPrefix(kv.key, arch+".") && kv.vtype == ggufArray && len(kv.raw) >= 12 &&
			binary.LittleEndian.Uint64(kv.raw[4:12]) == uint64(gf.blockCount) {
			return fmt.Errorf("%w: %s", ErrPerLayerMetadata, kv.key)
		}
	}
	gf.dataStart = alignUp(headerEnd, gf.alignment)
	blocks := map[int]bool{}
	for _, t := range gf.tensors {
		if int64(t.offset)%gf.alignment != 0 || gf.dataStart+int64(t.offset)+t.size > fileSize {
			return fmt.Errorf("%w: %s: %d bytes at offset %d", ErrGGUF, t.name, t.size, t.offset)
		}
		if b, _, ok := splitIndexed(t.name, ggufBlockPrefix); ok {
			if b >= gf.blockCount {
				return fmt.Errorf("%w: %s with block_count %d", ErrBlockCount, t.name, gf.blockCount)
			}
			blocks[b] = true
		}
	}
	if len(blocks) != gf.blockCount {
		return fmt.Errorf("%w: block_count %d, tensors for %d blocks", ErrBlockCount, gf.blockCount, len(blocks))
	}
	return nil
}

func kvInt(kv ggufKV) (int64, error) {
	r := bytes.NewReader(kv.raw)
	switch kv.vtype {
	case ggufU32:
		var v uint32
		err := binary.Read(r, binary.LittleEndian, &v)
		return int64(v), err
	case ggufI32:
		var v int32
		err := binary.Read(r, binary.LittleEndian, &v)
		return int64(v), err
	case ggufU64:
		var v uint64
		err := binary.Read(r, binary.LittleEndian, &v)
		if v > 1<<62 {
			return 0, fmt.Errorf("%d is out of range", v)
		}
		return int64(v), err
	case ggufI64:
		var v int64
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	}
	return 0, fmt.Errorf("%s has value type %d, want an integer", kv.key, kv.vtype)
}

// withInt re-encodes kv's integer value as n in kv's own type.
func withInt(kv ggufKV, n int) (ggufKV, error) {
	var v any
	switch kv.vtype {
	case ggufU32:
		v = uint32(n)
	case ggufI32:
		v = int32(n)
	case ggufU64:
		v = uint64(n)
	case ggufI64:
		v = int64(n)
	default:
		return kv, fmt.Errorf("%w: %s has value type %d", ErrGGUF, kv.key, kv.vtype)
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
		return kv, err
	}
	return ggufKV{key: kv.key, vtype: kv.vtype, raw: buf.Bytes()}, nil
}

func alignUp(n, a int64) int64 { return (n + a - 1) / a * a }

// GGUFLayers reads <general.architecture>.block_count from a gguf file, after checking
// it against the file's blk.N tensors (ErrBlockCount).
func GGUFLayers(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	gf, err := readGGUF(f)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return gf.blockCount, nil
}

// UnrollGGUF writes src unrolled by plan to dst. blk.N tensors are written once per
// target of N, renumbered, their bytes unchanged. Other tensors and metadata pass
// through, but <arch>.block_count becomes plan.Applications(). It refuses a block count
// other than plan.Layers() (ErrLayers) and a per-layer metadata array
// (ErrPerLayerMetadata). dst is replaced atomically.
func UnrollGGUF(dst, src string, plan Plan) error {
	if err := plan.check(); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	gf, err := readGGUF(in)
	if err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	if gf.blockCount != plan.Layers() {
		return fmt.Errorf("%w: %s has %d blocks, the plan %d", ErrLayers, src, gf.blockCount, plan.Layers())
	}
	if gf.kvs[gf.blockKey], err = withInt(gf.kvs[gf.blockKey], plan.Applications()); err != nil {
		return err
	}
	out, err := unrolledTensors(gf.tensors, plan)
	if err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	return writeAtomic(dst, src, func(f *os.File) error {
		w := bufio.NewWriterSize(f, 1<<20)
		if err := gf.writeHeader(w, out); err != nil {
			return err
		}
		for _, o := range out {
			if _, err := io.Copy(w, io.NewSectionReader(in, gf.dataStart+int64(o.from.offset), o.from.size)); err != nil {
				return fmt.Errorf("%s: %s: %w", src, o.from.name, err)
			}
			if _, err := w.Write(make([]byte, alignUp(o.from.size, gf.alignment)-o.from.size)); err != nil {
				return err
			}
		}
		return w.Flush()
	})
}

type ggufOut struct {
	name string
	from ggufTensor
}

// unrolledTensors orders the output the way llama.cpp's converter writes it: tensors
// before the first block, every block in unrolled order, then the rest.
func unrolledTensors(tensors []ggufTensor, plan Plan) ([]ggufOut, error) {
	byBlock := map[int][]ggufTensor{}
	for _, t := range tensors {
		if b, _, ok := splitIndexed(t.name, ggufBlockPrefix); ok {
			byBlock[b] = append(byBlock[b], t)
		}
	}
	var out []ggufOut
	emitted := false
	for _, t := range tensors {
		if _, _, ok := splitIndexed(t.name, ggufBlockPrefix); !ok {
			out = append(out, ggufOut{name: t.name, from: t})
			continue
		}
		if emitted {
			continue
		}
		emitted = true
		for i := range plan.Applications() {
			s, err := plan.Source(i)
			if err != nil {
				return nil, err
			}
			for _, bt := range byBlock[s] {
				_, suffix, _ := splitIndexed(bt.name, ggufBlockPrefix)
				out = append(out, ggufOut{name: ggufBlockPrefix + strconv.Itoa(i) + "." + suffix, from: bt})
			}
		}
	}
	return out, nil
}

func (gf ggufFile) writeHeader(w io.Writer, out []ggufOut) error {
	var hdr bytes.Buffer
	put := func(v any) { _ = binary.Write(&hdr, binary.LittleEndian, v) }
	putStr := func(s string) {
		put(uint64(len(s)))
		hdr.WriteString(s)
	}
	put(uint32(ggufMagic))
	put(gf.version)
	put(uint64(len(out)))
	put(uint64(len(gf.kvs)))
	for _, kv := range gf.kvs {
		putStr(kv.key)
		put(kv.vtype)
		hdr.Write(kv.raw)
	}
	var offset int64
	for _, o := range out {
		putStr(o.name)
		put(uint32(len(o.from.dims)))
		for _, d := range o.from.dims {
			put(d)
		}
		put(o.from.gtype)
		put(uint64(offset))
		offset += alignUp(o.from.size, gf.alignment)
	}
	hdr.Write(make([]byte, alignUp(int64(hdr.Len()), gf.alignment)-int64(hdr.Len())))
	_, err := w.Write(hdr.Bytes())
	return err
}
