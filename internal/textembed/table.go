package textembed

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sync"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/textrank"
)

// tableFormat is the version EncodeTable stamps and ReadTable requires. Bump it on any
// change to the layout or the header.
const tableFormat = 2

var tableMagic = [4]byte{'M', 'S', 'E', 'T'}

const (
	// maxDims keeps a dot product of two int8 vectors inside int32: 4096·127² < 2^31.
	maxDims   = 4096
	maxHeader = 1 << 16
)

// probe is tokenized into the tokenizer digest. Changing it, or changing what
// textrank.Tokens makes of it, refuses every table encoded before.
//
//go:embed testdata/probe.txt
var probe string

var tokenizerDigest = sync.OnceValue(func() string {
	h := sha256.New()
	for _, tok := range textrank.Tokens(probe) {
		// A token holds only letters, digits and '_', so '\n' cannot occur inside one.
		h.Write([]byte(tok))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
})

// Table is a static embedding table: per token a uint16 weight and an int8 direction,
// over tokens sorted for binary search. It reads the bytes it was decoded from in
// place, and is safe for concurrent use.
type Table struct {
	dims, n int
	prov    Provenance
	digest  [sha256.Size]byte
	offsets []byte // n little-endian uint32s: where token i's length prefix sits in toks
	toks    []byte // n uvarint-length-prefixed tokens
	weights []byte // n little-endian uint16s
	rows    []byte // n·dims int8s, token-major
	// starts[k] is the first token whose prefixKey is at least k, so the tokens with
	// key k are starts[k] to starts[k+1]. ReadTable fills it in the walk that checks
	// the tokens' order.
	//
	// optimization: a 256 KiB prefix index narrows a lookup's binary search from about
	// 15 probes to about 5, and lookups are most of Embed.
	//   measured (29k tokens of 256 dims, Apple M5, n=3): BenchmarkEmbed/doc400
	//   162µs -> 99µs, doc400/terms 103µs -> 41µs, query8 4.3µs -> 3.2µs.
	starts []uint32
}

// Provenance names what produced a table. Every string field is required. Format and
// TokenizerDigest are the codec's own: EncodeTable fills them when empty and refuses a
// value that differs from its own.
type Provenance struct {
	Format                         int
	Teacher, TeacherRevision       string
	TextsDigest, PairsDigest       string
	TokenizerDigest, Pipeline, Run string
	GOARCH                         string
	Seed                           uint64
}

// TableSpec is a table as the trainer hands it to EncodeTable.
type TableSpec struct {
	Tokens  []string // sorted, unique, each yielded first by textrank.Tokens of itself
	Weights []uint16 // w16 per token
	Rows    [][]int8 // q8 per token, all one length, a positive multiple of 64, in [-127, 127]
	Prov    Provenance
}

// tableHeader is the JSON header. Its fields mirror Provenance except Format, which
// the uvarint after the magic carries.
type tableHeader struct {
	Dims            int    `json:"dims"`
	Tokens          int    `json:"tokens"`
	Teacher         string `json:"teacher"`
	TeacherRevision string `json:"teacher_revision"`
	TextsDigest     string `json:"texts_digest"`
	PairsDigest     string `json:"pairs_digest"`
	TokenizerDigest string `json:"tokenizer_digest"`
	Pipeline        string `json:"pipeline"`
	Run             string `json:"run"`
	GOARCH          string `json:"goarch"`
	Seed            uint64 `json:"seed"`
}

func (h tableHeader) provenance() Provenance {
	return Provenance{
		Format:          tableFormat,
		Teacher:         h.Teacher,
		TeacherRevision: h.TeacherRevision,
		TextsDigest:     h.TextsDigest,
		PairsDigest:     h.PairsDigest,
		TokenizerDigest: h.TokenizerDigest,
		Pipeline:        h.Pipeline,
		Run:             h.Run,
		GOARCH:          h.GOARCH,
		Seed:            h.Seed,
	}
}

func (p Provenance) check() error {
	for _, f := range []struct{ name, value string }{
		{"teacher", p.Teacher},
		{"teacher_revision", p.TeacherRevision},
		{"texts_digest", p.TextsDigest},
		{"pairs_digest", p.PairsDigest},
		{"tokenizer_digest", p.TokenizerDigest},
		{"pipeline", p.Pipeline},
		{"run", p.Run},
		{"goarch", p.GOARCH},
	} {
		if f.value == "" {
			return fmt.Errorf("provenance %s is empty", f.name)
		}
	}
	if want := tokenizerDigest(); p.TokenizerDigest != want {
		return fmt.Errorf("tokenizer digest %s, but textrank.Tokens over the probe gives %s: retrain the table under today's tokenizer", p.TokenizerDigest, want)
	}
	return nil
}

func checkDims(dims int) error {
	if dims <= 0 || dims%64 != 0 || dims > maxDims {
		return fmt.Errorf("dims %d is not a positive multiple of 64 up to %d", dims, maxDims)
	}
	return nil
}

// EncodeTable validates spec and writes it as one MSET table: the magic, a uvarint
// format, a uvarint-length JSON header, uint32 token offsets, the uvarint-length
// tokens, w16[V], q8[V][dims], then a sha256 trailer over everything before it.
// Integers are little-endian, so the bytes depend only on spec. An invalid spec writes
// nothing.
func EncodeTable(w io.Writer, spec TableSpec) error {
	hdr, err := spec.header()
	if err != nil {
		return fmt.Errorf("textembed: encode table: %w", err)
	}
	if _, err := w.Write(layoutTable(hdr, spec)); err != nil {
		return fmt.Errorf("textembed: encode table: %w", err)
	}
	return nil
}

func (spec TableSpec) header() ([]byte, error) {
	dims, err := spec.checkVocabulary()
	if err != nil {
		return nil, err
	}
	prov := spec.Prov
	if prov.Format != 0 && prov.Format != tableFormat {
		return nil, fmt.Errorf("provenance names format %d, but this build writes format %d", prov.Format, tableFormat)
	}
	if prov.TokenizerDigest == "" {
		prov.TokenizerDigest = tokenizerDigest()
	}
	if err := prov.check(); err != nil {
		return nil, err
	}
	return json.Marshal(tableHeader{
		Dims:            dims,
		Tokens:          len(spec.Tokens),
		Teacher:         prov.Teacher,
		TeacherRevision: prov.TeacherRevision,
		TextsDigest:     prov.TextsDigest,
		PairsDigest:     prov.PairsDigest,
		TokenizerDigest: prov.TokenizerDigest,
		Pipeline:        prov.Pipeline,
		Run:             prov.Run,
		GOARCH:          prov.GOARCH,
		Seed:            prov.Seed,
	})
}

// checkVocabulary checks that spec holds one weight and one row per token, tokens
// sorted, unique and each what textrank.Tokens makes of it, rows of one length that
// checkDims accepts and in the symmetric int8 range, and token bytes a uint32 offset
// reaches. It returns the row length.
func (spec TableSpec) checkVocabulary() (dims int, err error) {
	n := len(spec.Tokens)
	if n == 0 {
		return 0, errors.New("no tokens")
	}
	if len(spec.Weights) != n || len(spec.Rows) != n {
		return 0, fmt.Errorf("%d tokens, %d weights and %d rows, but each token needs one of each", n, len(spec.Weights), len(spec.Rows))
	}
	dims = len(spec.Rows[0])
	if err := checkDims(dims); err != nil {
		return 0, err
	}
	var size int
	var prefix [binary.MaxVarintLen64]byte
	for i, tok := range spec.Tokens {
		if i > 0 && tok <= spec.Tokens[i-1] {
			return 0, fmt.Errorf("tokens %q and %q are not sorted and unique", spec.Tokens[i-1], tok)
		}
		if got := textrank.Tokens(tok); len(got) == 0 || got[0] != tok {
			return 0, fmt.Errorf("token %q is not what textrank.Tokens makes of it (%q)", tok, got)
		}
		row := spec.Rows[i]
		if len(row) != dims {
			return 0, fmt.Errorf("token %q has %d dims, the first row %d", tok, len(row), dims)
		}
		if slices.Contains(row, math.MinInt8) {
			return 0, fmt.Errorf("token %q holds -128, outside the symmetric int8 range", tok)
		}
		size += binary.PutUvarint(prefix[:], uint64(len(tok))) + len(tok)
	}
	if size > math.MaxUint32 {
		return 0, fmt.Errorf("tokens take %d bytes, more than a uint32 offset reaches", size)
	}
	return dims, nil
}

// layoutTable lays out a table without validating it.
func layoutTable(hdr []byte, spec TableSpec) []byte {
	buf := append([]byte{}, tableMagic[:]...)
	buf = binary.AppendUvarint(buf, tableFormat)
	buf = binary.AppendUvarint(buf, uint64(len(hdr)))
	buf = append(buf, hdr...)
	var toks []byte
	for _, tok := range spec.Tokens {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(toks)))
		toks = binary.AppendUvarint(toks, uint64(len(tok)))
		toks = append(toks, tok...)
	}
	buf = append(buf, toks...)
	for _, w := range spec.Weights {
		buf = binary.LittleEndian.AppendUint16(buf, w)
	}
	for _, row := range spec.Rows {
		for _, q := range row {
			buf = append(buf, byte(q))
		}
	}
	sum := sha256.Sum256(buf)
	return append(buf, sum[:]...)
}

// ReadTable decodes a table EncodeTable wrote, strictly: the magic, the format, the
// sha256 trailer, every provenance field, the tokenizer digest against today's
// textrank.Tokens, the token order and every section's length must all check out, or
// ReadTable returns an error naming the first that does not. The Table reads data in
// place, so the caller must not modify data afterwards.
func ReadTable(data []byte) (*Table, error) {
	t, err := readTable(data)
	if err != nil {
		return nil, fmt.Errorf("textembed: read table: %w", err)
	}
	return t, nil
}

var errTruncated = errors.New("truncated table")

func readTable(data []byte) (*Table, error) {
	if !bytes.HasPrefix(data, tableMagic[:]) {
		return nil, errors.New("not an embedding table (no MSET magic)")
	}
	format, k := binary.Uvarint(data[len(tableMagic):])
	if k <= 0 {
		return nil, errTruncated
	}
	if format != tableFormat {
		return nil, fmt.Errorf("format %d, but this build reads only format %d", format, tableFormat)
	}
	start := len(tableMagic) + k
	if len(data) < start+sha256.Size {
		return nil, errTruncated
	}
	body := data[:len(data)-sha256.Size]
	t := &Table{digest: sha256.Sum256(body)}
	if !bytes.Equal(t.digest[:], data[len(body):]) {
		return nil, errors.New("sha256 trailer does not match the contents: the table is truncated or corrupt")
	}

	r, err := t.readHeader(body[start:])
	if err != nil {
		return nil, err
	}
	if r, err = t.readTokens(r); err != nil {
		return nil, err
	}
	if want := int64(t.n) * int64(2+t.dims); int64(len(r)) != want {
		return nil, fmt.Errorf("%d bytes follow the tokens, but %d tokens of %d dims need %d", len(r), t.n, t.dims, want)
	}
	t.weights, t.rows = r[:2*t.n], r[2*t.n:]
	if i := bytes.IndexByte(t.rows, 0x80); i >= 0 {
		return nil, fmt.Errorf("token %q holds -128, outside the symmetric int8 range", t.token(i/t.dims))
	}
	return t, nil
}

// readHeader decodes the uvarint-length header at the start of r into t's provenance,
// dims and token count, and returns the bytes after it.
func (t *Table) readHeader(r []byte) ([]byte, error) {
	hlen, k := binary.Uvarint(r)
	if k <= 0 || hlen > maxHeader || hlen > uint64(len(r)-k) {
		return nil, errors.New("header length is malformed")
	}
	var h tableHeader
	if err := json.UnmarshalStrict(r[k:k+int(hlen)], &h); err != nil {
		return nil, fmt.Errorf("header: %w", err)
	}
	r = r[k+int(hlen):]
	t.prov = h.provenance()
	if err := t.prov.check(); err != nil {
		return nil, err
	}
	if err := checkDims(h.Dims); err != nil {
		return nil, err
	}
	t.dims, t.n = h.Dims, h.Tokens
	if t.n <= 0 {
		return nil, fmt.Errorf("header names %d tokens", t.n)
	}
	if int64(t.n)*4 > int64(len(r)) {
		return nil, fmt.Errorf("header names %d tokens, but the table holds room for at most %d", t.n, len(r)/4)
	}
	return r, nil
}

// readTokens reads the token offsets and the tokens at the start of r, checks that
// they are sorted and unique, builds the prefix-key index lookup searches, and returns
// the bytes after them.
func (t *Table) readTokens(r []byte) ([]byte, error) {
	t.offsets, r = r[:4*t.n], r[4*t.n:]
	t.starts = make([]uint32, 1<<16+1)
	pos, key := 0, 0
	var prev []byte
	for i := range t.n {
		if off := binary.LittleEndian.Uint32(t.offsets[4*i:]); int64(off) != int64(pos) {
			return nil, fmt.Errorf("token %d sits at %d, its offset says %d", i, pos, off)
		}
		size, k := binary.Uvarint(r[pos:])
		if k <= 0 || size == 0 || size > uint64(len(r)-pos-k) {
			return nil, fmt.Errorf("token %d has a malformed length", i)
		}
		tok := r[pos+k : pos+k+int(size)]
		if i > 0 && bytes.Compare(prev, tok) >= 0 {
			return nil, fmt.Errorf("tokens %q and %q are not sorted and unique", prev, tok)
		}
		for ; key <= prefixKey(tok); key++ {
			t.starts[key] = uint32(i)
		}
		prev = tok
		pos += k + int(size)
	}
	for ; key < len(t.starts); key++ {
		t.starts[key] = uint32(t.n)
	}
	t.toks = r[:pos]
	return r[pos:], nil
}

// Dims is the length of every row and of every Vector the table makes.
func (t *Table) Dims() int { return t.dims }

// Len is the number of tokens.
func (t *Table) Len() int { return t.n }

// Digest is the hex sha256 of the encoded bytes before the trailer, which the trailer
// stores. It names the table beside the weights digest in a ranked result.
func (t *Table) Digest() string { return hex.EncodeToString(t.digest[:]) }

// Provenance is the header the table was encoded with.
func (t *Table) Provenance() Provenance { return t.prov }

func (t *Table) token(i int) []byte {
	off := binary.LittleEndian.Uint32(t.offsets[4*i:])
	size, k := binary.Uvarint(t.toks[off:])
	return t.toks[int(off)+k : int(off)+k+int(size)]
}

func (t *Table) weight(i int) uint16 { return binary.LittleEndian.Uint16(t.weights[2*i:]) }

func (t *Table) row(i int) []byte { return t.rows[i*t.dims : (i+1)*t.dims : (i+1)*t.dims] }

// prefixKey is a token's first two bytes, the second 0 for a one-byte token. Zero sorts
// first, so the keys of sorted tokens never decrease.
func prefixKey[S string | []byte](tok S) int {
	k := int(tok[0]) << 8
	if len(tok) > 1 {
		k |= int(tok[1])
	}
	return k
}

// lookup binary-searches the tokens sharing tok's prefix key. The string conversions
// in its comparisons do not allocate.
func (t *Table) lookup(tok string) (int, bool) {
	if tok == "" {
		return 0, false
	}
	k := prefixKey(tok)
	lo, hi := int(t.starts[k]), int(t.starts[k+1])
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if string(t.token(mid)) < tok {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, lo < int(t.starts[k+1]) && string(t.token(lo)) == tok
}
