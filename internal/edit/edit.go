// Package edit applies a [types.EditSet]. Every site is resolved and checked against
// the file on disk before the first byte moves, and then every file is written or none
// is. No VCS command runs on the apply path: atomicity comes from staging each file as a
// temp sibling, renaming the stages over the originals, and renaming the held originals
// back if any rename fails. The receipt's undo set is the durable inverse.
package edit

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// ErrRefused is returned by [Plan.Apply] for a plan with refusals. Nothing was written;
// [Plan.Refused] says why, per site.
var ErrRefused = errors.New("edit: refused, nothing written")

// ErrRestoreFailed marks a [Plan.Apply] error after which some file could not be put
// back, so the tree holds part of the set. The recorded receipt is the way back.
var ErrRestoreFailed = errors.New("edit: restore failed")

// maxSetBytes bounds a decoded set, so a caller that piped the wrong file is refused
// before it is read whole.
const maxSetBytes = 64 << 20

// DecodeSet reads one set, refusing an unknown member and a schema_version this magus
// does not read. It checks each site's shape; the files are not opened.
func DecodeSet(r io.Reader) (types.EditSet, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxSetBytes+1))
	if err != nil {
		return types.EditSet{}, fmt.Errorf("edit: read the set: %w", err)
	}
	if len(raw) > maxSetBytes {
		return types.EditSet{}, fmt.Errorf("edit: the set is larger than %d bytes", maxSetBytes)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return types.EditSet{}, errors.New("edit: the set is empty")
	}
	// Non-strict first, so a newer sender is told about the version rather than about
	// the field it added.
	var envelope struct {
		Version int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return types.EditSet{}, fmt.Errorf("edit: the set is not JSON: %w", err)
	}
	switch v := envelope.Version; {
	case v <= 0:
		return types.EditSet{}, fmt.Errorf("edit: the set carries no schema_version; this magus reads 1 through %d", types.EditSetSchemaVersion)
	case v > types.EditSetSchemaVersion:
		return types.EditSet{}, fmt.Errorf("edit: the set is schema_version %d and this magus reads 1 through %d; update magus", v, types.EditSetSchemaVersion)
	}
	var set types.EditSet
	if err := json.UnmarshalStrict(raw, &set); err != nil {
		return types.EditSet{}, fmt.Errorf("edit: the input is not a version %d set: %w", types.EditSetSchemaVersion, err)
	}
	if len(set.Edits) == 0 {
		return types.EditSet{}, errors.New("edit: the set has no edits")
	}
	var errs []error
	for i, e := range set.Edits {
		if err := e.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("edit %d: %w", i, err))
		}
	}
	return set, errors.Join(errs...)
}

// Schema is the JSON Schema of [types.EditSet], derived from the struct so the two
// cannot disagree.
func Schema() (string, error) {
	s, err := jsonschema.For[types.EditSet](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{
			reflect.TypeFor[types.EditAnchor](): {Type: "string", Enum: anyOf(types.EditAnchor("").Values())},
		},
	})
	if err != nil {
		return "", fmt.Errorf("edit: schema: %w", err)
	}
	s.Title = "magus edit set"
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("edit: schema: %w", err)
	}
	return string(out) + "\n", nil
}

func anyOf(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// Option configures [Resolve].
type Option func(*options)

type options struct {
	refuse     func(path string) string
	checkpoint func() string
	rename     func(oldpath, newpath string) error
	now        func() time.Time
}

// WithRefusal refuses every site in a path for which refuse returns a reason. The CLI
// passes the workspace's declared outputs, which are regenerated, never edited.
func WithRefusal(refuse func(path string) string) Option {
	return func(o *options) { o.refuse = refuse }
}

// WithCheckpoint stamps the receipt with checkpoint's answer before and after the write.
// An empty answer is recorded as none.
func WithCheckpoint(checkpoint func() string) Option {
	return func(o *options) { o.checkpoint = checkpoint }
}

// Plan is a set resolved against the files it names, holding each file's bytes before
// and after. A plan with refusals cannot be applied.
type Plan struct {
	opts    options
	files   []*fileEdit
	spans   []types.EditSpan
	refused []types.EditRefusal
}

type fileEdit struct {
	path          string // workspace-relative, slash-separated
	abs           string
	mode          fs.FileMode
	before, after []byte
	digestBefore  string
	digestAfter   string
	undo          []types.EditSite
}

// span is a resolved site as byte offsets into the file's original bytes, end exclusive.
type span struct {
	edit       int
	start, end int
	repl       string
}

// Resolve reads every file set names under root and resolves every site, collecting
// every refusal rather than stopping at the first. It writes nothing.
func Resolve(root string, set types.EditSet, opts ...Option) *Plan {
	p := &Plan{opts: options{rename: os.Rename, now: time.Now}}
	for _, opt := range opts {
		opt(&p.opts)
	}
	if len(set.Edits) == 0 {
		p.refuse(-1, "", "the set has no edits")
	}

	byPath := map[string][]int{}
	for i, e := range set.Edits {
		if err := e.Validate(); err != nil {
			p.refuse(i, e.Path, err.Error())
			continue
		}
		rel := path.Clean(filepath.ToSlash(e.Path))
		if !filepath.IsLocal(filepath.FromSlash(rel)) {
			p.refuse(i, e.Path, "not a path inside the workspace")
			continue
		}
		byPath[rel] = append(byPath[rel], i)
	}

	for _, rel := range slices.Sorted(maps.Keys(byPath)) {
		if f := p.readFile(root, rel, byPath[rel], set.Edits); f != nil {
			p.files = append(p.files, f)
		}
	}
	return p
}

func (p *Plan) refuse(edit int, path, reason string) {
	p.refused = append(p.refused, types.EditRefusal{Edit: edit, Path: path, Reason: reason})
}

// readFile resolves every site of one file, or refuses them and returns nil.
func (p *Plan) readFile(root, rel string, idx []int, edits []types.EditSite) *fileEdit {
	refuseAll := func(reason string) *fileEdit {
		for _, i := range idx {
			p.refuse(i, rel, reason)
		}
		return nil
	}
	if p.opts.refuse != nil {
		if reason := p.opts.refuse(rel); reason != "" {
			return refuseAll(reason)
		}
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return refuseAll("no such file")
	case err != nil:
		return refuseAll(err.Error())
	case !info.Mode().IsRegular():
		// A rename over a symlink replaces the link, not the file it names.
		return refuseAll("not a regular file (" + info.Mode().Type().String() + ")")
	}
	if !insideRoot(root, abs) {
		return refuseAll("resolves outside the workspace through a symlinked directory")
	}
	before, err := os.ReadFile(abs)
	if err != nil {
		return refuseAll(err.Error())
	}
	f := &fileEdit{path: rel, abs: abs, mode: info.Mode().Perm(), before: before, digestBefore: Digest(before)}

	var spans []span
	ok := true
	for _, i := range idx {
		e := edits[i]
		if e.Digest != "" && e.Digest != f.digestBefore {
			p.refuse(i, rel, fmt.Sprintf("the file changed since it was read: digest %s, the site expects %s", f.digestBefore, e.Digest))
			ok = false
			continue
		}
		got, reason := resolveSite(before, i, e)
		if reason != "" {
			p.refuse(i, rel, reason)
			ok = false
			continue
		}
		spans = append(spans, got...)
	}
	if !ok {
		return nil
	}
	slices.SortFunc(spans, func(a, b span) int {
		return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(a.end, b.end))
	})
	for k := 1; k < len(spans); k++ {
		// Two spans starting together overlap even when one is empty: which lands first
		// is not something the set says.
		if prev, cur := spans[k-1], spans[k]; cur.start < prev.end || cur.start == prev.start {
			at := positionAt(before, cur.start)
			p.refuse(cur.edit, rel, fmt.Sprintf("overlaps edit %d at %d:%d", prev.edit, at.Line, at.Col))
			ok = false
		}
	}
	if !ok {
		return nil
	}
	for _, s := range spans {
		p.spans = append(p.spans, types.EditSpan{Edit: s.edit, Path: rel, Start: positionAt(before, s.start), End: positionAt(before, s.end)})
	}
	f.after, f.undo = compose(before, spans)
	f.digestAfter = Digest(f.after)
	for i := range f.undo {
		f.undo[i].Path = rel
		f.undo[i].Digest = f.digestAfter
	}
	return f
}

func insideRoot(root, abs string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realRoot, real)
	return err == nil && filepath.IsLocal(rel)
}

// resolveSite finds a site's spans in content, or says why it cannot.
func resolveSite(content []byte, i int, e types.EditSite) ([]span, string) {
	switch e.Anchor {
	case types.EditAnchorLines:
		start, end, reason := lineSpan(content, e.Lines[0], e.Lines[1])
		if reason != "" {
			return nil, reason
		}
		if e.Old != "" && e.Old != string(content[start:end]) {
			return nil, fmt.Sprintf("old does not match the bytes at lines [%d, %d]", e.Lines[0], e.Lines[1])
		}
		return []span{{edit: i, start: start, end: end, repl: e.New}}, ""
	case types.EditAnchorText:
		old := []byte(e.Old)
		var spans []span
		for off := 0; ; {
			at := bytes.Index(content[off:], old)
			if at < 0 {
				break
			}
			start := off + at
			spans = append(spans, span{edit: i, start: start, end: start + len(old), repl: e.New})
			off = start + len(old)
		}
		switch {
		case len(spans) == 0:
			return nil, "old does not occur in the file"
		case len(spans) > 1 && !e.All:
			return nil, fmt.Sprintf("old occurs %d times; widen it to one occurrence, or set all", len(spans))
		}
		return spans, ""
	}
	return nil, fmt.Sprintf("anchor %q is not one this magus resolves", e.Anchor)
}

// lineStarts is the offset of every line start, plus len(content) when content ends
// with a terminator: the insertion point after the last line.
func lineStarts(content []byte) []int {
	starts := []int{0}
	for i, b := range content {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func lineCount(content []byte) int {
	n := bytes.Count(content, []byte{'\n'})
	if len(content) > 0 && content[len(content)-1] != '\n' {
		n++
	}
	return n
}

// lineSpan resolves [start, end] to byte offsets. end = start-1 is the empty span at the
// start of line start.
func lineSpan(content []byte, start, end int) (int, int, string) {
	starts := lineStarts(content)
	n := lineCount(content)
	if start > len(starts) || end > n {
		return 0, 0, fmt.Sprintf("lines [%d, %d] is past the end of the file, which has %d lines", start, end, n)
	}
	from := starts[start-1]
	switch {
	case end == start-1:
		return from, from, ""
	case end < len(starts):
		return from, starts[end], ""
	default:
		return from, len(content), ""
	}
}

// positionAt is the 1-based line and byte column of offset.
func positionAt(content []byte, offset int) types.EditPosition {
	lineStart := bytes.LastIndexByte(content[:offset], '\n') + 1
	return types.EditPosition{Line: bytes.Count(content[:offset], []byte{'\n'}) + 1, Col: offset - lineStart + 1}
}

// compose writes spans (sorted, disjoint) into before, and returns the result with the
// lines-anchored sites that turn it back into before. Each undo site covers whole lines
// of the result; sites whose lines touch share one, so the undo set never overlaps.
func compose(before []byte, spans []span) ([]byte, []types.EditSite) {
	var after bytes.Buffer
	type mapped struct{ from, to, delta int }
	placed := make([]mapped, len(spans))
	prev, delta := 0, 0
	for k, s := range spans {
		after.Write(before[prev:s.start])
		from := after.Len()
		after.WriteString(s.repl)
		prev = s.end
		delta += len(s.repl) - (s.end - s.start)
		placed[k] = mapped{from: from, to: after.Len(), delta: delta}
	}
	after.Write(before[prev:])
	out := after.Bytes()

	var undo []types.EditSite
	var groupFrom, groupTo, deltaBefore int
	flush := func(deltaAfter int) {
		startLine := bytes.Count(out[:groupFrom], []byte{'\n'}) + 1
		endLine := startLine - 1 + lineCount(out[groupFrom:groupTo])
		undo = append(undo, types.EditSite{
			Anchor: types.EditAnchorLines,
			Lines:  []int{startLine, endLine},
			Old:    string(out[groupFrom:groupTo]),
			New:    string(before[groupFrom-deltaBefore : groupTo-deltaAfter]),
		})
	}
	for k, m := range placed {
		from, to := wholeLines(out, m.from, m.to)
		spanDeltaBefore := 0
		if k > 0 {
			spanDeltaBefore = placed[k-1].delta
		}
		switch {
		case k == 0:
			groupFrom, groupTo, deltaBefore = from, to, 0
		case from <= groupTo:
			groupTo = max(groupTo, to)
		default:
			flush(spanDeltaBefore)
			groupFrom, groupTo, deltaBefore = from, to, spanDeltaBefore
		}
	}
	if len(placed) > 0 {
		flush(placed[len(placed)-1].delta)
	}
	return out, undo
}

// wholeLines widens [from, to) to the lines it touches. An empty span at a line start
// stays empty: it is an insertion point, and widening it would claim a line it never
// touched.
func wholeLines(content []byte, from, to int) (int, int) {
	lineFrom := bytes.LastIndexByte(content[:from], '\n') + 1
	if to == from && from == lineFrom {
		return from, from
	}
	last := max(to-1, from)
	if last >= len(content) {
		return lineFrom, len(content)
	}
	if nl := bytes.IndexByte(content[last:], '\n'); nl >= 0 {
		return lineFrom, last + nl + 1
	}
	return lineFrom, len(content)
}

// Digest is the `sha256:<hex>` of content, the form a site's digest precondition takes.
func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Refused lists every reason the plan cannot be applied, in the order found.
func (p *Plan) Refused() []types.EditRefusal { return slices.Clone(p.refused) }

// Receipt is what the plan would do, written nowhere: the files with their digests
// before and after, and where every site resolved. A refused plan carries only its
// refusals.
func (p *Plan) Receipt() types.EditReceipt {
	r := types.EditReceipt{SchemaVersion: types.EditSetSchemaVersion}
	if len(p.refused) > 0 {
		r.Refused = p.Refused()
		return r
	}
	for _, f := range p.files {
		r.Files = append(r.Files, types.EditedFile{Path: f.path, DigestBefore: f.digestBefore, DigestAfter: f.digestAfter})
	}
	r.Spans = slices.Clone(p.spans)
	return r
}

// Apply writes every file of the plan or none of them. It stages every file as a temp
// sibling first, then hands record the full receipt (its undo set included) before the
// first rename, so a receipt exists for anything that lands; a record error aborts with
// nothing written. A failed rename renames the held originals back over every file
// already replaced, and the error says whether that restore held.
func (p *Plan) Apply(record func(types.EditReceipt) error) (types.EditReceipt, error) {
	if len(p.refused) > 0 {
		return p.Receipt(), ErrRefused
	}
	r := p.Receipt()
	r.Applied = true
	r.AppliedAt = p.opts.now().UTC()
	r.ID = newReceiptID(r.AppliedAt)
	if p.opts.checkpoint != nil {
		r.CheckpointBefore = p.opts.checkpoint()
	}
	undo := &types.EditSet{SchemaVersion: types.EditSetSchemaVersion}
	for _, f := range p.files {
		undo.Edits = append(undo.Edits, f.undo...)
	}
	r.Undo = undo

	staged := make([]string, len(p.files))
	discard := func() {
		for _, s := range staged {
			if s != "" {
				_ = os.Remove(s)
			}
		}
	}
	for i, f := range p.files {
		s, err := stage(f.abs, f.after, f.mode)
		if err != nil {
			discard()
			return p.Receipt(), fmt.Errorf("edit: stage %s: %w, nothing written", f.path, err)
		}
		staged[i] = s
	}
	if record != nil {
		if err := record(r); err != nil {
			discard()
			return p.Receipt(), fmt.Errorf("edit: record the receipt: %w, nothing written", err)
		}
	}
	for i, f := range p.files {
		err := unchanged(f)
		if err == nil {
			err = p.opts.rename(staged[i], f.abs)
		}
		if err != nil {
			discard()
			failed := fmt.Errorf("edit: write %s: %w", f.path, err)
			if rerr := p.restore(p.files[:i]); rerr != nil {
				return p.Receipt(), errors.Join(failed, fmt.Errorf("%w: %w", ErrRestoreFailed, rerr))
			}
			return p.Receipt(), fmt.Errorf("%w; every file written before it was restored", failed)
		}
		staged[i] = ""
	}
	if p.opts.checkpoint != nil {
		r.CheckpointAfter = p.opts.checkpoint()
	}
	return r, nil
}

// unchanged refuses a file another writer changed between Resolve and its rename, which
// the rename would otherwise silently discard.
func unchanged(f *fileEdit) error {
	now, err := os.ReadFile(f.abs)
	if err != nil {
		return err
	}
	if d := Digest(now); d != f.digestBefore {
		return fmt.Errorf("changed on disk since it was read (%s, was %s)", d, f.digestBefore)
	}
	return nil
}

func (p *Plan) restore(files []*fileEdit) error {
	var errs []error
	for _, f := range files {
		s, err := stage(f.abs, f.before, f.mode)
		if err == nil {
			err = p.opts.rename(s, f.abs)
			if err != nil {
				_ = os.Remove(s)
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.path, err))
		}
	}
	return errors.Join(errs...)
}

// stage writes content to a synced temp sibling of abs, so the rename that lands it
// stays on one filesystem.
func stage(abs string, content []byte, mode fs.FileMode) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(abs), "."+filepath.Base(abs)+".magus-edit-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	_, err = tmp.Write(content)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// FormatRefusal renders a refusal as one line.
func FormatRefusal(r types.EditRefusal) string {
	var b strings.Builder
	if r.Edit >= 0 {
		fmt.Fprintf(&b, "edit %d ", r.Edit)
	}
	if r.Path != "" {
		b.WriteString(r.Path + ": ")
	}
	b.WriteString(r.Reason)
	return b.String()
}
