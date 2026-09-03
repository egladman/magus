// Package memory is the durable, per-repository handoff journal: discrete,
// categorized records (one markdown file per entry, YAML frontmatter carrying the
// structured fields) plus a legacy cursor snapshot. It is the one place that owns
// where journal entries live and how a record is serialized. Two consumers read and
// write through it: the MCP tool (internal/handler/mcp) and the console RPC
// (internal/handler/memory).
//
// There is NO knowledge-graph shard over these records. This comment used to name an
// "@memory shard" among the consumers and it does not exist - knowledge.Inputs has a
// Notes field and no Memory one, and AssembleShards builds nothing for it. So a record
// is invisible to `magus query`, carries no edges to what it points at, and cannot be
// drift-checked: nothing anchors it. See RefKind on what that costs.
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
	"github.com/egladman/magus/internal/file"
	"gopkg.in/yaml.v3"
)

// RecordType is the closed subject axis a record's Type may take. A named string type so
// the compiler carries the closed set the values below promise, not just Validate at
// runtime. pointer carries no prose; decision/plan carry a ref-anchored prose caption.
//
// elimination is the falsified axis: a hypothesis an investigation ruled out. The other
// three report intent, a settled choice, and a location, so overloading one would make a
// listing misreport the entry.
type RecordType string

const (
	TypePointer     RecordType = "pointer"
	TypeDecision    RecordType = "decision"
	TypePlan        RecordType = "plan"
	TypeElimination RecordType = "elimination"
)

// RefKind is the closed set a Ref.Kind may take. node/doc/output name a magus-domain node;
// query/command are re-runnable strings.
//
// All five are resolvable IN PRINCIPLE, which is not the same as resolved. This package
// knows none of the stores behind them, so Verify takes a RefResolver from its caller and
// reports a decayed ref only when it gets one. The "deferred Phase 2 shard" an earlier
// version of this comment pointed at was never built, so the graph side of the gap is
// still open.
//
// An output ref is the shortest-lived of the five. Output blobs live under the checkout
// that produced them while this store is keyed by repository, so a ref minted in a
// worktree that has since been removed resolves from nowhere. That asymmetry is why an
// elimination copies its evidence into Excerpt.
//
// A node ref's Target is additionally a notes-anchor-shaped string ("symbol:...", "file:...",
// "project:...", "target:..."), parsed by notes.ParseAnchor when `magus notes promote` turns a
// record into a note. That coupling is real and undeclared: this package names no anchor kinds,
// so nothing stops a Target that the notes vocabulary cannot read, and promotion silently skips
// the ones it cannot parse.
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
// prose caption present only for decision/plan/elimination records (empty for pointer).
// Created and Updated are unix seconds, stamped by the store (output-only to callers).
//
// Excerpt belongs to an elimination alone: the captured evidence that falsified the
// hypothesis, copied in so the record outlives the ref beside it. It lives in the
// frontmatter because Body already owns everything after the closing delimiter, and the
// two round-trip separately.
type Record struct {
	Name       string     `json:"name" yaml:"name"`
	Type       RecordType `json:"type" yaml:"type"`
	Status     string     `json:"status,omitempty" yaml:"status,omitempty"`
	Refs       []Ref      `json:"refs" yaml:"refs"`
	References []string   `json:"references,omitempty" yaml:"references,omitempty"`
	Created    int64      `json:"created" yaml:"created,omitempty"`
	Updated    int64      `json:"updated" yaml:"updated,omitempty"`
	Excerpt    string     `json:"excerpt,omitempty" yaml:"excerpt,omitempty"`
	Body       string     `json:"body,omitempty" yaml:"-"`
}

// Issue is one actionable problem found by Verify. Severity says whether a human has
// repair work to do, and no severity withholds a record: every problem here is scoped to
// one entry, so the readable entries are always returned beside it.
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

// ErrUnknownType marks a record whose Type this binary does not know. Writing one is an
// error; reading one is not. The journal is durable user data shared by binaries of many
// ages, so a type a newer magus introduced has to degrade to a skipped entry with a
// warning, leaving the rest of the journal readable by the older binary that met it.
var ErrUnknownType = errors.New("memory: unknown record type")

// ErrInvalid marks a write the caller got wrong rather than one that failed to store, so a
// frontend can answer InvalidArgument without re-deriving why.
var ErrInvalid = errors.New("memory: invalid record")

// invalidError matches ErrInvalid without prefixing its text onto the message.
type invalidError struct{ err error }

func (e invalidError) Error() string   { return e.err.Error() }
func (e invalidError) Unwrap() []error { return []error{ErrInvalid, e.err} }

func invalidf(format string, a ...any) error { return invalidError{fmt.Errorf(format, a...)} }

// Validate enforces the record schema on the way IN (the rules the whole feature rests
// on): a known type, at least one ref, a known kind on every ref, and prose only where
// it is allowed. Rejecting a bad record at the door keeps the store, the graph, and the
// console from ever holding a shape the model does not expect.
//
// It also runs on the way OUT, where its verdict is advisory: readRecordFile reports a
// failure as an issue against that one file and scan skips it.
func Validate(r Record) error {
	if !nameRE.MatchString(r.Name) {
		return fmt.Errorf("memory: name %q must be a kebab slug (lowercase alphanumerics and hyphens)", r.Name)
	}
	switch r.Type {
	case TypePointer, TypeDecision, TypePlan, TypeElimination:
	default:
		return fmt.Errorf("%w %q (want pointer, decision, plan, or elimination)", ErrUnknownType, r.Type)
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
		return errors.New("memory: a pointer carries no prose; its refs are the payload (only decision/plan/elimination take a caption)")
	}
	if err := validateExcerpt(r); err != nil {
		return err
	}
	for _, name := range r.References {
		if !nameRE.MatchString(name) {
			return fmt.Errorf("memory: reference %q must be a kebab slug", name)
		}
	}
	return nil
}

// validateExcerpt enforces the rule the elimination type exists for: the record has to
// stand on its own once the ref beside it stops resolving. A body with no excerpt is a
// verdict a later reader can only take on faith, so both are required here and nowhere
// else.
func validateExcerpt(r Record) error {
	if r.Type != TypeElimination {
		if strings.TrimSpace(r.Excerpt) != "" {
			return fmt.Errorf("memory: only an elimination carries an excerpt (type is %q)", r.Type)
		}
		return nil
	}
	if strings.TrimSpace(r.Body) == "" {
		return errors.New("memory: an elimination needs a body saying why the hypothesis is dead")
	}
	if strings.TrimSpace(r.Excerpt) == "" {
		return errors.New("memory: an elimination needs an excerpt of the evidence that killed the hypothesis; a ref alone dies with the checkout that minted it")
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

// List returns every readable record in name order, skipping the entries it cannot read.
// This journal is the surface a human uses to find and delete a bad entry, so a corrupt
// file that took the listing down would leave nowhere to make the repair from. Only a
// failure to read the store itself returns an error. Run Verify for what was skipped and
// why.
func List(root string) ([]Record, error) {
	recs, _, err := Inspect(root)
	return recs, err
}

// RefResolver reports whether one external evidence ref can still be reopened, returning
// nil when it can. A kind the caller does not check resolves trivially.
//
// The caller supplies it because the stores behind a ref are not this package's to know:
// the cache answers an output ref, the graph a node. Threading either store in here would
// make the record serializer depend on the engine to read its own files.
type RefResolver func(Ref) error

// Verify scans every entry without hiding malformed files or broken journal links, then
// asks resolve whether each record's evidence can still be reopened. A missing store is
// valid and reports zero entries.
//
// A decayed ref is a warning. The entry keeps whatever it copied inline, so it degrades
// and stays readable.
func Verify(root string, resolve RefResolver) (Verification, error) {
	recs, issues, err := Inspect(root)
	if err != nil {
		return Verification{}, err
	}
	dir, err := Dir(root)
	if err != nil {
		return Verification{}, err
	}
	for _, rec := range recs {
		for _, ref := range rec.Refs {
			if resolve(ref) == nil {
				continue
			}
			// The underlying error is dropped on purpose: a cache miss reports that the
			// run had different inputs, which misdiagnoses the common case of a ref whose
			// checkout was deleted.
			issues = append(issues, Issue{
				Severity: "warning", Code: "unresolvable-ref",
				Path:    filepath.Join(dir, recordsSubdir, rec.Name+".md"),
				Record:  rec.Name,
				Message: fmt.Sprintf("evidence ref %q no longer resolves", string(ref.Kind)+": "+ref.Target),
				Hint:    "An output blob lives in the checkout that produced it, so a ref minted in a removed worktree cannot be reopened anywhere. Re-run the work for a fresh ref, or copy what it showed into the entry's excerpt.",
			})
		}
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
		if errors.Is(err, ErrUnknownType) {
			// A record a newer magus wrote. Nothing is broken and there is nothing to
			// repair, so this stays a warning and `verify` stays green on it.
			issues = append(issues, Issue{
				Severity: "warning", Code: "unknown-entry-type", Path: path,
				Message: err.Error(),
				Hint:    "A newer magus wrote this entry. Skipped here; upgrade to read it, or delete it with `magus memory delete`.",
			})
			continue
		}
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

// mutableFields is the closed set a mask may name, spelled as the proto's fields so a
// console mask needs no translation. Name is the identity and the timestamps are server-set.
var mutableFields = []string{"type", "status", "refs", "references", "body", "excerpt"}

// MutableFields returns every field path a mask may name. A caller that means a full
// replace passes all of them.
func MutableFields() []string { return slices.Clone(mutableFields) }

// UpdateOptions carries Update's AIP-134 contract: the field mask, and whether an absent
// name is created.
type UpdateOptions struct {
	// Mask names the fields to write, from MutableFields. Empty means the fields the
	// caller populated, which cannot unset one: an empty value there reads as unchanged,
	// so a caller that means to clear a field names it.
	Mask []string
	// AllowMissing creates the record when the name is absent; without it an absent name
	// is os.ErrNotExist, so a mistyped name is an error rather than a second entry.
	AllowMissing bool
}

// Update writes the fields opts.Mask names and keeps every other field the journal holds.
// The store has no history, so a field the caller does not name has nothing to restore it
// from. Created is preserved, Updated stamped, the file written atomically, and the stored
// record returned so a caller can echo the server-set fields back.
//
// Type is refused rather than merged: it is the subject axis a listing reports and it
// decides which fields the entry may carry, so changing it is a delete and a create.
// Naming the type already stored is a no-op. Validate runs on the MERGED record, so an
// invariant an update would break is refused whole.
func Update(root string, r Record, opts UpdateOptions) (Record, error) {
	if !nameRE.MatchString(r.Name) {
		return Record{}, invalidf("memory: name %q must be a kebab slug (lowercase alphanumerics and hyphens)", r.Name)
	}
	fields, err := maskFields(r, opts.Mask)
	if err != nil {
		return Record{}, err
	}
	dir, err := Dir(root)
	if err != nil {
		return Record{}, err
	}
	rdir := filepath.Join(dir, recordsSubdir)
	path := filepath.Join(rdir, r.Name+".md")
	prev, exists, err := mergeBase(path, fields)
	if err != nil {
		return Record{}, err
	}
	if !exists && !opts.AllowMissing {
		return Record{}, fmt.Errorf("memory: no entry named %q: %w", r.Name, os.ErrNotExist)
	}
	merged, err := applyFields(prev, r, fields)
	if err != nil {
		return Record{}, err
	}
	if err := Validate(merged); err != nil {
		return Record{}, invalidError{err}
	}
	// Captured output ends in a newline and a YAML block scalar drops it. Trimming on the
	// way in keeps the returned record equal to what a later Get reads.
	merged.Excerpt = strings.TrimRight(merged.Excerpt, "\n")
	if err := os.MkdirAll(rdir, 0o755); err != nil {
		return Record{}, fmt.Errorf("memory: update: %w", err)
	}
	now := time.Now().Unix()
	merged.Created = prev.Created
	if merged.Created == 0 {
		merged.Created = now
	}
	merged.Updated = now
	if err := writeAtomic(path, marshalRecord(merged)); err != nil {
		return Record{}, err
	}
	return merged, nil
}

// maskFields resolves an update's fields. A whitespace-only body or excerpt counts as
// unpopulated: marshalRecord trims both, so the stored record would not carry it anyway.
func maskFields(r Record, mask []string) (map[string]bool, error) {
	if len(mask) > 0 {
		out := make(map[string]bool, len(mask))
		for _, f := range mask {
			if !slices.Contains(mutableFields, f) {
				return nil, invalidf("memory: update mask names unknown field %q (want %s)", f, strings.Join(mutableFields, ", "))
			}
			out[f] = true
		}
		return out, nil
	}
	return map[string]bool{
		"type":       r.Type != "",
		"status":     r.Status != "",
		"refs":       len(r.Refs) > 0,
		"references": len(r.References) > 0,
		"body":       strings.TrimSpace(r.Body) != "",
		"excerpt":    strings.TrimSpace(r.Excerpt) != "",
	}, nil
}

// mergeBase reads the record an update merges into and reports whether the name is taken.
// An unparseable entry is fatal to a partial update and harmless to one naming every field,
// which is what keeps the console able to repair an entry nothing here can read.
func mergeBase(path string, fields map[string]bool) (Record, bool, error) {
	_, statErr := os.Stat(path)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		return Record{}, false, nil
	case statErr != nil:
		return Record{}, false, fmt.Errorf("memory: %s: %w", filepath.Base(path), statErr)
	}
	prev, err := readRecordFile(path)
	if err == nil {
		return prev, true, nil
	}
	for _, f := range mutableFields {
		if !fields[f] {
			return Record{}, true, fmt.Errorf("memory: %s cannot be read, so an update has nothing to merge into: %w", filepath.Base(path), err)
		}
	}
	return Record{}, true, nil
}

func applyFields(prev, in Record, fields map[string]bool) (Record, error) {
	out := prev
	out.Name = in.Name
	if fields["type"] {
		if prev.Type != "" && in.Type != prev.Type {
			return Record{}, invalidf("memory: %q is a %s and an update cannot make it a %s: the type is the subject axis a listing reports, and it decides which fields the entry may carry. Delete it and create the entry you want.", in.Name, prev.Type, in.Type)
		}
		out.Type = in.Type
	}
	if fields["status"] {
		out.Status = in.Status
	}
	if fields["refs"] {
		out.Refs = in.Refs
	}
	if fields["references"] {
		out.References = in.References
	}
	if fields["body"] {
		out.Body = in.Body
	}
	if fields["excerpt"] {
		out.Excerpt = in.Excerpt
	}
	return out, nil
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
		// No "memory:" prefix here: every Validate message carries one, and the doubled
		// package name is the first thing a reader sees on a skipped entry.
		return Record{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
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

// writeAtomic writes data to path, atomically and durably.
//
// It delegates rather than reimplementing the temp-file-and-rename dance, because the
// obvious hand-rolled version gets two things wrong and both are silent. It does not fsync
// before the rename, so a crash can make the rename durable while the bytes are not -
// leaving a truncated file behind a comment promising that cannot happen. And
// os.CreateTemp creates 0600, which the rename carries through, so entries end up
// owner-only when the surrounding files are not.
func writeAtomic(path string, data []byte) error {
	if err := file.WriteFileAtomic(path, data, 0o644); err != nil {
		return fmt.Errorf("memory: write %s: %w", filepath.Base(path), err)
	}
	return nil
}
