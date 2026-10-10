package edit

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// ErrRefused is returned by [Plan.Apply] for a plan with refusals. Nothing was written;
// [Plan.Refused] says why.
var ErrRefused = errors.New("edit: refused, nothing written")

// ErrRestoreFailed marks a [Plan.Apply] error after which some file could not be put
// back, so the tree holds part of the edit.
var ErrRestoreFailed = errors.New("edit: restore failed")

// Site replaces the bytes at [Start, End) of Path, which must be Old, with New.
type Site struct {
	Path       string
	Start, End types.EditPosition
	Old, New   string
}

// Option configures [Resolve].
type Option func(*options)

type options struct {
	grade  func(path string, before, after []byte) (rule, reason string)
	rename func(oldpath, newpath string) error
}

// WithGrade refuses every file for which grade returns a reason. It sees each file's bytes
// before and after, so a grader can place the change, and it runs only for files whose
// every site resolved.
func WithGrade(grade func(path string, before, after []byte) (rule, reason string)) Option {
	return func(o *options) { o.grade = grade }
}

// Plan is a set of sites resolved against the files they name, holding each file's bytes
// before and after. A plan with refusals cannot be applied.
type Plan struct {
	opts    options
	files   []*fileEdit
	refused []types.EditRefusal
}

type fileEdit struct {
	path          string // workspace-relative, slash-separated
	abs           string
	mode          fs.FileMode
	before, after []byte
	digestBefore  string
	digestAfter   string
	spans         []types.EditSpan
}

// span is a resolved site as byte offsets into the file's original bytes, end exclusive.
type span struct {
	start, end int
	at         types.EditPosition
	repl       string
}

// Resolve reads every file sites name under root and resolves every site, collecting every
// refusal rather than stopping at the first. It writes nothing.
func Resolve(root string, sites []Site, opts ...Option) *Plan {
	p := &Plan{opts: options{rename: os.Rename}}
	for _, opt := range opts {
		opt(&p.opts)
	}
	if len(sites) == 0 {
		p.refuse(types.EditRefusal{Reason: "nothing to edit"})
	}
	byPath := map[string][]Site{}
	for _, s := range sites {
		rel := path.Clean(filepath.ToSlash(s.Path))
		if s.Path == "" || !filepath.IsLocal(filepath.FromSlash(rel)) {
			p.refuse(types.EditRefusal{Path: s.Path, Reason: "not a path inside the workspace"})
			continue
		}
		byPath[rel] = append(byPath[rel], s)
	}
	for _, rel := range slices.Sorted(maps.Keys(byPath)) {
		if f := p.readFile(root, rel, byPath[rel]); f != nil {
			p.files = append(p.files, f)
		}
	}
	return p
}

func (p *Plan) refuse(r types.EditRefusal) { p.refused = append(p.refused, r) }

// readFile resolves every site of one file, or refuses and returns nil.
func (p *Plan) readFile(root, rel string, sites []Site) *fileEdit {
	refuseFile := func(reason string) *fileEdit {
		p.refuse(types.EditRefusal{Path: rel, Reason: reason})
		return nil
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return refuseFile("no such file")
	case err != nil:
		return refuseFile(err.Error())
	case !info.Mode().IsRegular():
		// A rename over a symlink replaces the link, not the file it names.
		return refuseFile("not a regular file (" + info.Mode().Type().String() + ")")
	}
	if !insideRoot(root, abs) {
		return refuseFile("resolves outside the workspace through a symlinked directory")
	}
	before, err := os.ReadFile(abs)
	if err != nil {
		return refuseFile(err.Error())
	}

	ok := true
	spans := make([]span, 0, len(sites))
	for _, s := range sites {
		start, end, reason := byteRange(before, s)
		if reason != "" {
			p.refuse(types.EditRefusal{Path: rel, Line: s.Start.Line, Column: s.Start.Column, Reason: reason})
			ok = false
			continue
		}
		spans = append(spans, span{start: start, end: end, at: s.Start, repl: s.New})
	}
	if !ok {
		return nil
	}
	slices.SortFunc(spans, func(a, b span) int {
		return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(a.end, b.end))
	})
	for k := 1; k < len(spans); k++ {
		// Two spans starting together overlap even when one is empty: which lands first is
		// not something the sites say.
		if prev, cur := spans[k-1], spans[k]; cur.start < prev.end || cur.start == prev.start {
			p.refuse(types.EditRefusal{Path: rel, Line: cur.at.Line, Column: cur.at.Column,
				Reason: fmt.Sprintf("overlaps the site at %d:%d", prev.at.Line, prev.at.Column)})
			ok = false
		}
	}
	if !ok {
		return nil
	}

	f := &fileEdit{path: rel, abs: abs, mode: info.Mode().Perm(), before: before, digestBefore: digest(before)}
	f.after = compose(before, spans)
	f.digestAfter = digest(f.after)
	for _, s := range spans {
		f.spans = append(f.spans, types.EditSpan{Path: rel, Start: positionAt(before, s.start), End: positionAt(before, s.end)})
	}
	if p.opts.grade != nil {
		if rule, reason := p.opts.grade(rel, f.before, f.after); reason != "" {
			p.refuse(types.EditRefusal{Path: rel, Rule: rule, Reason: reason})
			return nil
		}
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

// byteRange resolves a site's positions to offsets into content and checks that they hold
// Old, or says why not.
func byteRange(content []byte, s Site) (int, int, string) {
	start, ok := offsetAt(content, s.Start)
	if !ok {
		return 0, 0, fmt.Sprintf("%d:%d is not a position in the file", s.Start.Line, s.Start.Column)
	}
	end, ok := offsetAt(content, s.End)
	if !ok || end < start {
		return 0, 0, fmt.Sprintf("%d:%d is not a position in the file after the start", s.End.Line, s.End.Column)
	}
	if got := string(content[start:end]); got != s.Old {
		return 0, 0, fmt.Sprintf("holds %q, not %q: the file changed since it was read", got, s.Old)
	}
	return start, end, ""
}

// offsetAt is the byte offset of a 1-based line and column. A column may sit one past the
// line's last byte, where an exclusive end lands, but never past its terminator.
func offsetAt(content []byte, at types.EditPosition) (int, bool) {
	if at.Line < 1 || at.Column < 1 {
		return 0, false
	}
	lineStart := 0
	for range at.Line - 1 {
		nl := bytes.IndexByte(content[lineStart:], '\n')
		if nl < 0 {
			return 0, false
		}
		lineStart += nl + 1
	}
	lineEnd := len(content)
	if nl := bytes.IndexByte(content[lineStart:], '\n'); nl >= 0 {
		lineEnd = lineStart + nl
	}
	off := lineStart + at.Column - 1
	return off, off <= lineEnd
}

// positionAt is the 1-based line and byte column of offset.
func positionAt(content []byte, offset int) types.EditPosition {
	lineStart := bytes.LastIndexByte(content[:offset], '\n') + 1
	return types.EditPosition{Line: bytes.Count(content[:offset], []byte{'\n'}) + 1, Column: offset - lineStart + 1}
}

// compose writes spans, sorted and disjoint, into before.
func compose(before []byte, spans []span) []byte {
	var after bytes.Buffer
	prev := 0
	for _, s := range spans {
		after.Write(before[prev:s.start])
		after.WriteString(s.repl)
		prev = s.end
	}
	after.Write(before[prev:])
	return after.Bytes()
}

// digest is the `sha256:<hex>` of content.
func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Refused lists every reason the plan cannot be applied, in the order found.
func (p *Plan) Refused() []types.EditRefusal { return slices.Clone(p.refused) }

// Files are the files the plan rewrites, sorted by path. Empty for a refused plan.
func (p *Plan) Files() []types.EditedFile {
	if len(p.refused) > 0 {
		return nil
	}
	out := make([]types.EditedFile, 0, len(p.files))
	for _, f := range p.files {
		out = append(out, types.EditedFile{Path: f.path, DigestBefore: f.digestBefore, DigestAfter: f.digestAfter})
	}
	return out
}

// Spans is where every site resolved, by file then position. Empty for a refused plan.
func (p *Plan) Spans() []types.EditSpan {
	if len(p.refused) > 0 {
		return nil
	}
	var out []types.EditSpan
	for _, f := range p.files {
		out = append(out, f.spans...)
	}
	return out
}

// Apply writes every file of the plan or none of them, and runs no VCS command. It stages
// every file as a temp sibling and renames each over its original; when a rename fails, it
// re-stages the original bytes of every file already replaced and renames them back. The
// error says whether that restore held.
func (p *Plan) Apply() error {
	if len(p.refused) > 0 {
		return ErrRefused
	}
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
			return fmt.Errorf("edit: stage %s, nothing written: %w", f.path, err)
		}
		staged[i] = s
	}
	for i, f := range p.files {
		err := checkUnchanged(f)
		if err == nil {
			err = p.opts.rename(staged[i], f.abs)
		}
		if err != nil {
			discard()
			failed := fmt.Errorf("edit: write %s: %w", f.path, err)
			if rerr := p.restore(p.files[:i]); rerr != nil {
				return errors.Join(failed, fmt.Errorf("%w: %w", ErrRestoreFailed, rerr))
			}
			return fmt.Errorf("every file written before it was restored: %w", failed)
		}
		staged[i] = ""
	}
	return nil
}

// checkUnchanged refuses a file another writer changed between Resolve and its rename,
// which the rename would otherwise silently discard.
func checkUnchanged(f *fileEdit) error {
	now, err := os.ReadFile(f.abs)
	if err != nil {
		return err
	}
	if d := digest(now); d != f.digestBefore {
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

// stage writes content to a synced temp sibling of abs, so the rename that lands it stays
// on one filesystem.
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
	switch {
	case r.Path != "" && r.Line > 0:
		fmt.Fprintf(&b, "%s:%d:%d: ", r.Path, r.Line, r.Column)
	case r.Path != "":
		b.WriteString(r.Path + ": ")
	}
	b.WriteString(r.Reason)
	if r.Rule != "" {
		b.WriteString(" [" + r.Rule + "]")
	}
	return b.String()
}
