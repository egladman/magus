package loop

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

var (
	ErrHeader        = errors.New("loop: malformed safetensors header")
	ErrDType         = errors.New("loop: unknown safetensors dtype")
	ErrConfig        = errors.New("loop: config.json cannot be unrolled")
	ErrSlidingWindow = errors.New("loop: use_sliding_window is on, and max_window_layers has no unrolled meaning")
	ErrMissingLayers = errors.New("loop: the checkpoint's shards do not hold every layer")
	ErrDestination   = errors.New("loop: destination directory already exists")
)

// layerPrefix starts every per-layer tensor name in Hugging Face and mlx-lm checkpoints of
// a decoder stack (qwen2, llama, mistral).
const layerPrefix = "model.layers."

// stEntry is one tensor in a safetensors header. Offsets are a half-open byte range from
// the start of the data section. A copy of libs/train/embed/teacher's entry, which decodes
// floats where this one copies bytes of any dtype.
type stEntry struct {
	DType   string   `json:"dtype"`
	Shape   []int64  `json:"shape"`
	Offsets [2]int64 `json:"data_offsets"`
}

var stElemSize = map[string]int64{
	"BOOL": 1, "U8": 1, "I8": 1, "F8_E4M3": 1, "F8_E5M2": 1,
	"U16": 2, "I16": 2, "F16": 2, "BF16": 2,
	"U32": 4, "I32": 4, "F32": 4,
	"U64": 8, "I64": 8, "F64": 8,
}

type stHeader struct {
	entries  map[string]stEntry
	metadata json.RawMessage
	base     int64
}

// The format's reference reader caps a header at 100 MB.
const maxSafetensorsHeader = 100 << 20

func readSafetensorsHeader(f *os.File) (stHeader, error) {
	info, err := f.Stat()
	if err != nil {
		return stHeader{}, err
	}
	var n uint64
	if err := binary.Read(f, binary.LittleEndian, &n); err != nil {
		return stHeader{}, fmt.Errorf("%w: %s: length: %w", ErrHeader, f.Name(), err)
	}
	if n > maxSafetensorsHeader || int64(8+n) > info.Size() {
		return stHeader{}, fmt.Errorf("%w: %s: header of %d bytes in a %d-byte file", ErrHeader, f.Name(), n, info.Size())
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(f, raw); err != nil {
		return stHeader{}, fmt.Errorf("%w: %s: %w", ErrHeader, f.Name(), err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return stHeader{}, fmt.Errorf("%w: %s: %w", ErrHeader, f.Name(), err)
	}
	h := stHeader{entries: make(map[string]stEntry, len(fields)), metadata: fields["__metadata__"], base: int64(8 + n)}
	delete(fields, "__metadata__")
	dataLen := info.Size() - h.base
	for name, field := range fields {
		var e stEntry
		if err := json.Unmarshal(field, &e); err != nil {
			return stHeader{}, fmt.Errorf("%w: %s: %s: %w", ErrHeader, f.Name(), name, err)
		}
		if err := e.check(dataLen); err != nil {
			return stHeader{}, fmt.Errorf("%s: %s: %w", f.Name(), name, err)
		}
		h.entries[name] = e
	}
	return h, nil
}

func (e stEntry) check(dataLen int64) error {
	size, ok := stElemSize[e.DType]
	if !ok {
		return fmt.Errorf("%w: %q", ErrDType, e.DType)
	}
	count := int64(1)
	for _, d := range e.Shape {
		count *= d
	}
	begin, end := e.Offsets[0], e.Offsets[1]
	if begin < 0 || end > dataLen || end-begin != count*size {
		return fmt.Errorf("%w: bytes [%d, %d) of %d for %s %v", ErrHeader, begin, end, dataLen, e.DType, e.Shape)
	}
	return nil
}

// unrolledNames lists the names a stored tensor takes once unrolled, and its stored
// layer, or -1 outside the stack. A name outside the stack keeps itself. A layer name
// gets one renumbered name per target.
func unrolledNames(name string, plan Plan) ([]string, int, error) {
	layer, suffix, ok := splitIndexed(name, layerPrefix)
	if !ok {
		return []string{name}, -1, nil
	}
	targets, err := plan.Targets(layer)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", name, err)
	}
	out := make([]string, len(targets))
	for k, t := range targets {
		out[k] = layerPrefix + strconv.Itoa(t) + "." + suffix
	}
	return out, layer, nil
}

// UnrollSafetensors writes src unrolled by plan to dst. Each model.layers.N. tensor is
// written once per target of N, renumbered, its bytes unchanged. Other tensors and
// __metadata__ pass through. It does not check that src holds every layer, since a shard
// holds a few; UnrollCheckpoint does. dst is replaced atomically; dst naming src wraps
// ErrSameFile.
func UnrollSafetensors(dst, src string, plan Plan) error {
	_, _, err := unrollSafetensors(dst, src, plan)
	return err
}

func unrollSafetensors(dst, src string, plan Plan) (layers map[int]bool, size int64, err error) {
	if err := plan.check(); err != nil {
		return nil, 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return nil, 0, err
	}
	defer in.Close()
	h, err := readSafetensorsHeader(in)
	if err != nil {
		return nil, 0, err
	}
	names := slices.SortedFunc(maps.Keys(h.entries), func(a, b string) int {
		return cmp.Compare(h.entries[a].Offsets[0], h.entries[b].Offsets[0])
	})
	out := make(map[string]any, len(h.entries)+1)
	if h.metadata != nil {
		out["__metadata__"] = h.metadata
	}
	var copies []stEntry
	layers = map[int]bool{}
	for _, name := range names {
		e := h.entries[name]
		targets, layer, err := unrolledNames(name, plan)
		if err != nil {
			return nil, 0, fmt.Errorf("%s: %w", src, err)
		}
		if layer >= 0 {
			layers[layer] = true
		}
		n := e.Offsets[1] - e.Offsets[0]
		for _, t := range targets {
			if _, dup := out[t]; dup {
				return nil, 0, fmt.Errorf("%w: %s: two tensors unroll to %s", ErrHeader, src, t)
			}
			out[t] = stEntry{DType: e.DType, Shape: e.Shape, Offsets: [2]int64{size, size + n}}
			copies = append(copies, e)
			size += n
		}
	}
	hdr, err := json.Marshal(out)
	if err != nil {
		return nil, 0, err
	}
	// The reference writer pads the header with spaces to 8 bytes, which keeps every
	// tensor of an aligned dtype aligned in a memory map.
	hdr = append(hdr, bytes.Repeat([]byte(" "), (8-len(hdr)%8)%8)...)
	err = writeAtomic(dst, src, func(f *os.File) error {
		if err := binary.Write(f, binary.LittleEndian, uint64(len(hdr))); err != nil {
			return err
		}
		if _, err := f.Write(hdr); err != nil {
			return err
		}
		for _, e := range copies {
			n := e.Offsets[1] - e.Offsets[0]
			if _, err := io.Copy(f, io.NewSectionReader(in, h.base+e.Offsets[0], n)); err != nil {
				return fmt.Errorf("%s: %w", src, err)
			}
		}
		return nil
	})
	return layers, size, err
}

// ConfigLayers reads num_hidden_layers from a Hugging Face config.json.
func ConfigLayers(path string) (int, error) {
	cfg, err := readConfig(path)
	if err != nil {
		return 0, err
	}
	return configInt(cfg, path, "num_hidden_layers")
}

func readConfig(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrConfig, path, err)
	}
	return cfg, nil
}

func configInt(cfg map[string]any, path, key string) (int, error) {
	num, ok := cfg[key].(json.Number)
	if !ok {
		return 0, fmt.Errorf("%w: %s: %s is %v, want an integer", ErrConfig, path, key, cfg[key])
	}
	n, err := strconv.Atoi(num.String())
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %s: %w", ErrConfig, path, key, err)
	}
	return n, nil
}

// UnrollConfig writes to dst the config.json src with num_hidden_layers raised to
// plan.Applications(). A per-layer layer_types list is unrolled with the layers, and so
// are model.layers.N. keys of an mlx-lm quantization or quantization_config map. It
// refuses a config whose layer count is not plan.Layers() (ErrLayers) and one with
// use_sliding_window on (ErrSlidingWindow). Numbers keep their source text; keys come out
// sorted.
func UnrollConfig(dst, src string, plan Plan) error {
	if err := plan.check(); err != nil {
		return err
	}
	cfg, err := readConfig(src)
	if err != nil {
		return err
	}
	layers, err := configInt(cfg, src, "num_hidden_layers")
	if err != nil {
		return err
	}
	if layers != plan.Layers() {
		return fmt.Errorf("%w: %s has %d layers, the plan %d", ErrLayers, src, layers, plan.Layers())
	}
	if on, _ := cfg["use_sliding_window"].(bool); on {
		return fmt.Errorf("%w: %s", ErrSlidingWindow, src)
	}
	cfg["num_hidden_layers"] = plan.Applications()
	if types, ok := cfg["layer_types"]; ok {
		if cfg["layer_types"], err = unrollList(types, plan); err != nil {
			return fmt.Errorf("%s: layer_types: %w", src, err)
		}
	}
	for _, key := range []string{"quantization", "quantization_config"} {
		q, ok := cfg[key].(map[string]any)
		if !ok {
			continue
		}
		if cfg[key], err = unrollKeys(q, plan); err != nil {
			return fmt.Errorf("%s: %s: %w", src, key, err)
		}
	}
	return writeJSON(dst, src, cfg)
}

func writeJSON(dst, src string, v any) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(dst, src, func(f *os.File) error {
		_, err := f.Write(append(out, '\n'))
		return err
	})
}

func unrollList(v any, plan Plan) ([]any, error) {
	list, ok := v.([]any)
	if !ok || len(list) != plan.Layers() {
		return nil, fmt.Errorf("%w: want a list of %d, got %v", ErrConfig, plan.Layers(), v)
	}
	out := make([]any, plan.Applications())
	for i := range out {
		s, err := plan.Source(i)
		if err != nil {
			return nil, err
		}
		out[i] = list[s]
	}
	return out, nil
}

func unrollKeys[V any](m map[string]V, plan Plan) (map[string]V, error) {
	out := make(map[string]V, len(m))
	for k, v := range m {
		names, _, err := unrolledNames(k, plan)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			out[n] = v
		}
	}
	return out, nil
}

const indexFile = "model.safetensors.index.json"

// UnrollCheckpoint writes srcDir unrolled by plan to dstDir, which must not exist
// (ErrDestination). config.json goes through UnrollConfig, each *.safetensors through
// UnrollSafetensors. The index gets a new weight_map and total_size. Other files, such
// as the tokenizer, are copied. The shards must hold every stored layer
// (ErrMissingLayers). A subdirectory is refused. A failure can leave dstDir partly
// written.
func UnrollCheckpoint(dstDir, srcDir string, plan Plan) error {
	if err := plan.check(); err != nil {
		return err
	}
	if _, err := os.Stat(dstDir); err == nil {
		return fmt.Errorf("%w: %s", ErrDestination, dstDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	seen := map[int]bool{}
	var total int64
	hasIndex := false
	for _, de := range entries {
		name := de.Name()
		src, dst := filepath.Join(srcDir, name), filepath.Join(dstDir, name)
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			return fmt.Errorf("%w: %s is a directory", ErrConfig, src)
		case name == "config.json":
			err = UnrollConfig(dst, src, plan)
		case name == indexFile:
			hasIndex = true
		case strings.HasSuffix(name, ".safetensors"):
			var layers map[int]bool
			var size int64
			layers, size, err = unrollSafetensors(dst, src, plan)
			maps.Copy(seen, layers)
			total += size
		default:
			err = copyFile(dst, src)
		}
		if err != nil {
			return err
		}
	}
	if len(seen) != plan.Layers() {
		return fmt.Errorf("%w: %s holds %d of %d layers", ErrMissingLayers, srcDir, len(seen), plan.Layers())
	}
	if hasIndex {
		return unrollIndex(filepath.Join(dstDir, indexFile), filepath.Join(srcDir, indexFile), plan, total)
	}
	return nil
}

func unrollIndex(dst, src string, plan Plan, total int64) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var idx struct {
		Metadata  map[string]any    `json:"metadata"`
		WeightMap map[string]string `json:"weight_map"`
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrConfig, src, err)
	}
	if idx.WeightMap, err = unrollKeys(idx.WeightMap, plan); err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	if idx.Metadata == nil {
		idx.Metadata = map[string]any{}
	}
	idx.Metadata["total_size"] = total
	return writeJSON(dst, src, idx)
}

func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeAtomic(dst, src, func(f *os.File) error {
		_, err := io.Copy(f, in)
		return err
	})
}
