package knowledge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/textrank"
	"github.com/egladman/magus/types"
)

// lexicalIndexFile is the one lexical index every free-text query ranks from: a JSON header
// line naming the shards it was built from, then the textrank binary encoding. It is a pure
// function of the domain and symbol shards and their lexical sidecars, bound to the shards
// by lexicalShardsKey, and never the source of truth. The encoding is binary, so the name
// does not claim JSON.
const lexicalIndexFile = "@lexical.idx"

// lexicalSidecarDir holds one JSON Lines sidecar per domain and symbol shard, named by the
// shard's slug: a lexicalSidecarHeader line, then one lexicalRecord per node in the shard's
// canonical order. It is what the lexical index reads for a shard the current sync did not
// assemble, so a rebuild never decodes a whole shard.
const lexicalSidecarDir = "lexical"

// lexicalFormat versions lexicalShardsKey, so an index written under another layout or
// tokenization never binds.
const lexicalFormat = 1

// lexicalIndexHeader is the index file's first line.
type lexicalIndexHeader struct {
	ShardsKey string `json:"shards_key"`
}

// lexicalSidecarHeader is a sidecar's first line. Fingerprint is the shard's; TextDigest is
// textDigest of the shard's Text, which the fingerprint does not cover.
type lexicalSidecarHeader struct {
	Shard       string `json:"shard"`
	Fingerprint string `json:"fingerprint"`
	TextDigest  string `json:"text_digest,omitempty"`
}

// lexicalRecord is one node as the lexical index reads it: the node fields lexicalDocument
// takes, and the node's Text entry.
type lexicalRecord struct {
	ID        string `json:"id"`
	Label     string `json:"label,omitempty"`
	Source    string `json:"source,omitempty"`
	Doc       string `json:"doc,omitempty"`
	SymbolDoc string `json:"symbol_doc,omitempty"`
	Signature string `json:"signature,omitempty"`
	Text      string `json:"text,omitempty"`
}

func (r lexicalRecord) node() types.KnowledgeNode {
	n := types.KnowledgeNode{ID: r.ID, Label: r.Label, Source: r.Source, Doc: r.Doc}
	if r.SymbolDoc != "" || r.Signature != "" {
		n.Attrs = map[string]string{}
		if r.SymbolDoc != "" {
			n.Attrs[AttrDoc] = r.SymbolDoc
		}
		if r.Signature != "" {
			n.Attrs[AttrSignature] = r.Signature
		}
	}
	return n
}

// lexicalClass reports whether a shard's nodes belong in the lexical index: the domain and
// code symbols. Overlays annotate nodes those shards already hold.
func lexicalClass(name string) bool {
	c := shardClass(name)
	return c == ClassDomain || c == ClassSymbols
}

// lexicalDocument is n as a ranked document. name holds the id's last segment and the
// label, path the source file, doc the node doc and a symbol's doc comment, signature a
// symbol's signature, and body the node's Text entry. Every field goes through
// textrank.Tokens, so an identifier is found by its whole name or any of its words.
func lexicalDocument(n types.KnowledgeNode, body string) textrank.Document {
	fields := map[string][]string{}
	add := func(field string, texts ...string) {
		parts := make([][]string, 0, len(texts))
		for _, t := range texts {
			parts = append(parts, textrank.Tokens(t))
		}
		if toks := slices.Concat(parts...); len(toks) > 0 {
			fields[field] = toks
		}
	}
	segment := n.ID[strings.LastIndexAny(n.ID, "/:")+1:]
	if n.Label != "" && n.Label != segment {
		add(textrank.FieldName, segment, n.Label)
	} else {
		add(textrank.FieldName, segment)
	}
	add(textrank.FieldPath, sourcePath(n.Source))
	add(textrank.FieldDoc, n.Doc, n.Attrs[AttrDoc])
	add(textrank.FieldSignature, n.Attrs[AttrSignature])
	add(textrank.FieldBody, body)
	return textrank.Document{ID: n.ID, Fields: fields}
}

// sourcePath cuts a node source to its file: no "#anchor", no ":line" or ":line:col".
func sourcePath(source string) string {
	if i := strings.IndexByte(source, '#'); i >= 0 {
		source = source[:i]
	}
	for {
		i := strings.LastIndexByte(source, ':')
		if i < 0 || i == len(source)-1 || strings.Trim(source[i+1:], "0123456789") != "" {
			return source
		}
		source = source[:i]
	}
}

// buildLexicalIndex indexes every node of the domain and symbol shards among shards, each
// with its texts entry as its body. A node in several shards is indexed once, from the
// first shard in the order the graph merges them (domain shards by name, then symbol
// shards by name), the copy the merged graph keeps.
func buildLexicalIndex(shards []Shard, texts map[string]string) *textrank.Index {
	picked := make([]Shard, 0, len(shards))
	for _, sh := range shards {
		if lexicalClass(sh.Name) {
			picked = append(picked, sh)
		}
	}
	slices.SortFunc(picked, func(a, b Shard) int {
		if la, lb := isSymbolsShard(a.Name), isSymbolsShard(b.Name); la != lb {
			if la {
				return 1
			}
			return -1
		}
		return strings.Compare(a.Name, b.Name)
	})
	seen := map[string]bool{}
	var docs []textrank.Document
	for _, sh := range picked {
		for _, n := range sh.Nodes {
			if !seen[n.ID] {
				seen[n.ID] = true
				docs = append(docs, lexicalDocument(n, texts[n.ID]))
			}
		}
	}
	return textrank.NewIndex(docs)
}

// lexicalShardsKey hashes the sorted (name, fingerprint) of every domain and symbol shard in
// the manifest, the identity the lexical index is bound to. Empty when there are none.
func lexicalShardsKey(man *manifest) string {
	if man == nil {
		return ""
	}
	var names []string
	for name := range man.Shards {
		if lexicalClass(name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	slices.Sort(names)
	buf := []byte{lexicalFormat}
	for _, name := range names {
		buf = appendField(buf, name)
		buf = appendField(buf, man.Shards[name].Fingerprint)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// textDigest hashes a shard's Text in id order. Empty for no text.
func textDigest(text map[string]string) string {
	if len(text) == 0 {
		return ""
	}
	h := sha256.New()
	var buf []byte
	for _, id := range slices.Sorted(maps.Keys(text)) {
		buf = appendField(appendField(buf[:0], id), text[id])
		_, _ = h.Write(buf) // hash.Hash never errors
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Store) lexicalIndexPath() string { return filepath.Join(s.dir, lexicalIndexFile) }

// lexicalSidecarPath is the sidecar of the shard stored at shardFile.
func (s *Store) lexicalSidecarPath(shardFile string) string {
	return filepath.Join(s.dir, lexicalSidecarDir, strings.TrimSuffix(filepath.Base(shardFile), ".json")+".jsonl")
}

// writeLexicalSidecar writes the sidecar of shard name at fingerprint fp. nodes are the
// shard's canonical nodes; a text entry for an id no node holds is dropped.
func (s *Store) writeLexicalSidecar(name, fp string, nodes []types.KnowledgeNode, text map[string]string) error {
	var buf bytes.Buffer
	line := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
		return nil
	}
	if err := line(lexicalSidecarHeader{Shard: name, Fingerprint: fp, TextDigest: textDigest(text)}); err != nil {
		return err
	}
	for _, n := range nodes {
		rec := lexicalRecord{ID: n.ID, Label: n.Label, Source: n.Source, Doc: n.Doc,
			SymbolDoc: n.Attrs[AttrDoc], Signature: n.Attrs[AttrSignature], Text: text[n.ID]}
		if err := line(rec); err != nil {
			return err
		}
	}
	return file.WriteFileAtomic(s.lexicalSidecarPath(s.shardPath(name)), buf.Bytes(), 0o644)
}

// lexicalSidecarCurrent reports whether shard name's sidecar was written at fingerprint fp
// with text of digest digest. It reads only the header line.
func (s *Store) lexicalSidecarCurrent(name, fp, digest string) bool {
	f, err := os.Open(s.lexicalSidecarPath(s.shardPath(name)))
	if err != nil {
		return false
	}
	defer f.Close()
	first, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		return false
	}
	var h lexicalSidecarHeader
	if json.Unmarshal(first, &h) != nil {
		return false
	}
	return h.Shard == name && h.Fingerprint == fp && h.TextDigest == digest
}

// readLexicalSidecar returns shard name as its sidecar holds it, nodes cut to their lexical
// fields, failing unless the sidecar was written at fingerprint fp.
func (s *Store) readLexicalSidecar(name, fp string) (Shard, error) {
	b, err := os.ReadFile(s.lexicalSidecarPath(s.shardPath(name)))
	if err != nil {
		return Shard{}, err
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte{'\n'})
	var h lexicalSidecarHeader
	if err := json.Unmarshal(lines[0], &h); err != nil {
		return Shard{}, fmt.Errorf("lexical sidecar of %s: header: %w", name, err)
	}
	if h.Shard != name || h.Fingerprint != fp {
		return Shard{}, fmt.Errorf("lexical sidecar of %s was written for %s at %s, not at %s", name, h.Shard, h.Fingerprint, fp)
	}
	sh := Shard{Name: name, Nodes: make([]types.KnowledgeNode, 0, len(lines)-1), Text: map[string]string{}}
	for _, ln := range lines[1:] {
		var rec lexicalRecord
		if err := json.Unmarshal(ln, &rec); err != nil {
			return Shard{}, fmt.Errorf("lexical sidecar of %s: %w", name, err)
		}
		sh.Nodes = append(sh.Nodes, rec.node())
		if rec.Text != "" {
			sh.Text[rec.ID] = rec.Text
		}
	}
	return sh, nil
}

// lexicalShards returns every domain and symbol shard man names: the ones in have as they
// are, and every other read from its sidecar. A missing or stale sidecar is an error, since
// an index built without it would lack that shard's nodes or bodies.
func (s *Store) lexicalShards(ctx context.Context, man *manifest, have []Shard) ([]Shard, error) {
	present := map[string]bool{}
	out := make([]Shard, 0, len(man.Shards))
	for _, sh := range have {
		if lexicalClass(sh.Name) {
			present[sh.Name] = true
			out = append(out, sh)
		}
	}
	var names []string
	for name := range man.Shards {
		if lexicalClass(name) && !present[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	read := make([]Shard, len(names))
	eg, egctx := errgroup.WithContext(ctx)
	eg.SetLimit(runtime.GOMAXPROCS(0))
	for i, name := range names {
		eg.Go(func() error {
			if err := egctx.Err(); err != nil {
				return err
			}
			sh, err := s.readLexicalSidecar(name, man.Shards[name].Fingerprint)
			read[i] = sh
			return err
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return append(out, read...), nil
}

// writeLexicalIndex writes the lexical index over shards, bound to key, or removes it when
// key is empty. shards must hold every domain and symbol shard key covers, with its Text.
func (s *Store) writeLexicalIndex(shards []Shard, key string) error {
	if key == "" {
		return s.removeLexicalIndex()
	}
	texts := map[string]string{}
	for _, sh := range shards {
		for id, t := range sh.Text {
			if _, ok := texts[id]; !ok {
				texts[id] = t
			}
		}
	}
	var buf bytes.Buffer
	header, err := json.Marshal(lexicalIndexHeader{ShardsKey: key})
	if err != nil {
		return err
	}
	buf.Write(header)
	buf.WriteByte('\n')
	if _, err := buildLexicalIndex(shards, texts).WriteTo(&buf); err != nil {
		return err
	}
	return file.WriteFileAtomic(s.lexicalIndexPath(), buf.Bytes(), 0o644)
}

func (s *Store) removeLexicalIndex() error {
	if err := os.Remove(s.lexicalIndexPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// lexicalIndexKey is the shards key the stored index was built under, "" when there is none.
func (s *Store) lexicalIndexKey() string {
	f, err := os.Open(s.lexicalIndexPath())
	if err != nil {
		return ""
	}
	defer f.Close()
	h, err := readLexicalHeader(bufio.NewReader(f))
	if err != nil {
		return ""
	}
	return h.ShardsKey
}

// readLexicalHeader consumes the index file's header line from r.
func readLexicalHeader(r *bufio.Reader) (lexicalIndexHeader, error) {
	first, err := r.ReadBytes('\n')
	if err != nil {
		return lexicalIndexHeader{}, err
	}
	var h lexicalIndexHeader
	err = json.Unmarshal(first, &h)
	return h, err
}

// readLexicalIndex returns the lexical index when it is bound to man's domain and symbol
// shards, else nil: absent, unreadable, or built from other shards. A caller states the
// nil; it never ranks from a stale index.
func (s *Store) readLexicalIndex(man *manifest) *textrank.Index {
	key := lexicalShardsKey(man)
	if key == "" {
		return nil
	}
	f, err := os.Open(s.lexicalIndexPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	r := bufio.NewReader(f)
	h, err := readLexicalHeader(r)
	if err != nil || h.ShardsKey != key {
		return nil
	}
	ix, err := textrank.ReadIndex(r)
	if err != nil {
		return nil
	}
	return ix
}

// syncLexicalIndex rebuilds the lexical index for man after a sync that carried shards. A
// failure removes the index rather than leave one bound to shards it does not reflect.
func (s *Store) syncLexicalIndex(ctx context.Context, shards []Shard, man *manifest) error {
	all, err := s.lexicalShards(ctx, man, shards)
	if err == nil {
		err = s.writeLexicalIndex(all, lexicalShardsKey(man))
	}
	if err != nil {
		return errors.Join(err, s.removeLexicalIndex())
	}
	return nil
}
