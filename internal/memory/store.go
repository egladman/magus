// Package memory is the durable, per-repository handoff journal: discrete,
// categorized records (one markdown file per entry, YAML frontmatter carrying the
// structured fields) plus a legacy cursor snapshot. It is the one place that owns
// where journal entries live and how a record is serialized; the MCP tool
// (internal/handler/mcp), the console RPC (internal/handler/memory), and the
// knowledge-graph @memory shard all read/write through it.
//
// The store lives in the user's XDG state directory, NOT the repo (a developer's
// working memory does not belong in a shared checkout) and NOT the cache (evictable).
// It is keyed by repository identity, so every worktree of one repo shares one memory.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"gopkg.in/yaml.v3"
)

// RecordType is the closed subject axis a record's Type may take. A named string type so
// the compiler carries the closed set the values below promise, not just Validate at
// runtime. pointer carries no prose; decision/plan carry a ref-anchored prose caption.
type RecordType string

const (
	TypePointer  RecordType = "pointer"
	TypeDecision RecordType = "decision"
	TypePlan     RecordType = "plan"
)

// RefKind is the closed set a Ref.Kind may take. node/doc/output name a magus-domain node;
// query/command are re-runnable strings. All five are resolvable, so staleness is
// detectable (the isolation and graph-anchoring live in the deferred Phase 2 shard).
type RefKind string

const (
	RefKindQuery   RefKind = "query"
	RefKindNode    RefKind = "node"
	RefKindOutput  RefKind = "output"
	RefKindCommand RefKind = "command"
	RefKindDoc     RefKind = "doc"
)

// Ref is one typed pointer a record carries: Kind is the closed ref-kind
// (query/node/output/command/doc); Target is the payload (a node ID or path, an output ref
// token, or a raw query/command string).
type Ref struct {
	Kind   RefKind `json:"kind" yaml:"kind"`
	Target string  `json:"target" yaml:"target"`
}

// Record is one persisted memory. The payload is one or more typed Refs; Body is a
// prose caption present only for decision/plan records (empty for pointer). Created
// and Updated are unix seconds, stamped by the store (output-only to callers).
type Record struct {
	Name       string     `json:"name" yaml:"name"`
	Type       RecordType `json:"type" yaml:"type"`
	Status     string     `json:"status,omitempty" yaml:"status,omitempty"`
	Refs       []Ref      `json:"refs" yaml:"refs"`
	References []string   `json:"references,omitempty" yaml:"references,omitempty"`
	Created    int64      `json:"created" yaml:"created,omitempty"`
	Updated    int64      `json:"updated" yaml:"updated,omitempty"`
	Body       string     `json:"body,omitempty" yaml:"-"`
}

// Issue is one actionable problem found by Verify. A warning does not make a
// journal unreadable; an error does and causes list consumers to stop rather than
// quietly omit the entry.
type Issue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path"`
	Record   string `json:"record,omitempty"`
	Message  string `json:"message"`
	Hint     string `json:"hint"`
}

// Verification is the deterministic result of checking a handoff journal.
type Verification struct {
	Records int     `json:"records"`
	Issues  []Issue `json:"issues"`
}

// nameRE is the record name shape: a kebab slug. It doubles as the on-disk basename,
// so it must be filesystem-safe - lowercase alphanumerics joined by single hyphens,
// no slashes or dots, which keeps a name from escaping the records directory.
var nameRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// recordsSubdir holds one file per record, kept separate from the cursor snapshot and
// any legacy/rotated journals so a directory listing of records is unambiguous.
const recordsSubdir = "records"

// cursorFile is the single "where did I leave off" snapshot beside the record set. It
// is NOT a record and never becomes a graph node - a cursor, not an accumulating log.
const cursorFile = "cursor.md"

// Dir resolves the per-repository memory directory:
// <XDG state>/magus/memory/<repo-basename>-<hash12>. The hash keys on repository
// identity, not the checkout path, so every worktree of a repo shares one memory.
func Dir(root string) (string, error) {
	base, err := config.UserStateDir()
	if err != nil {
		return "", fmt.Errorf("memory: state dir: %w", err)
	}
	id := repoIdentity(root)
	sum := sha256.Sum256([]byte(id))
	name := filepath.Base(id) + "-" + hex.EncodeToString(sum[:])[:12]
	return filepath.Join(base, "magus", "memory", name), nil
}

// repoIdentity returns the path that identifies the repository behind root. A linked
// worktree's .git is a file holding "gitdir: <main>/.git/worktrees/<n>"; resolve it to
// <main> so worktrees share identity. Anything else identifies as root itself.
func repoIdentity(root string) string {
	b, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root // .git is a directory (plain checkout) or absent (other VCS)
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if gitdir == "" {
		return root
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	if i := strings.Index(filepath.ToSlash(gitdir), "/.git/worktrees/"); i >= 0 {
		return filepath.Clean(gitdir[:i])
	}
	return filepath.Clean(gitdir)
}

// Validate enforces the record schema on the way IN (the rules the whole feature rests
// on): a known type, at least one ref, a known kind on every ref, and prose only where
// it is allowed. Rejecting a bad record at the door keeps the store, the graph, and the
// console from ever holding a shape the model does not expect.
func Validate(r Record) error {
	if !nameRE.MatchString(r.Name) {
		return fmt.Errorf("memory: name %q must be a kebab slug (lowercase alphanumerics and hyphens)", r.Name)
	}
	switch r.Type {
	case TypePointer, TypeDecision, TypePlan:
	default:
		return fmt.Errorf("memory: type must be one of pointer, decision, plan (got %q)", r.Type)
	}
	if len(r.Refs) == 0 {
		return errors.New("memory: a record needs at least one ref; if you cannot name a ref kind, it is not a memory, it is a query you should just run")
	}
	for _, ref := range r.Refs {
		switch ref.Kind {
		case RefKindQuery, RefKindNode, RefKindOutput, RefKindCommand, RefKindDoc:
		default:
			return fmt.Errorf("memory: ref kind must be one of query, node, output, command, doc (got %q)", ref.Kind)
		}
		if strings.TrimSpace(ref.Target) == "" {
			return fmt.Errorf("memory: ref of kind %q has an empty target", ref.Kind)
		}
	}
	if r.Type == TypePointer && strings.TrimSpace(r.Body) != "" {
		return errors.New("memory: a pointer carries no prose; its refs are the payload (only decision/plan take a caption)")
	}
	for _, name := range r.References {
		if !nameRE.MatchString(name) {
			return fmt.Errorf("memory: reference %q must be a kebab slug", name)
		}
	}
	return nil
}

// ParseRefs parses one ref per line in "kind: target" form. Newline is the only
// separator because queries and node IDs commonly contain spaces, colons, or commas.
func ParseRefs(s string) ([]Ref, error) {
	var refs []Ref
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.IndexByte(line, ':')
		if i < 0 {
			return nil, fmt.Errorf("memory: ref %q must be written as 'kind: target' (kinds: query, node, output, command, doc)", line)
		}
		refs = append(refs, Ref{Kind: RefKind(strings.TrimSpace(line[:i])), Target: strings.TrimSpace(line[i+1:])})
	}
	return refs, nil
}

// List returns every record in name order. An invalid on-disk entry is an error rather
// than a silently skipped record; run Verify for every problem and its repair hint.
func List(root string) ([]Record, error) {
	recs, issues, err := Inspect(root)
	if err != nil {
		return nil, err
	}
	if len(issues) != 0 {
		return nil, issuesError(issues)
	}
	return recs, nil
}

// Verify scans every entry without hiding malformed files or broken journal links.
// A missing store is valid and reports zero entries.
func Verify(root string) (Verification, error) {
	recs, issues, err := Inspect(root)
	if err != nil {
		return Verification{}, err
	}
	return Verification{Records: len(recs), Issues: issues}, nil
}

// Inspect returns readable entries and every detected issue. It is for frontends that
// can present warnings alongside records; callers that only need safe records use List.
func Inspect(root string) ([]Record, []Issue, error) {
	return scan(root)
}

func scan(root string) ([]Record, []Issue, error) {
	dir, err := Dir(root)
	if err != nil {
		return nil, nil, err
	}
	rdir := filepath.Join(dir, recordsSubdir)
	ents, err := os.ReadDir(rdir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("memory: list: %w", err)
	}
	out := make([]Record, 0)
	issues := make([]Issue, 0)
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(rdir, e.Name())
		rec, err := readRecordFile(path)
		if err != nil {
			issues = append(issues, Issue{
				Severity: "error", Code: "invalid-entry", Path: path,
				Message: err.Error(),
				Hint:    "Repair or remove this file, then run `magus memory verify` again.",
			})
			continue
		}
		out = append(out, rec)
	}
	slices.SortFunc(out, func(a, b Record) int { return strings.Compare(a.Name, b.Name) })
	known := make(map[string]struct{}, len(out))
	for _, rec := range out {
		known[rec.Name] = struct{}{}
		if strings.EqualFold(rec.Status, "stale") {
			issues = append(issues, Issue{
				Severity: "warning", Code: "stale-entry", Path: filepath.Join(rdir, rec.Name+".md"), Record: rec.Name,
				Message: "entry is marked stale",
				Hint:    "Refresh it with `magus memory put` or remove it with `magus memory delete`.",
			})
		}
	}
	for _, rec := range out {
		for _, ref := range rec.References {
			if _, ok := known[ref]; !ok {
				issues = append(issues, Issue{
					Severity: "error", Code: "missing-reference", Path: filepath.Join(rdir, rec.Name+".md"), Record: rec.Name,
					Message: fmt.Sprintf("references missing entry %q", ref),
					Hint:    "Create the referenced entry, update this entry with `magus memory put`, or delete the broken reference.",
				})
			}
		}
	}
	return out, issues, nil
}

func issuesError(issues []Issue) error {
	for _, issue := range issues {
		if issue.Severity == "error" {
			return fmt.Errorf("memory: journal has invalid entries; run `magus memory verify` for repair steps (%s)", issue.Message)
		}
	}
	return nil
}

// Get returns one record by name, or os.ErrNotExist if it is absent.
func Get(root, name string) (Record, error) {
	dir, err := Dir(root)
	if err != nil {
		return Record{}, err
	}
	if !nameRE.MatchString(name) {
		return Record{}, fmt.Errorf("memory: invalid name %q", name)
	}
	return readRecordFile(filepath.Join(dir, recordsSubdir, name+".md"))
}

// Put upserts a record: it validates, stamps Created (first write) / Updated (every
// write), and writes the file atomically. The name is the identity, so writing an
// existing name overwrites it. It returns the stored record (with timestamps) so a
// caller can echo the server-set fields back.
func Put(root string, r Record) (Record, error) {
	if err := Validate(r); err != nil {
		return Record{}, err
	}
	dir, err := Dir(root)
	if err != nil {
		return Record{}, err
	}
	rdir := filepath.Join(dir, recordsSubdir)
	if err := os.MkdirAll(rdir, 0o755); err != nil {
		return Record{}, fmt.Errorf("memory: put: %w", err)
	}
	now := time.Now().Unix()
	if prev, err := readRecordFile(filepath.Join(rdir, r.Name+".md")); err == nil {
		r.Created = prev.Created // preserve the original creation time on update
	}
	if r.Created == 0 {
		r.Created = now
	}
	r.Updated = now
	if err := writeAtomic(filepath.Join(rdir, r.Name+".md"), marshalRecord(r)); err != nil {
		return Record{}, err
	}
	return r, nil
}

// Delete removes a record. allowMissing decides whether deleting an absent record is a
// no-op (AIP-135 idempotent delete) or an error.
func Delete(root, name string, allowMissing bool) error {
	dir, err := Dir(root)
	if err != nil {
		return err
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("memory: invalid name %q", name)
	}
	err = os.Remove(filepath.Join(dir, recordsSubdir, name+".md"))
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return nil
	}
	if err != nil {
		return fmt.Errorf("memory: delete %q: %w", name, err)
	}
	return nil
}

// ReadCursor returns the cursor snapshot ("where did I leave off"), or "" if unwritten.
func ReadCursor(root string) (string, error) {
	dir, err := Dir(root)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, cursorFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("memory: read cursor: %w", err)
	}
	return string(b), nil
}

// WriteCursor overwrites the cursor snapshot.
func WriteCursor(root, content string) error {
	dir, err := Dir(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("memory: write cursor: %w", err)
	}
	return writeAtomic(filepath.Join(dir, cursorFile), []byte(content))
}

// frontmatterRE splits a record file into its YAML frontmatter and markdown body. The
// body (a decision/plan caption) is everything after the closing delimiter.
var frontmatterRE = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n?(.*)\z`)

func readRecordFile(path string) (Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err // callers distinguish os.ErrNotExist
	}
	m := frontmatterRE.FindSubmatch(b)
	if m == nil {
		return Record{}, fmt.Errorf("memory: %s: missing YAML frontmatter", filepath.Base(path))
	}
	var r Record
	if err := yaml.Unmarshal(m[1], &r); err != nil {
		return Record{}, fmt.Errorf("memory: %s: %w", filepath.Base(path), err)
	}
	r.Body = strings.TrimSpace(string(m[2]))
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	if r.Name != "" && r.Name != name {
		return Record{}, fmt.Errorf("memory: %s: frontmatter name %q does not match filename %q", filepath.Base(path), r.Name, name)
	}
	r.Name = name
	if err := Validate(r); err != nil {
		return Record{}, fmt.Errorf("memory: %s: %w", filepath.Base(path), err)
	}
	return r, nil
}

func marshalRecord(r Record) []byte {
	r.Body = strings.TrimSpace(r.Body)
	fm, _ := yaml.Marshal(r) // Record has no unmarshalable fields; error is unreachable
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	if r.Body != "" {
		b.WriteString("\n")
		b.WriteString(r.Body)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// writeAtomic writes via a temp file in the same directory then renames over the target,
// so a crash mid-write leaves either the old file or the new one whole, never a partial.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".*.tmp")
	if err != nil {
		return fmt.Errorf("memory: write %s: %w", filepath.Base(path), err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("memory: write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("memory: write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("memory: write %s: %w", filepath.Base(path), err)
	}
	return nil
}
