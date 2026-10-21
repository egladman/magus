package embed

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func testProvenance() Provenance {
	return Provenance{
		Teacher:         "nomic-ai/CodeRankEmbed",
		TeacherRevision: "rev1",
		TextsDigest:     "texts1",
		PairsDigest:     "pairs1",
		Pipeline:        "f1980cc5c",
		Run:             "run1",
		GOARCH:          "arm64",
		Seed:            7,
	}
}

// testSpec derives each row and weight from sha256 of its token, so the table is the
// same on every machine and every Go release.
func testSpec(tokens []string, dims int) TableSpec {
	spec := TableSpec{Tokens: tokens, Prov: testProvenance()}
	for _, tok := range tokens {
		row := make([]int8, 0, dims)
		for block := 0; len(row) < dims; block++ {
			sum := sha256.Sum256([]byte(tok + ":" + strconv.Itoa(block)))
			for _, b := range sum {
				row = append(row, max(int8(b), -127))
			}
		}
		w := sha256.Sum256([]byte(tok + ":weight"))
		spec.Weights = append(spec.Weights, binary.LittleEndian.Uint16(w[:]))
		spec.Rows = append(spec.Rows, row[:dims])
	}
	return spec
}

// probeVocab is every distinct token Tokens makes of the probe that EncodeTable
// accepts, sorted.
func probeVocab() []string {
	var out []string
	for _, tok := range Tokens(probe) {
		if got := Tokens(tok); got[0] == tok {
			out = append(out, tok)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func encode(t testing.TB, spec TableSpec) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := EncodeTable(&buf, spec); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func read(t testing.TB, data []byte) *Table {
	t.Helper()
	tbl, err := ReadTable(data)
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func TestTableRoundTrip(t *testing.T) {
	spec := testSpec([]string{"alpha", "beta", "foo_bar", "gamma", "日本語のテキスト"}, 128)
	data := encode(t, spec)
	tbl := read(t, data)

	if tbl.Len() != 5 || tbl.Dims() != 128 {
		t.Fatalf("Len, Dims = %d, %d; want 5, 128", tbl.Len(), tbl.Dims())
	}
	sum := sha256.Sum256(data[:len(data)-sha256.Size])
	if got, want := tbl.Digest(), hex.EncodeToString(sum[:]); got != want {
		t.Errorf("Digest = %s, want %s", got, want)
	}
	want := spec.Prov
	want.Format, want.TokenizerDigest = tableFormat, tokenizerDigest()
	if got := tbl.Provenance(); got != want {
		t.Errorf("Provenance = %+v, want %+v", got, want)
	}
	for i, tok := range spec.Tokens {
		if got := string(tbl.token(i)); got != tok {
			t.Errorf("token(%d) = %q, want %q", i, got, tok)
		}
		if got := tbl.weight(i); got != spec.Weights[i] {
			t.Errorf("weight(%d) = %d, want %d", i, got, spec.Weights[i])
		}
		for j, b := range tbl.row(i) {
			if int8(b) != spec.Rows[i][j] {
				t.Fatalf("row(%d)[%d] = %d, want %d", i, j, int8(b), spec.Rows[i][j])
			}
		}
		if got, ok := tbl.lookup(tok); !ok || got != i {
			t.Errorf("lookup(%q) = %d, %v; want %d, true", tok, got, ok, i)
		}
	}
	for _, absent := range []string{"", "a", "alphaa", "betz", "zzz", "foo"} {
		if i, ok := tbl.lookup(absent); ok {
			t.Errorf("lookup(%q) = %d, true; want absent", absent, i)
		}
	}
}

// TestTableGolden pins the encoding byte for byte: the same spec must encode to the
// same digest on every architecture, and a layout change must bump tableFormat.
func TestTableGolden(t *testing.T) {
	data := encode(t, testSpec(probeVocab(), 64))
	const want = "bf02e8edb121f77d890e4ea3a9c2af88ae049764b378f12795c36cb1192752f6"
	if got := read(t, data).Digest(); got != want {
		t.Errorf("probe table digest = %s, want %s", got, want)
	}
	if !bytes.Equal(encode(t, testSpec(probeVocab(), 64)), data) {
		t.Error("encoding the same spec twice gave different bytes")
	}
}

// tokenizerWithoutErrorOperator is the digest of the tokenizer that dropped "!>".
const tokenizerWithoutErrorOperator = "1f021cc9256fabab0fb891a53270047faa9ea49231b3a982e62377d94ee86ad7"

// TestTableTokenizerDigest fails when Tokens or a probe changes. Either
// refuses every table encoded before, so the fix is to retrain the table, then update
// want.
func TestTableTokenizerDigest(t *testing.T) {
	const want = "d62f3a9e2c1ad064ff7878c9f54c14fad7fead0dde89e20fe197b188c72fae8e"
	if got := tokenizerDigest(); got != want {
		t.Errorf("tokenizer digest = %s, want %s", got, want)
	}
}

func TestTableEncodeRefuses(t *testing.T) {
	valid := func() TableSpec { return testSpec([]string{"alpha", "beta"}, 64) }
	tests := []struct {
		name   string
		mutate func(*TableSpec)
		want   string
	}{
		{"no tokens", func(s *TableSpec) { *s = TableSpec{Prov: s.Prov} }, "no tokens"},
		{"missing weight", func(s *TableSpec) { s.Weights = s.Weights[:1] }, "2 tokens, 1 weights and 2 rows"},
		{"missing row", func(s *TableSpec) { s.Rows = s.Rows[:1] }, "2 tokens, 2 weights and 1 rows"},
		{"unsorted", func(s *TableSpec) { s.Tokens = []string{"beta", "alpha"} }, `tokens "beta" and "alpha" are not sorted`},
		{"duplicate", func(s *TableSpec) { s.Tokens = []string{"alpha", "alpha"} }, `tokens "alpha" and "alpha" are not sorted`},
		{"empty token", func(s *TableSpec) { s.Tokens = []string{"", "alpha"} }, `token "" is not what embed.Tokens`},
		{"uppercase", func(s *TableSpec) { s.Tokens = []string{"Alpha", "beta"} }, `token "Alpha" is not what embed.Tokens`},
		{"two words", func(s *TableSpec) { s.Tokens = []string{"a b", "beta"} }, `token "a b" is not what`},
		{"kebab", func(s *TableSpec) { s.Tokens = []string{"alpha", "foo-bar"} }, `token "foo-bar" is not what`},
		{"dims 0", func(s *TableSpec) { s.Rows = [][]int8{{}, {}} }, "dims 0 is not a positive multiple of 64"},
		{"dims 65", func(s *TableSpec) { s.Rows = [][]int8{make([]int8, 65), make([]int8, 65)} }, "dims 65"},
		{"dims over max", func(s *TableSpec) {
			s.Rows = [][]int8{make([]int8, maxDims+64), make([]int8, maxDims+64)}
		}, "dims 4160"},
		{"ragged", func(s *TableSpec) { s.Rows[1] = s.Rows[1][:32] }, `token "beta" has 32 dims, the first row 64`},
		{"minus 128", func(s *TableSpec) { s.Rows[1][9] = -128 }, `token "beta" holds -128`},
		{"no teacher", func(s *TableSpec) { s.Prov.Teacher = "" }, "provenance teacher is empty"},
		{"no run", func(s *TableSpec) { s.Prov.Run = "" }, "provenance run is empty"},
		{"no goarch", func(s *TableSpec) { s.Prov.GOARCH = "" }, "provenance goarch is empty"},
		{"other format", func(s *TableSpec) { s.Prov.Format = 3 }, "provenance names format 3, but this build writes format 2"},
		{"other tokenizer", func(s *TableSpec) { s.Prov.TokenizerDigest = "feed" }, "tokenizer digest feed, but embed.Tokens"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := valid()
			tt.mutate(&spec)
			var buf bytes.Buffer
			err := EncodeTable(&buf, spec)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("EncodeTable error = %v, want one containing %q", err, tt.want)
			}
			if buf.Len() != 0 {
				t.Errorf("EncodeTable wrote %d bytes for an invalid spec", buf.Len())
			}
		})
	}
}

func TestTableEncodeAcceptsItsOwnStamps(t *testing.T) {
	spec := testSpec([]string{"alpha"}, 64)
	plain := encode(t, spec)
	spec.Prov.Format, spec.Prov.TokenizerDigest = tableFormat, tokenizerDigest()
	if !bytes.Equal(encode(t, spec), plain) {
		t.Error("stating the codec's own format and tokenizer digest changed the bytes")
	}
}

func TestTableEncodeWriteError(t *testing.T) {
	err := EncodeTable(failingWriter{}, testSpec([]string{"alpha"}, 64))
	if !errors.Is(err, errWrite) {
		t.Errorf("EncodeTable error = %v, want %v", err, errWrite)
	}
}

var errWrite = errors.New("disk full")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

// layout splits a valid encoding into its header and the sections between the header
// and the trailer.
func layout(data []byte) (hdr, rest []byte) {
	_, k := binary.Uvarint(data[4:])
	hlen, k2 := binary.Uvarint(data[4+k:])
	start := 4 + k + k2
	return data[start : start+int(hlen)], data[start+int(hlen) : len(data)-sha256.Size]
}

// resign encodes hdr and rest with a fresh trailer, so the checks past the digest see
// whatever corruption they carry.
func resign(hdr, rest []byte) []byte {
	out := append([]byte{}, tableMagic[:]...)
	out = binary.AppendUvarint(out, tableFormat)
	out = binary.AppendUvarint(out, uint64(len(hdr)))
	out = append(out, hdr...)
	out = append(out, rest...)
	sum := sha256.Sum256(out)
	return append(out, sum[:]...)
}

func TestTableReadRefuses(t *testing.T) {
	data := encode(t, testSpec([]string{"alpha", "beta", "gamma"}, 64))
	hdr, rest := layout(data)
	var h tableHeader
	if err := unmarshalJSON(hdr, &h); err != nil {
		t.Fatal(err)
	}
	withHeader := func(edit func(*tableHeader)) []byte {
		h := h
		edit(&h)
		b, err := marshalJSON(h)
		if err != nil {
			t.Fatal(err)
		}
		return resign(b, rest)
	}
	withRest := func(edit func([]byte) []byte) []byte {
		return resign(hdr, edit(slices.Clone(rest)))
	}
	offsets := 4 * 3
	tokEnd := offsets + len("\x05alpha\x04beta\x05gamma")

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "no MSET magic"},
		{"other magic", append([]byte("MTRX"), data[4:]...), "no MSET magic"},
		{"magic only", data[:4], "truncated table"},
		{"other format", append(append([]byte("MSET"), 3), data[5:]...), "format 3, but this build reads only format 2"},
		{"no trailer", data[:len(data)-sha256.Size], "sha256 trailer does not match"},
		{"flipped row byte", func() []byte { d := slices.Clone(data); d[len(d)-40] ^= 1; return d }(), "sha256 trailer does not match"},
		{"flipped trailer", func() []byte { d := slices.Clone(data); d[len(d)-1] ^= 1; return d }(), "sha256 trailer does not match"},
		{"header unknown field", resign(append(slices.Clone(hdr[:len(hdr)-1]), `,"extra":1}`...), rest), "header"},
		{"header not json", resign([]byte("{"), rest), "header"},
		{"header no teacher", withHeader(func(h *tableHeader) { h.Teacher = "" }), "provenance teacher is empty"},
		{"header no pairs", withHeader(func(h *tableHeader) { h.PairsDigest = "" }), "provenance pairs_digest is empty"},
		{"other tokenizer", withHeader(func(h *tableHeader) { h.TokenizerDigest = "feed" }), "tokenizer digest feed, but embed.Tokens over the probe gives"},
		{"tokenizer before the error operator", withHeader(func(h *tableHeader) { h.TokenizerDigest = tokenizerWithoutErrorOperator }),
			"tokenizer digest " + tokenizerWithoutErrorOperator + ", but embed.Tokens over the probe gives " + tokenizerDigest() + ": retrain the table under today's tokenizer"},
		{"dims 65", withHeader(func(h *tableHeader) { h.Dims = 65 }), "dims 65"},
		{"zero tokens", withHeader(func(h *tableHeader) { h.Tokens = 0 }), "header names 0 tokens"},
		{"too many tokens", withHeader(func(h *tableHeader) { h.Tokens = 1 << 30 }), "header names 1073741824 tokens"},
		{"fewer tokens", withHeader(func(h *tableHeader) { h.Tokens = 2 }), "token 1 sits at 12, its offset says 6"},
		{"bad offset", withRest(func(r []byte) []byte { r[4]++; return r }), "token 1 sits at 6, its offset says 7"},
		{"zero length token", withRest(func(r []byte) []byte { r[offsets] = 0; return r }), "token 0 has a malformed length"},
		{"unsorted tokens", withRest(func(r []byte) []byte { copy(r[offsets+1:], "zzzzz"); return r }), `tokens "zzzzz" and "beta" are not sorted`},
		{"short rows", withRest(func(r []byte) []byte { return r[:len(r)-1] }), "197 bytes follow the tokens, but 3 tokens of 64 dims need 198"},
		{"extra byte", withRest(func(r []byte) []byte { return append(r, 0) }), "199 bytes follow the tokens"},
		{"minus 128", withRest(func(r []byte) []byte { r[tokEnd+6+64+3] = 0x80; return r }), `token "beta" holds -128`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tbl, err := ReadTable(tt.data)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ReadTable error = %v, want one containing %q", err, tt.want)
			}
			if tbl != nil {
				t.Error("ReadTable returned a table with its error")
			}
		})
	}
}

func TestTableReadRefusesEveryTruncation(t *testing.T) {
	data := encode(t, testSpec([]string{"alpha", "beta"}, 64))
	for n := range len(data) {
		if _, err := ReadTable(data[:n]); err == nil {
			t.Fatalf("ReadTable accepted the first %d of %d bytes", n, len(data))
		}
	}
}

func FuzzReadTable(f *testing.F) {
	f.Add(encode(f, testSpec([]string{"alpha", "beta", "foo_bar"}, 64)))
	f.Add(encode(f, testSpec([]string{"x"}, 128)))
	f.Add([]byte("MSET\x01"))
	f.Fuzz(func(t *testing.T, data []byte) {
		check := func(data []byte) {
			tbl, err := ReadTable(data)
			if err != nil {
				return
			}
			if tbl.Len() <= 0 || checkDims(tbl.Dims()) != nil {
				t.Fatalf("accepted Len %d, Dims %d", tbl.Len(), tbl.Dims())
			}
			for i := range min(tbl.Len(), 64) {
				tok := string(tbl.token(i))
				if j, ok := tbl.lookup(tok); !ok || j != i {
					t.Fatalf("lookup(%q) = %d, %v; want %d", tok, j, ok, i)
				}
				nonzero := tbl.weight(i) != 0 && slices.ContainsFunc(tbl.row(i), func(b byte) bool { return b != 0 })
				if _, ok := tbl.Embed([]Field{{Tokens: []string{tok}, Weight: 1}}); ok != nonzero {
					t.Fatalf("Embed(%q) ok = %v, want %v", tok, ok, nonzero)
				}
			}
		}
		check(data)
		// The trailer stops nearly every mutation; re-signing lets the structural
		// checks behind it see the input.
		sum := sha256.Sum256(data)
		check(append(slices.Clone(data), sum[:]...))
	})
}

// updateGolden names the variable that rewrites the golden files from this build.
const updateGolden = "UPDATE_EMBED_GOLDEN"

var (
	goldenTablePath   = filepath.Join("testdata", "golden-table.mset")
	goldenRankingPath = filepath.Join("testdata", "golden.json")
)

var goldenDocs = []struct{ id, text string }{
	{"bm25f", "func (ix *Index) Score(terms []string) []Hit scores BM25F over every field"},
	{"fuse", "Fuse merges ranked lists by weighted reciprocal rank fusion"},
	{"pagerank", "PersonalizedPageRank walks the graph from seeds with restart"},
	{"pull", "Pull fetches the embedding table layer into the cache by rename"},
	{"load", "Load reads the cached table and checks its sha256 against the pin"},
	{"tokens", "Tokens splits text into lowercase words and identifier parts"},
	{"search", "Search scores every allowed doc against the query vector exactly"},
	{"spell", "a spell declares ops that a target runs in the sandbox"},
	{"cache", "the build cache keys each target by the digest of its inputs"},
	{"lease", "a lease binds a job to its write paths in one worktree"},
	{"guard", "the guard denies a command that pipes magus output"},
	{"weights", "weights.json holds the BM25F field weights and the fusion lists"},
}

var goldenQueries = []string{
	"rank fusion",
	"embedding table cache",
	"BM25F field weights",
	"PersonalizedPageRank seeds",
	"sha256 pin",
	"identifier parts",
	"query vector",
	"target sandbox",
	"write paths",
	"pipes output",
}

type goldenQuery struct {
	Query   string   `json:"query"`
	InvNorm uint32   `json:"inv_norm"`
	Q       []int8   `json:"q"`
	Ranked  []string `json:"ranked"`
}

// goldenSpec is a 64-dim table over every token of the docs and queries, derived as
// testSpec derives rows, so it is the same on every machine.
func goldenSpec() TableSpec {
	var vocab []string
	for _, text := range slices.Concat(goldenQueries, docTexts()) {
		for _, tok := range Tokens(text) {
			if Tokens(tok)[0] == tok {
				vocab = append(vocab, tok)
			}
		}
	}
	slices.Sort(vocab)
	return testSpec(slices.Compact(vocab), 64)
}

func docTexts() []string {
	out := make([]string, len(goldenDocs))
	for i, d := range goldenDocs {
		out[i] = d.text
	}
	return out
}

func goldenRanking(t *testing.T, tbl *Table) []goldenQuery {
	t.Helper()
	docs := make([][]Field, len(goldenDocs))
	for i, d := range goldenDocs {
		docs[i] = []Field{{Tokens: Tokens(d.text), Weight: 1}}
	}
	ix := NewIndex(tbl, docs)
	out := make([]goldenQuery, len(goldenQueries))
	for i, q := range goldenQueries {
		v, ok := tbl.Embed([]Field{{Tokens: Tokens(q), Weight: 1}})
		if !ok {
			t.Fatalf("query %q embeds to no vector", q)
		}
		out[i] = goldenQuery{Query: q, InvNorm: v.InvNorm, Q: v.Q}
		for _, h := range ix.Search(v, SearchOptions{Depth: len(goldenDocs)}) {
			out[i].Ranked = append(out[i].Ranked, goldenDocs[h.Doc].id)
		}
	}
	return out
}

// TestShippedGoldenTable pins the bytes EncodeTable writes for goldenSpec.
func TestShippedGoldenTable(t *testing.T) {
	data := encode(t, goldenSpec())
	if os.Getenv(updateGolden) != "" {
		if err := os.WriteFile(goldenTablePath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenTablePath)
	if err != nil {
		t.Fatalf("%v: regenerate with %s=1", err, updateGolden)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("EncodeTable wrote %d bytes that differ from %s", len(data), goldenTablePath)
	}
}

// TestShippedGoldenRanking pins the query vectors and rankings over the committed table.
// Embed and Search use no float, so these hold byte for byte on amd64 and arm64; a
// mismatch on one architecture only is a determinism bug, never a reason to update.
func TestShippedGoldenRanking(t *testing.T) {
	data, err := os.ReadFile(goldenTablePath)
	if err != nil {
		t.Fatal(err)
	}
	got := goldenRanking(t, read(t, data))
	if os.Getenv(updateGolden) != "" {
		out := []byte("[\n")
		for i, q := range got {
			line, err := json.Marshal(q)
			if err != nil {
				t.Fatal(err)
			}
			if i > 0 {
				out = append(out, ",\n"...)
			}
			out = append(out, line...)
		}
		if err := os.WriteFile(goldenRankingPath, append(out, "\n]\n"...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(goldenRankingPath)
	if err != nil {
		t.Fatalf("%v: regenerate with %s=1", err, updateGolden)
	}
	var want []goldenQuery
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(goldenQueries) {
		t.Fatalf("%s holds %d queries, want %d", goldenRankingPath, len(want), len(goldenQueries))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("query %q:\n got %+v\nwant %+v", got[i].Query, got[i], want[i])
		}
	}
}
