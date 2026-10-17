package textembed

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
)

func writeIndex(t testing.TB, ix *Index) []byte {
	t.Helper()
	var buf bytes.Buffer
	n, err := ix.WriteTo(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(buf.Len()) {
		t.Fatalf("WriteTo reported %d bytes, wrote %d", n, buf.Len())
	}
	return buf.Bytes()
}

func readIndexBytes(t testing.TB, data []byte, tbl *Table) *Index {
	t.Helper()
	ix, err := ReadIndex(bytes.NewReader(data), int64(len(data)), tbl)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestIndexRoundTrip(t *testing.T) {
	c, ix := smallDocSet(t)
	data := writeIndex(t, ix)
	back := readIndexBytes(t, data, c.table)
	if back.Len() != ix.Len() || back.Len() != len(c.docs) {
		t.Fatalf("Len = %d, %d; want %d", back.Len(), ix.Len(), len(c.docs))
	}
	if again := writeIndex(t, back); !bytes.Equal(again, data) {
		t.Fatal("a read index writes different bytes")
	}
	for i, q := range c.queries {
		for _, r := range []int{0, 10, ix.Len()} {
			opts := SearchOptions{Depth: 15, Rescore: r}
			if got, want := back.Search(q, opts), ix.Search(q, opts); !slices.Equal(got, want) {
				t.Fatalf("query %d, rescore %d: read index finds %v, built %v", i, r, got, want)
			}
		}
	}
}

func TestIndexNoVector(t *testing.T) {
	c, _ := smallDocSet(t)
	ix := NewIndex(c.table, [][]Field{c.docs[0], {{Tokens: []string{"zzzzzzzzzzzz"}, Weight: 1}}, nil})
	if ix.invNorm[1] != 0 || ix.invNorm[2] != 0 || ix.invNorm[0] == 0 {
		t.Errorf("InvNorms = %v, want only doc 0's set", ix.invNorm)
	}
	if !slices.Equal(ix.code(1), make([]uint64, 2)) {
		t.Errorf("doc 1's code = %v, want zero", ix.code(1))
	}
}

// TestIndexGolden pins the bytes WriteTo writes for the small doc set. Embed and
// WriteTo use no float and little-endian integers, so they hold on amd64 and arm64.
func TestIndexGolden(t *testing.T) {
	_, ix := smallDocSet(t)
	data := writeIndex(t, ix)
	sum := sha256.Sum256(data)
	if got, want := hex.EncodeToString(sum[:]), "62a94011377d578fb3b84dcd317b8ea500750c885071702b6d389e99802b22a6"; got != want {
		t.Errorf("index digest = %q, want %q", got, want)
	}
}

// indexLayout splits a valid encoding into its header, the inverse norms and codes,
// and the rows.
func indexLayout(data []byte, docs, dims int) (hdr, head, rows []byte) {
	_, k := binary.Uvarint(data[4:])
	hlen, k2 := binary.Uvarint(data[4+k:])
	start := 4 + k + k2
	end := len(data) - docs*dims
	return data[start : start+int(hlen)], data[start+int(hlen) : end-sha256.Size], data[end:]
}

// resignIndex encodes its parts with a fresh digest, so the checks past the digest see
// whatever corruption they carry.
func resignIndex(format uint64, hdr, head, rows []byte) []byte {
	out := append([]byte{}, indexMagic[:]...)
	out = binary.AppendUvarint(out, format)
	out = binary.AppendUvarint(out, uint64(len(hdr)))
	out = append(out, hdr...)
	out = append(out, head...)
	sum := sha256.Sum256(out)
	out = append(out, sum[:]...)
	return append(out, rows...)
}

func TestIndexReadRefuses(t *testing.T) {
	c, ix := smallDocSet(t)
	data := writeIndex(t, ix)
	hdr, head, rows := indexLayout(data, ix.Len(), 128)
	var h indexHeader
	if err := json.Unmarshal(hdr, &h); err != nil {
		t.Fatal(err)
	}
	withHeader := func(edit func(*indexHeader)) []byte {
		h := h
		edit(&h)
		b, err := json.Marshal(h)
		if err != nil {
			t.Fatal(err)
		}
		return resignIndex(indexFormat, b, head, rows)
	}
	clone := func(b []byte) []byte { return append([]byte{}, b...) }
	flipped := clone(data)
	flipped[len(data)-len(rows)-sha256.Size-1] ^= 1
	noVector := clone(head)
	// Doc 96 holds only an out-of-vocabulary token; its code starts after every InvNorm.
	noVector[4*ix.Len()+8*2*(noVectorEvery-1)] = 1
	other := newDocSet(499, 1, 1, 128).table
	if other.Digest() == c.table.Digest() {
		t.Fatal("the other table is the same table")
	}

	tests := []struct {
		name string
		data []byte
		tbl  *Table
		want string
	}{
		{"empty", nil, c.table, "no MSEI magic"},
		{"magic", append([]byte("MSET"), data[4:]...), c.table, "no MSEI magic"},
		{"format", resignIndex(2, hdr, head, rows), c.table, "format 2"},
		{"header length", append(indexMagic[:], 1, 0xff, 0xff, 0x7f), c.table, "header length is malformed"},
		{"header json", resignIndex(indexFormat, []byte("{"), head, rows), c.table, "header"},
		{"header field", withHeaderJSON(t, hdr, head, rows), c.table, "header"},
		{"table digest", withHeader(func(h *indexHeader) { h.TableDigest = other.Digest() }), c.table, "rebuild it"},
		{"other table", data, other, "rebuild it"},
		{"dims", withHeader(func(h *indexHeader) { h.Dims = 64 }), c.table, "64 dims"},
		{"negative docs", withHeader(func(h *indexHeader) { h.Docs = -1 }), c.table, "-1 docs"},
		{"too many docs", withHeader(func(h *indexHeader) { h.Docs = 1 << 40 }), c.table, "hold at most"},
		{"one doc more", withHeader(func(h *indexHeader) { h.Docs++ }), c.table, "hold at most"},
		{"one doc fewer", withHeader(func(h *indexHeader) { h.Docs-- }), c.table, "docs of 128 dims take"},
		{"one byte over", append(clone(data), 0), c.table, "docs of 128 dims take"},
		{"digest", flipped, c.table, "sha256 does not match"},
		{"code without a vector", resignIndex(indexFormat, hdr, noVector, rows), c.table, "doc 96 has no vector"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadIndex(bytes.NewReader(tt.data), int64(len(tt.data)), tt.tbl)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ReadIndex error = %v, want one naming %q", err, tt.want)
			}
			if !strings.HasPrefix(err.Error(), "read index: ") {
				t.Errorf("error %q does not say it was reading the index", err)
			}
		})
	}
}

func withHeaderJSON(t *testing.T, hdr, head, rows []byte) []byte {
	t.Helper()
	extra := append(bytes.TrimSuffix(bytes.Clone(hdr), []byte("}")), `,"shards_key":"x"}`...)
	return resignIndex(indexFormat, extra, head, rows)
}

func TestIndexReadRefusesEveryTruncation(t *testing.T) {
	c, _ := smallDocSet(t)
	data := writeIndex(t, NewIndex(c.table, c.docs[:3]))
	for n := range len(data) {
		if _, err := ReadIndex(bytes.NewReader(data[:n]), int64(n), c.table); err == nil {
			t.Fatalf("ReadIndex accepted the first %d of %d bytes", n, len(data))
		}
	}
}

type errReader struct{}

func (errReader) ReadAt([]byte, int64) (int, error) { return 0, errWrite }

func TestIndexReadErrors(t *testing.T) {
	c, ix := smallDocSet(t)
	data := writeIndex(t, ix)
	// A size larger than the reader holds is a file truncated after its size was read.
	if _, err := ReadIndex(bytes.NewReader(data[:100]), int64(len(data)), c.table); err == nil || !strings.Contains(err.Error(), "truncated index") {
		t.Errorf("ReadIndex of a short reader = %v, want truncated", err)
	}
	if _, err := ReadIndex(errReader{}, int64(len(data)), c.table); !errors.Is(err, errWrite) {
		t.Errorf("ReadIndex of a failing reader = %v, want its error", err)
	}
	if _, err := ReadIndex(bytes.NewReader(data), -1, c.table); err == nil {
		t.Error("ReadIndex accepted a negative size")
	}
}

func TestIndexWriteError(t *testing.T) {
	_, ix := smallDocSet(t)
	if _, err := ix.WriteTo(failingWriter{}); !errors.Is(err, errWrite) {
		t.Errorf("WriteTo error = %v, want the writer's", err)
	}
}
