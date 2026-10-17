package textembed

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/egladman/magus/internal/json"
)

// indexFormat is the version WriteTo stamps and ReadIndex requires. Bump it on any
// change to the layout or the header.
const indexFormat = 1

var indexMagic = [4]byte{'M', 'S', 'E', 'I'}

// Index holds one Vector per document, by document ordinal: the inverse norms and sign
// codes in memory, and the int8 rows behind an io.ReaderAt that Search reads only for
// the documents it rescores. It is safe for concurrent use when its rows' ReaderAt is,
// as an *os.File is.
type Index struct {
	dims, words, n int
	table          string
	invNorm        []uint32 // per doc; 0 for a doc with no Vector
	codes          []uint64 // n·words, doc-major
	rows           io.ReaderAt
}

type indexHeader struct {
	TableDigest string `json:"table_digest"`
	Docs        int    `json:"docs"`
	Dims        int    `json:"dims"`
}

// NewIndex embeds docs[i] with t as document i and holds every row in memory. A doc
// t.Embed reports no Vector for stays in the index with InvNorm 0, and Search never
// returns it. A Field Embed refuses panics, as it does in Embed.
func NewIndex(t *Table, docs [][]Field) *Index {
	ix := &Index{
		dims:    t.dims,
		words:   t.dims / 64,
		n:       len(docs),
		table:   t.Digest(),
		invNorm: make([]uint32, len(docs)),
		codes:   make([]uint64, len(docs)*(t.dims/64)),
	}
	rows := make([]byte, len(docs)*t.dims)
	for i, fields := range docs {
		v, ok := t.Embed(fields)
		if !ok {
			continue
		}
		ix.invNorm[i] = v.InvNorm
		copy(ix.codes[i*ix.words:], v.Code)
		for j, x := range v.Q {
			rows[i*ix.dims+j] = byte(x)
		}
	}
	ix.rows = bytes.NewReader(rows)
	return ix
}

// Len is the number of documents, including those with no Vector.
func (ix *Index) Len() int { return ix.n }

// WriteTo writes the index as one MSEI file: the magic, a uvarint format, a
// uvarint-length JSON header {table_digest, docs, dims}, InvNorm[N] as little-endian
// uint32s, Code[N][dims/64] as little-endian uint64s, a sha256 of everything so far,
// then the rows, N·dims int8s. The rows sit outside the digest so ReadIndex need not
// read them. The bytes depend only on the table and the documents.
func (ix *Index) WriteTo(w io.Writer) (int64, error) {
	head, err := ix.head()
	if err != nil {
		return 0, fmt.Errorf("write index: %w", err)
	}
	sum := sha256.Sum256(head)
	head = append(head, sum[:]...)
	n, err := w.Write(head)
	if err != nil {
		return int64(n), fmt.Errorf("write index: %w", err)
	}
	size := int64(ix.n) * int64(ix.dims)
	m, err := io.Copy(w, io.NewSectionReader(ix.rows, 0, size))
	total := int64(n) + m
	if err != nil {
		return total, fmt.Errorf("write index: %w", err)
	}
	if m != size {
		return total, fmt.Errorf("write index: copied %d bytes of rows, want %d", m, size)
	}
	return total, nil
}

func (ix *Index) head() ([]byte, error) {
	hdr, err := json.Marshal(indexHeader{TableDigest: ix.table, Docs: ix.n, Dims: ix.dims})
	if err != nil {
		return nil, err
	}
	buf := append([]byte{}, indexMagic[:]...)
	buf = binary.AppendUvarint(buf, indexFormat)
	buf = binary.AppendUvarint(buf, uint64(len(hdr)))
	buf = append(buf, hdr...)
	for _, x := range ix.invNorm {
		buf = binary.LittleEndian.AppendUint32(buf, x)
	}
	for _, c := range ix.codes {
		buf = binary.LittleEndian.AppendUint64(buf, c)
	}
	return buf, nil
}

// ReadIndex decodes an index WriteTo wrote, from size bytes of r, strictly. The magic,
// the format and the header must check out. So must a table digest equal to t.Digest(),
// dims equal to t.Dims(), a size exactly what the header implies, the sha256 over
// everything before the rows, and no doc with InvNorm 0 but a sign code. ReadIndex
// returns an error naming the first that does not. It reads the inverse norms and codes into
// memory and keeps r for the rows, which it does not read or verify; the caller must
// keep r open and unchanged while the Index is in use.
func ReadIndex(r io.ReaderAt, size int64, t *Table) (*Index, error) {
	ix, err := readIndex(r, size, t)
	if err != nil {
		return nil, fmt.Errorf("read index: %w", err)
	}
	return ix, nil
}

var errTruncatedIndex = errors.New("truncated index")

func readIndex(r io.ReaderAt, size int64, t *Table) (*Index, error) {
	h, headLen, err := readIndexHeader(r, size, t)
	if err != nil {
		return nil, err
	}
	ix := &Index{dims: t.dims, words: t.dims / 64, n: h.Docs, table: h.TableDigest}
	perDoc := int64(4 + 8*ix.words + ix.dims)
	room := size - headLen - sha256.Size
	if room < 0 || int64(ix.n) > room/perDoc {
		return nil, fmt.Errorf("header names %d docs, but %d bytes hold at most %d", ix.n, size, max(room, 0)/perDoc)
	}
	if want := headLen + sha256.Size + int64(ix.n)*perDoc; size != want {
		return nil, fmt.Errorf("%d bytes, but %d docs of %d dims take %d", size, ix.n, ix.dims, want)
	}
	rowsAt, err := ix.readCodes(r, headLen)
	if err != nil {
		return nil, err
	}
	ix.rows = io.NewSectionReader(r, rowsAt, int64(ix.n)*int64(ix.dims))
	return ix, nil
}

// readIndexHeader reads the magic, the format and the header, checks the header
// against t, and returns it with the offset where the inverse norms start.
func readIndexHeader(r io.ReaderAt, size int64, t *Table) (h indexHeader, headLen int64, err error) {
	if size < 0 {
		return h, 0, fmt.Errorf("size %d is negative", size)
	}
	pre := make([]byte, min(size, int64(len(indexMagic)+2*binary.MaxVarintLen64)))
	if err := readAt(r, pre, 0); err != nil {
		return h, 0, err
	}
	if !bytes.HasPrefix(pre, indexMagic[:]) {
		return h, 0, errors.New("not an embedding index (no MSEI magic)")
	}
	format, k := binary.Uvarint(pre[len(indexMagic):])
	if k <= 0 {
		return h, 0, errTruncatedIndex
	}
	if format != indexFormat {
		return h, 0, fmt.Errorf("format %d, but this build reads only format %d", format, indexFormat)
	}
	hlen, k2 := binary.Uvarint(pre[len(indexMagic)+k:])
	if k2 <= 0 || hlen > maxHeader {
		return h, 0, errors.New("header length is malformed")
	}
	start := int64(len(indexMagic) + k + k2)
	if start+int64(hlen) > size {
		return h, 0, errTruncatedIndex
	}
	hdr := make([]byte, hlen)
	if err := readAt(r, hdr, start); err != nil {
		return h, 0, err
	}
	if err := json.UnmarshalStrict(hdr, &h); err != nil {
		return h, 0, fmt.Errorf("header: %w", err)
	}
	if want := t.Digest(); h.TableDigest != want {
		return h, 0, fmt.Errorf("index was embedded with table %s, not %s: rebuild it", h.TableDigest, want)
	}
	if h.Dims != t.dims {
		return h, 0, fmt.Errorf("header names %d dims, the table %d", h.Dims, t.dims)
	}
	if h.Docs < 0 {
		return h, 0, fmt.Errorf("header names %d docs", h.Docs)
	}
	return h, start + int64(hlen), nil
}

// readCodes reads everything before the rows, checks its sha256, and decodes the
// inverse norms and sign codes that start at headLen. It returns where the rows start.
func (ix *Index) readCodes(r io.ReaderAt, headLen int64) (int64, error) {
	head := make([]byte, headLen+int64(ix.n)*int64(4+8*ix.words)+sha256.Size)
	if err := readAt(r, head, 0); err != nil {
		return 0, err
	}
	body := head[:len(head)-sha256.Size]
	if sum := sha256.Sum256(body); !bytes.Equal(sum[:], head[len(body):]) {
		return 0, errors.New("sha256 does not match the head: the index is corrupt")
	}
	b := body[headLen:]
	ix.invNorm = make([]uint32, ix.n)
	for i := range ix.invNorm {
		ix.invNorm[i] = binary.LittleEndian.Uint32(b[4*i:])
	}
	b = b[4*ix.n:]
	ix.codes = make([]uint64, ix.n*ix.words)
	for i := range ix.codes {
		ix.codes[i] = binary.LittleEndian.Uint64(b[8*i:])
	}
	for i, x := range ix.invNorm {
		if x != 0 {
			continue
		}
		for _, c := range ix.code(i) {
			if c != 0 {
				return 0, fmt.Errorf("doc %d has no vector (InvNorm 0) but a sign code", i)
			}
		}
	}
	return int64(len(head)), nil
}

// readAt fills p from r at off, calling a short read with no error, or with io.EOF,
// truncation.
func readAt(r io.ReaderAt, p []byte, off int64) error {
	n, err := r.ReadAt(p, off)
	switch {
	case n == len(p):
		return nil
	case err == nil, errors.Is(err, io.EOF):
		return errTruncatedIndex
	default:
		return err
	}
}

func (ix *Index) code(doc int) []uint64 {
	return ix.codes[doc*ix.words : (doc+1)*ix.words : (doc+1)*ix.words]
}
