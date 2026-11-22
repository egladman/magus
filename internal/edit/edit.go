package edit

import (
	"bytes"
	"cmp"
	"crypto/rand"
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
	"syscall"

	"github.com/egladman/magus/types"
)

// ErrRefused is returned by [Plan.Apply] for a plan with refusals. Nothing was written;
// [Plan.Refused] says why.
var ErrRefused = errors.New("edit: refused, nothing written")

// ErrRestoreFailed marks a [Plan.Apply] error after which some file could not be put
// back, so the tree holds part of the edit.
var ErrRestoreFailed = errors.New("edit: restore failed")

const (
	noSuchFile     = "no such file"
	notLocal       = "not a path inside the workspace"
	createdMode    = fs.FileMode(0o644)
	createdDirMode = fs.FileMode(0o755)
)

// Site replaces the bytes at [Start, End) of Path, which must be Old, with New.
type Site struct {
	Path       string
	Start, End types.EditPosition
	Old, New   string
}

// Request is every write one plan makes: sites in files that
// exist, and files that do not exist yet.
type Request struct {
	Sites   []Site
	Creates []Creation
}

// Option configures [Resolve].
type Option func(*options)

type options struct {
	grade  func(path string, before, after []byte) (rule, reason string)
	rename func(root *os.Root, oldpath, newpath string) error
}

// WithGrade refuses every file for which grade returns a reason.
// It sees each file's bytes before and after, so a grader can
// place the change. It runs only for files whose every site
// resolved. Before is nil for a created file.
func WithGrade(grade func(path string, before, after []byte) (rule, reason string)) Option {
	return func(o *options) { o.grade = grade }
}

// Plan is a set of sites resolved against the files they name, holding each file's bytes
// before and after. A plan with refusals cannot be applied.
type Plan struct {
	opts    options
	root    string
	files   []*fileEdit
	refused []types.EditRefusal
}

type fileEdit struct {
	path          string // workspace-relative, slash-separated
	mode          fs.FileMode
	before, after []byte
	digestBefore  string
	digestAfter   string
	spans         []types.EditSpan
	created       bool
	// makesDirs are the missing parents of a created file.
	makesDirs []string
}

// span is a resolved site as byte offsets into the file's original bytes, end exclusive.
type span struct {
	start, end int
	at, until  types.EditPosition
	repl       string
}

// Resolve reads every file req names under root and resolves
// every site and creation. It collects every refusal rather
// than stopping at the first, and writes nothing. An empty
// request is an applicable plan that writes nothing.
func Resolve(root string, req Request, opts ...Option) *Plan {
	p := &Plan{root: root}
	for _, opt := range opts {
		opt(&p.opts)
	}
	byPath := map[string][]Site{}
	for _, s := range req.Sites {
		rel, ok := localPath(s.Path)
		if !ok {
			p.refuse(types.EditRefusal{Path: s.Path, Reason: notLocal})
			continue
		}
		byPath[rel] = append(byPath[rel], s)
	}
	creates := map[string]string{}
	for _, c := range req.Creates {
		rel, ok := localPath(c.Path)
		_, edited := byPath[rel]
		_, twice := creates[rel]
		switch {
		case !ok:
			p.refuse(types.EditRefusal{Path: c.Path, Reason: notLocal})
		case edited:
			p.refuse(types.EditRefusal{Path: rel, Reason: "both created and edited; create it with its final content"})
		case twice:
			p.refuse(types.EditRefusal{Path: rel, Reason: "created twice; keep one creation"})
		default:
			creates[rel] = c.Content
		}
	}
	for _, rel := range slices.Sorted(maps.Keys(byPath)) {
		if f := p.readFile(rel, byPath[rel]); f != nil {
			p.files = append(p.files, f)
		}
	}
	for _, rel := range slices.Sorted(maps.Keys(creates)) {
		if f := p.createFile(rel, creates[rel]); f != nil {
			p.files = append(p.files, f)
		}
	}
	slices.SortFunc(p.files, func(a, b *fileEdit) int { return cmp.Compare(a.path, b.path) })
	dirs := p.MakesDirs()
	for _, f := range p.files {
		if f.created && slices.Contains(dirs, f.path) {
			p.refuse(types.EditRefusal{Path: f.path, Reason: "created as a file and as the parent of another creation"})
		}
	}
	return p
}

func (p *Plan) refuse(r types.EditRefusal) { p.refused = append(p.refused, r) }

func (p *Plan) abs(rel string) string { return filepath.Join(p.root, filepath.FromSlash(rel)) }

// localPath cleans p to slash form, or reports that it leaves the
// workspace.
func localPath(p string) (string, bool) {
	rel := path.Clean(filepath.ToSlash(p))
	return rel, p != "" && filepath.IsLocal(filepath.FromSlash(rel))
}

// readRegular reads rel under root, or says why a rename could
// not safely replace it.
func readRegular(root, rel string) ([]byte, fs.FileMode, string) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, 0, err.Error()
	}
	defer r.Close()
	info, err := r.Lstat(filepath.FromSlash(rel))
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		// a.go/b under a regular a.go is as absent as any missing path.
		return nil, 0, noSuchFile
	case err != nil:
		return nil, 0, err.Error()
	case !info.Mode().IsRegular():
		// A rename over a symlink replaces the link, not the file it names.
		return nil, 0, fmt.Sprintf("not a regular file (%s)", info.Mode().Type().String())
	}
	body, err := r.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		return nil, 0, err.Error()
	}
	return body, info.Mode().Perm(), ""
}

// readFile resolves every site of one file, or refuses and returns nil.
func (p *Plan) readFile(rel string, sites []Site) *fileEdit {
	before, mode, reason := readRegular(p.root, rel)
	if reason != "" {
		p.refuse(types.EditRefusal{Path: rel, Reason: reason})
		return nil
	}

	ok := true
	spans := make([]span, 0, len(sites))
	lines := indexLines(before)
	for _, s := range sites {
		start, end, reason := lines.byteRange(before, s)
		if reason != "" {
			p.refuse(types.EditRefusal{Path: rel, Line: s.Start.Line, Column: s.Start.Column, Reason: reason})
			ok = false
			continue
		}
		spans = append(spans, span{start: start, end: end, at: s.Start, until: s.End, repl: s.New})
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

	f := &fileEdit{path: rel, mode: mode, before: before, digestBefore: digest(before)}
	f.after = compose(before, spans)
	f.digestAfter = digest(f.after)
	for _, s := range spans {
		f.spans = append(f.spans, types.EditSpan{Path: rel, Start: s.at, End: s.until})
	}
	return p.graded(f)
}

// createFile plans a new file and the parents it needs, or
// refuses and returns nil.
func (p *Plan) createFile(rel, content string) *fileEdit {
	refuseFile := func(reason string) *fileEdit {
		p.refuse(types.EditRefusal{Path: rel, Reason: reason})
		return nil
	}
	var missing []string
	root, err := os.OpenRoot(p.root)
	if err != nil {
		return refuseFile(err.Error())
	}
	defer root.Close()
	if parent := path.Dir(rel); parent != "." {
		parts := strings.Split(parent, "/")
		for i := range parts {
			dir := strings.Join(parts[:i+1], "/")
			info, err := root.Stat(filepath.FromSlash(dir))
			switch {
			case errors.Is(err, fs.ErrNotExist):
				missing = append(missing, dir)
			case err != nil:
				if !insideRoot(p.root, p.abs(dir)) {
					return refuseFile("resolves outside the workspace through a symlinked directory")
				}
				return refuseFile(err.Error())
			case !info.IsDir():
				return refuseFile(dir + " is not a directory")
			}
		}
	}
	if _, err := root.Lstat(filepath.FromSlash(rel)); err == nil {
		return refuseFile("already exists; change it with old and new")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return refuseFile(err.Error())
	}
	after := []byte(content)
	return p.graded(&fileEdit{
		path: rel, mode: createdMode, after: after, digestAfter: digest(after),
		created: true, makesDirs: missing,
	})
}

// graded returns f, or nil when the grader refuses it.
func (p *Plan) graded(f *fileEdit) *fileEdit {
	if p.opts.grade != nil {
		if rule, reason := p.opts.grade(f.path, f.before, f.after); reason != "" {
			p.refuse(types.EditRefusal{Path: f.path, Rule: rule, Reason: reason})
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
func (lines lineOffsets) byteRange(content []byte, s Site) (int, int, string) {
	start, ok := lines.offsetAt(content, s.Start)
	if !ok {
		return 0, 0, fmt.Sprintf("%d:%d is not a position in the file", s.Start.Line, s.Start.Column)
	}
	end, ok := lines.offsetAt(content, s.End)
	if !ok || end < start {
		return 0, 0, fmt.Sprintf("%d:%d is not a position in the file after the start", s.End.Line, s.End.Column)
	}
	if got := string(content[start:end]); got != s.Old {
		return 0, 0, fmt.Sprintf("holds %q, not %q: the file changed since it was read", got, s.Old)
	}
	return start, end, ""
}

// lineOffsets includes the empty line after a final newline.
type lineOffsets []int

func indexLines(content []byte) lineOffsets {
	lines := make(lineOffsets, 1, bytes.Count(content, []byte{'\n'})+1)
	for offset, c := range content {
		if c == '\n' {
			lines = append(lines, offset+1)
		}
	}
	return lines
}

// offsetAt is the byte offset of a 1-based line and column. A column may sit one past the
// line's last byte, where an exclusive end lands, but never past its terminator.
func (lines lineOffsets) offsetAt(content []byte, at types.EditPosition) (int, bool) {
	if at.Line < 1 || at.Line > len(lines) || at.Column < 1 {
		return 0, false
	}
	lineStart := lines[at.Line-1]
	lineEnd := len(content)
	if at.Line < len(lines) {
		lineEnd = lines[at.Line] - 1
	}
	if at.Column-1 > lineEnd-lineStart {
		return 0, false
	}
	return lineStart + (at.Column - 1), true
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

// Files are the files the plan writes, sorted by path.
// DigestBefore is empty for a created file. Empty for a refused
// plan.
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

// Digest is `sha256:` over each file's path, digest before and
// digest after, sorted by path. Two plans with one digest write
// the same bytes over the same bytes. Empty for a refused plan.
func (p *Plan) Digest() string {
	if len(p.refused) > 0 {
		return ""
	}
	h := sha256.New()
	for _, f := range p.files {
		fmt.Fprintf(h, "%s\x00%s\x00%s\n", f.path, f.digestBefore, f.digestAfter)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// MakesDirs are the workspace-relative directories [Plan.Apply]
// creates for new files, sorted, so a parent comes before its
// children. Empty for a refused plan.
func (p *Plan) MakesDirs() []string {
	if len(p.refused) > 0 {
		return nil
	}
	var dirs []string
	for _, f := range p.files {
		dirs = append(dirs, f.makesDirs...)
	}
	slices.Sort(dirs)
	return slices.Compact(dirs)
}

// Apply stages every file before publishing them individually. On failure
// it attempts to restore preceding writes. Readers may see intermediate states.
func (p *Plan) Apply() error {
	if len(p.refused) > 0 {
		return ErrRefused
	}
	var made []string
	if len(p.files) == 0 {
		return nil
	}
	root, err := os.OpenRoot(p.root)
	if err != nil {
		return err
	}
	defer root.Close()
	rename := p.opts.rename
	if rename == nil {
		rename = (*os.Root).Rename
	}
	staged := make([]string, len(p.files))
	discard := func() error {
		var errs []error
		for _, s := range staged {
			if s != "" {
				errs = append(errs, root.Remove(s))
			}
		}
		return errors.Join(append(errs, removeDirs(root, made))...)
	}
	for _, dir := range p.MakesDirs() {
		err := root.Mkdir(dir, createdDirMode)
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return errors.Join(fmt.Errorf("make %s, nothing written: %w", dir, err), discard())
		}
		if err == nil {
			made = append(made, dir)
		}
	}
	for i, f := range p.files {
		s, err := stage(root, f.path, f.after, f.mode)
		if err != nil {
			return errors.Join(fmt.Errorf("stage %s, nothing written: %w", f.path, err), discard())
		}
		staged[i] = s
	}
	for i, f := range p.files {
		err := checkUnchanged(root, f)
		if err == nil {
			if f.created {
				err = root.Link(staged[i], f.path)
			} else {
				err = rename(root, staged[i], f.path)
			}
		}
		if err != nil {
			failed := fmt.Errorf("edit: write %s: %w", f.path, err)
			rerr := errors.Join(restore(root, rename, p.files[:i]), discard())
			if rerr != nil {
				return errors.Join(failed, fmt.Errorf("%w: %w", ErrRestoreFailed, rerr))
			}
			return fmt.Errorf("every file written before it was restored: %w", failed)
		}
		if !f.created {
			staged[i] = ""
		}
	}
	made = nil
	return discard()
}

// removeDirs removes the directories Apply made, innermost first.
func removeDirs(root *os.Root, made []string) error {
	var errs []error
	for _, d := range slices.Backward(made) {
		if err := root.Remove(d); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// checkUnchanged refuses a file another writer changed between Resolve and its rename,
// which the rename would otherwise silently discard.
func checkUnchanged(root *os.Root, f *fileEdit) error {
	if f.created {
		if _, err := root.Lstat(f.path); !errors.Is(err, fs.ErrNotExist) {
			return errors.New("created on disk since it was planned")
		}
		return nil
	}
	now, err := root.ReadFile(f.path)
	if err != nil {
		return err
	}
	if d := digest(now); d != f.digestBefore {
		return fmt.Errorf("changed on disk since it was read (%s, was %s)", d, f.digestBefore)
	}
	return nil
}

func restore(root *os.Root, rename func(*os.Root, string, string) error, files []*fileEdit) error {
	var errs []error
	for _, f := range files {
		if f.created {
			if err := root.Remove(f.path); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", f.path, err))
			}
			continue
		}
		s, err := stage(root, f.path, f.before, f.mode)
		if err == nil {
			err = rename(root, s, f.path)
			if err != nil {
				_ = root.Remove(s)
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
func stage(root *os.Root, rel string, content []byte, mode fs.FileMode) (string, error) {
	name := path.Join(path.Dir(rel), "."+path.Base(rel)+".magus-edit-"+rand.Text())
	tmp, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
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
		_ = root.Remove(name)
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
		fmt.Fprintf(&b, " [%s]", r.Rule)
	}
	return b.String()
}
