package changeset

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// This is the ONE unified-diff reader in the product, and a second one must not be written.
// A hunk's DIGEST is its identity: it keys a read receipt and lets a mark made in the console
// be seen by the CLI and by an agent. Two readers computing that independently is a shared
// session that can disagree with itself, silently and only for some hunks.
//
// It reads GIT's extended unified format, a superset of the POSIX one: the `diff --git` line
// and its `old mode` / `new file` / `rename from` / `index` headers carry facts a `---`/`+++`
// pair alone cannot express - a rename, a pure mode change, a binary file. hg and jj wrap the
// same unified body in their own header lines; an unrecognized header is SKIPPED rather than
// rejected, so those patches land on the same shape with the git-only extras left unset.

// Kind is what one row of a hunk is. Context appears on both sides; Add and Del on one.
// KindMeta is the "\ No newline at end of file" marker, which belongs to the hunk but is a
// line of neither file and must never be counted as one.
const (
	KindContext = "context"
	KindAdd     = "add"
	KindDel     = "del"
	KindMeta    = "meta"
)

// Status is what happened to a file as a whole: taken from the git extended headers where
// they are present, and otherwise inferred from the /dev/null convention in the ---/+++ pair.
const (
	StatusAdded    = "added"
	StatusDeleted  = "deleted"
	StatusModified = "modified"
	StatusRenamed  = "renamed"
	StatusCopied   = "copied"
)

// Row is one rendered line of a hunk.
//
// OldLine and NewLine are absolute numbers in the two files, nil where the line does not
// exist on that side. They are computed HERE rather than derived at render time because a
// virtualized view draws rows out of order and from arbitrary offsets, so a renderer counting
// as it painted would number them wrong the moment it skipped a row.
type Row struct {
	Kind string `json:"kind"`
	// Text is the content WITHOUT the leading +/-/space marker.
	Text    string `json:"text"`
	OldLine *int   `json:"old_line"`
	NewLine *int   `json:"new_line"`
	// Emph is which PART of this line changed, for a row paired with its counterpart across a
	// rewrite. Nil where there is nothing to mark, which is most rows.
	//
	// Offsets are UTF-16 code units, indexing Text - what a JavaScript string is indexed by,
	// because the browser is this field's consumer. A Go caller slicing bytes converts with
	// byteSpan.
	Emph *Span `json:"emph,omitempty"`
}

// Hunk is one @@ section: the header line, its body, and the content digest the viewed set
// is keyed by.
type Hunk struct {
	// Index is the 0-based position within the file, which is the coordinate the MCP surface
	// takes on a comment. It is carried explicitly rather than left implicit in the slice
	// position so a caller that filters hunks cannot silently renumber them.
	Index  int    `json:"index"`
	Header string `json:"header"`
	// Declaration is the enclosing declaration git named in Header: the text after the second
	// @@, which is "func (r Diff) AttachChurn(...)" or "type Diff struct {". Empty where git
	// named none - the top of a file, or a language it has no funcname pattern for.
	//
	// Parsed HERE rather than by each surface, for the reason the digest and the intra-line
	// emphasis are: two readers of one header is two chances to disagree about what a hunk is
	// called, and nothing would ever report the disagreement.
	Declaration string `json:"declaration,omitempty"`
	// Display is Lines with the characters a renderer obeys but a reader cannot see escaped, or
	// NIL when no line carried one - which is every ordinary hunk, so this costs nothing to ship
	// and nothing to hold.
	//
	// Computed here so the two surfaces cannot disagree about it. A sanitizer in the terminal and
	// not the browser would leave one of them rendering the deception, and the reader has no way
	// to tell which they are looking at.
	Display []string `json:"display,omitempty"`
	// Lines is the body EXACTLY as it arrived, markers included, because that is what Digest
	// hashes. Rows carries the same body parsed for rendering; the two are not interchangeable,
	// since a context line whose producer dropped the trailing space arrives as "" and would
	// come back from Rows as " ".
	Lines  []string `json:"lines"`
	Digest string   `json:"digest"`

	OldStart int   `json:"old_start"`
	OldCount int   `json:"old_count"`
	NewStart int   `json:"new_start"`
	NewCount int   `json:"new_count"`
	Rows     []Row `json:"rows"`
}

// File is one changed file, fully described.
type File struct {
	// Path is what the file is called NOW - the new path for a rename, the old path for a
	// deletion, where there is no new one. A sidebar lists it and an anchor names it, so it
	// is never "/dev/null".
	Path string `json:"path"`
	// OldPath differs from Path only for a rename or a copy.
	OldPath   string `json:"old_path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// Binary files carry no hunks. Rendering one as an empty diff reads as "nothing changed",
	// which is false, so the flag is explicit and the surface says so.
	Binary  bool   `json:"binary"`
	OldMode string `json:"old_mode,omitempty"`
	NewMode string `json:"new_mode,omitempty"`
	Hunks   []Hunk `json:"hunks"`
}

// hunkHeader captures the four numbers of an @@ header. The counts are OPTIONAL in the
// format: `@@ -1 +1 @@` means one line on each side, and reading an absent count as 0 rather
// than 1 silently drops every single-line hunk.
var hunkHeader = regexp.MustCompile(`^@@+ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// Parse reads a whole unified patch. An empty or whitespace-only patch yields nil rather than
// an error: a clean tree is a state, not a parse failure.
func Parse(patch string) []File {
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	p := &parser{}
	// Drop the ONE empty element a trailing newline leaves behind. It is an artifact of the
	// split, not a line of the patch, and the hunk reader treats a bare "" as an empty context
	// line - so leaving it appends a phantom row to whatever hunk ends the patch, shifting
	// every count and line number derived from it, and changing that hunk's digest. Exactly
	// one is removed, so a patch genuinely ending in a blank context line keeps it.
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if strings.HasPrefix(line, "diff --git ") {
			p.closeFile()
			old, now := gitHeaderPaths(line)
			p.openFile(old, now)
			p.gitNamed = true
			continue
		}

		// The ---/+++ pair is matched by LOOKAHEAD rather than by remembering the previous
		// line, because a lone `--- x` is also how a hunk body spells a removed line whose
		// text begins "-- ". Requiring the `+++` partner on the very next line is what every
		// unified-diff reader uses to tell those apart, and it keeps the `---` out of the open
		// hunk's text, where it would have changed that hunk's digest.
		if strings.HasPrefix(line, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ ") {
			oldSide := stripPathPrefix(strings.TrimPrefix(line, "--- "))
			newSide := stripPathPrefix(strings.TrimPrefix(lines[i+1], "+++ "))
			if p.gitNamed {
				// git names the file on its own header and then repeats it here. The pair adds
				// no path, but it still carries the /dev/null that says added or deleted.
				p.applyDevNull(oldSide, newSide)
				p.gitNamed = false
			} else {
				p.closeFile()
				p.openFile(oldSide, newSide)
				p.applyDevNull(oldSide, newSide)
			}
			i++ // the +++ line is consumed with its --- partner
			continue
		}

		if m := hunkHeader.FindStringSubmatch(line); m != nil && p.cur != nil {
			p.openHunk(line, m)
			continue
		}

		if p.hunk != nil && p.readBodyLine(line) {
			continue
		}
		if p.cur != nil {
			p.applyExtendedHeader(line)
		}
	}
	p.closeFile()
	return p.files
}

type parser struct {
	files []File
	cur   *File
	hunk  *Hunk
	// raw keeps the hunk's body lines EXACTLY as they arrived, markers included, because that
	// is what HunkDigest hashes. Rebuilding them from Row.Kind would not round-trip: a context
	// line whose producer dropped the trailing space arrives as "" and would come back as " ".
	raw []string
	// gitNamed records that a `diff --git` header opened the current file, so the redundant
	// ---/+++ pair that follows does not open a second, hunkless entry for it.
	gitNamed     bool
	oldNo, newNo int
}

func (p *parser) openFile(oldPath, newPath string) {
	p.cur = &File{Path: newPath, OldPath: oldPath, Status: StatusModified}
}

// applyDevNull reads the added/deleted fact out of the ---/+++ pair. A patch with no git
// extended headers - every POSIX and Mercurial one - carries it nowhere else.
func (p *parser) applyDevNull(oldSide, newSide string) {
	if p.cur == nil {
		return
	}
	switch {
	case oldSide == devNull && newSide != devNull:
		p.cur.Status = StatusAdded
	case newSide == devNull && oldSide != devNull:
		p.cur.Status = StatusDeleted
	}
}

func (p *parser) openHunk(line string, m []string) {
	p.closeHunk()
	atoi := func(s string, missing int) int {
		if s == "" {
			return missing
		}
		n, _ := strconv.Atoi(s)
		return n
	}
	oldStart, newStart := atoi(m[1], 0), atoi(m[3], 0)
	p.hunk = &Hunk{
		Index:       len(p.cur.Hunks),
		Header:      line,
		Declaration: DeclarationOf(line),
		OldStart:    oldStart,
		OldCount:    atoi(m[2], 1),
		NewStart:    newStart,
		NewCount:    atoi(m[4], 1),
	}
	p.raw = nil
	p.oldNo, p.newNo = oldStart, newStart
}

// readBodyLine consumes one line of an open hunk, reporting whether it belonged to it. A line
// that does not is a header for the next file and is re-read by the caller.
func (p *parser) readBodyLine(line string) bool {
	num := func(n *int) *int { v := *n; *n++; return &v }
	switch {
	case strings.HasPrefix(line, `\`):
		// "\ No newline at end of file" describes the PRECEDING line and consumes no line
		// number on either side.
		p.push(line, Row{Kind: KindMeta, Text: strings.TrimSpace(line[1:])})
	case strings.HasPrefix(line, "+"):
		p.push(line, Row{Kind: KindAdd, Text: line[1:], NewLine: num(&p.newNo)})
		p.cur.Additions++
	case strings.HasPrefix(line, "-"):
		p.push(line, Row{Kind: KindDel, Text: line[1:], OldLine: num(&p.oldNo)})
		p.cur.Deletions++
	case strings.HasPrefix(line, " "):
		p.push(line, Row{Kind: KindContext, Text: line[1:], OldLine: num(&p.oldNo), NewLine: num(&p.newNo)})
	case line == "":
		// A fully empty line inside a hunk is a context line whose content is empty: some
		// producers drop the trailing space. Treating it as a terminator instead would cut
		// every hunk short at its first blank line.
		p.push(line, Row{Kind: KindContext, Text: "", OldLine: num(&p.oldNo), NewLine: num(&p.newNo)})
	default:
		p.closeHunk()
		return false
	}
	return true
}

func (p *parser) push(rawLine string, r Row) {
	p.raw = append(p.raw, rawLine)
	p.hunk.Rows = append(p.hunk.Rows, r)
}

// applyExtendedHeader reads git's per-file header lines. An unrecognized line is ignored, so
// an hg or jj header, or a git extension added later, is skipped rather than fatal.
func (p *parser) applyExtendedHeader(line string) {
	f := p.cur
	cut := func(prefix string) string { return strings.TrimSpace(strings.TrimPrefix(line, prefix)) }
	switch {
	case strings.HasPrefix(line, "new file mode "):
		f.Status, f.NewMode = StatusAdded, cut("new file mode ")
	case strings.HasPrefix(line, "deleted file mode "):
		f.Status, f.OldMode = StatusDeleted, cut("deleted file mode ")
	case strings.HasPrefix(line, "old mode "):
		f.OldMode = cut("old mode ")
	case strings.HasPrefix(line, "new mode "):
		f.NewMode = cut("new mode ")
	case strings.HasPrefix(line, "rename from "):
		f.Status, f.OldPath = StatusRenamed, cut("rename from ")
	case strings.HasPrefix(line, "rename to "):
		f.Status, f.Path = StatusRenamed, cut("rename to ")
	case strings.HasPrefix(line, "copy from "):
		f.Status, f.OldPath = StatusCopied, cut("copy from ")
	case strings.HasPrefix(line, "copy to "):
		f.Status, f.Path = StatusCopied, cut("copy to ")
	case strings.HasPrefix(line, "Binary files "), strings.HasPrefix(line, "GIT binary patch"):
		f.Binary = true
	}
}

func (p *parser) closeHunk() {
	if p.cur == nil || p.hunk == nil {
		return
	}
	// The digest is taken over the file's identity as it stands now. Every path-bearing header
	// precedes the first @@, so Path is already settled by the time any hunk closes.
	p.hunk.Digest = HunkDigest(p.identity(), p.raw)
	p.hunk.Lines = p.raw
	// AFTER the digest, never before: the digest is the hunk's identity and must address the
	// file as it really is, so a line rewritten for safe display cannot become a different hunk.
	p.hunk.Display = displayLines(p.raw)
	markEmphasis(p.hunk.Rows)
	sanitizeRows(p.hunk.Rows)
	p.cur.Hunks = append(p.cur.Hunks, *p.hunk)
	p.hunk, p.raw = nil, nil
}

// markEmphasis fills in each changed row's intra-line span, once, for every surface to read.
//
// The pairing is a run of removed lines against the run of added lines that follows, matched
// positionally and only when the two runs are the same length. An unequal run means lines were
// added or removed rather than rewritten, and pairing across that boundary would invent a
// correspondence the patch does not contain.
func markEmphasis(rows []Row) {
	var dels, adds []int
	flush := func() {
		for _, pair := range pairForEmphasis(dels, adds) {
			before, after := emphasize(rows[pair.Del].Text, rows[pair.Add].Text)
			rows[pair.Del].Emph = utf16Span(rows[pair.Del].Text, before)
			rows[pair.Add].Emph = utf16Span(rows[pair.Add].Text, after)
		}
		dels, adds = nil, nil
	}
	for i, r := range rows {
		switch {
		case r.Kind == KindDel && len(adds) == 0:
			dels = append(dels, i)
		case r.Kind == KindAdd && len(dels) > 0:
			adds = append(adds, i)
		default:
			flush()
			if r.Kind == KindDel {
				dels = append(dels, i)
			}
		}
	}
	flush()
}

// RawLineEmphasis re-expresses a hunk's intra-line spans in the coordinates a terminal
// renderer slices: byte offsets into the RAW line, +/- marker included. Indexed by Rows, which
// the parser's push keeps parallel with Lines; an empty span means nothing to mark.
//
// Two conversions, and each is a coordinate the other consumer does not want. Rows carry
// UTF-16 offsets because the wire's reader is a browser, and they measure Text, which has no
// marker on it. Both happen here so neither surface has to hold an opinion about how a span
// travelled to it.
func RawLineEmphasis(h Hunk) []Span {
	if len(h.Rows) == 0 {
		return nil
	}
	out := make([]Span, len(h.Rows))
	for i, r := range h.Rows {
		if r.Emph == nil {
			continue
		}
		s := byteSpan(r.Text, *r.Emph)
		if s.Empty() {
			continue
		}
		out[i] = Span{Start: s.Start + 1, End: s.End + 1}
	}
	return out
}

// utf16Span converts a byte span into the UTF-16 code units a browser indexes strings by, or
// nil when there is nothing to mark.
//
// The conversion happens on THIS side because the wire's consumer is JavaScript, and because
// Go is the side holding both the bytes and the runes. A browser handed byte offsets would
// slice a line containing one accented character in the wrong place - and would do it only on
// the lines nobody thinks to test.
func utf16Span(text string, s Span) *Span {
	if s.Empty() {
		return nil
	}
	out := Span{Start: utf16Len(text[:s.Start]), End: utf16Len(text[:s.End])}
	return &out
}

// utf16Len counts the UTF-16 code units in s. Anything outside the BMP takes two, which is
// what a JavaScript string index counts and what a Go rune count would get wrong.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// identity settles which name the file goes by, which is what the sidebar lists, what an
// anchor names, and what a hunk digest is salted with. A deletion has no new path and a
// creation has no old one, so it falls back to whichever side exists.
func (p *parser) identity() string {
	if p.cur.Path != "" && p.cur.Path != devNull {
		return p.cur.Path
	}
	return p.cur.OldPath
}

func (p *parser) closeFile() {
	p.closeHunk()
	if p.cur == nil {
		return
	}
	p.cur.Path = p.identity()
	if p.cur.OldPath == "" || p.cur.OldPath == devNull {
		p.cur.OldPath = p.cur.Path
	}
	p.files = append(p.files, *p.cur)
	p.cur, p.gitNamed = nil, false
}

const devNull = "/dev/null"

// gitHeaderPaths splits `diff --git a/x b/x` into its two paths.
//
// It splits on " b/" rather than on whitespace, because a path may CONTAIN spaces and a naive
// split puts half a filename in each field. That still cannot disambiguate a path holding the
// literal " b/", which no format can without quoting - so the a//b prefixes are the tiebreak
// and the LAST occurrence wins, matching git's own reader.
func gitHeaderPaths(line string) (oldPath, newPath string) {
	rest := strings.TrimPrefix(line, "diff --git ")
	cut := strings.LastIndex(rest, " b/")
	if cut < 0 {
		// No recognizable pair: fall back to a whitespace split so the file still gets a name.
		parts := strings.Fields(rest)
		if len(parts) == 0 {
			return "", ""
		}
		return stripPathPrefix(parts[0]), stripPathPrefix(parts[len(parts)-1])
	}
	return stripPathPrefix(rest[:cut]), stripPathPrefix(rest[cut+1:])
}

// stripPathPrefix removes the a/ or b/ prefix and any trailing tab-delimited timestamp POSIX
// diff appends. The tab is what makes a path containing spaces readable at all, so the cut is
// on that and never on whitespace. /dev/null passes through untouched - callers key on it.
func stripPathPrefix(raw string) string {
	p := strings.TrimSpace(raw)
	if tab := strings.IndexByte(p, '\t'); tab >= 0 {
		p = strings.TrimSpace(p[:tab])
	}
	if p == devNull {
		return p
	}
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// FileHunks is one changed file's hunks, in patch order.
type FileHunks struct {
	Path  string `json:"path"`
	Hunks []Hunk `json:"hunks"`
}

// sanitizeRows escapes the deceptive characters in each row's text, which is what the browser
// draws - Lines is what the terminal draws, and both have to be covered or one surface renders a
// deception the other caught.
//
// AFTER markEmphasis, and the row's emphasis is DROPPED where the text changed. Emph is an offset
// into the text, so escaping would shift what it points at; and a line carrying a bidirectional
// override does not want a subtle intra-line highlight anyway. The escape is the signal.
func sanitizeRows(rows []Row) {
	for i := range rows {
		safe, changed := SanitizeBidi(rows[i].Text)
		if !changed {
			continue
		}
		rows[i].Text = safe
		rows[i].Emph = nil
	}
}

// displayLines returns lines with deceptive characters escaped, or nil when none carried any.
//
// Nil rather than a copy for the common case: every hunk in an honest patch takes this path, and a
// second slice per hunk would double what a large changeset holds to say nothing.
func displayLines(lines []string) []string {
	var out []string
	for i, l := range lines {
		safe, changed := SanitizeBidi(l)
		if !changed {
			continue
		}
		if out == nil {
			out = slices.Clone(lines)
		}
		out[i] = safe
	}
	return out
}

// ParseHunks is the identity view of a patch: paths and hunk digests, without the rendering
// detail. It is what the session store and the MCP surface consume.
//
// A projection of Parse, never its own pass. This is what decides which files a changeset
// contains, so a reader of its own here would be the one place a dialect gap turns into an
// empty changeset.
func ParseHunks(patch string) []FileHunks {
	files := Parse(patch)
	if len(files) == 0 {
		return nil
	}
	out := make([]FileHunks, 0, len(files))
	for _, f := range files {
		out = append(out, FileHunks{Path: f.Path, Hunks: f.Hunks})
	}
	return out
}

// HunkCounts reports how many hunks each path has, which is all a validator needs.
func HunkCounts(patch string) map[string]int {
	out := map[string]int{}
	for _, f := range ParseHunks(patch) {
		out[f.Path] = len(f.Hunks)
	}
	return out
}

// PatchDigest is the identity of a whole patch, used to tell "the tree moved" from "the tree
// is the same and we simply looked again".
//
// A session holds a changeset computed at some past moment. Without this, a client that joins
// later cannot tell a current answer from a frozen one, and the party least able to notice -
// an agent, which cannot see the tree - is the one served the stale copy.
func PatchDigest(patch string) string {
	sum := sha256.Sum256([]byte(patch))
	return hex.EncodeToString(sum[:16])
}

// PlaceThreads resolves each thread's line onto the hunk of its file that contains it, setting
// Hunk to that index or to -1 when nothing in this changeset does.
//
// The arithmetic lives HERE, once, for the reason the parser and the emphasis do: a thread is
// anchored to a line of the REVIEW, and the review is not the changeset in front of the reader.
// Two surfaces working that out independently is the same remark landing against different code
// depending on where you opened it.
//
// The NEW side, always. A host anchors an inline comment to the line as it stands after the
// change, which is the side a reader is looking at; matching the old side would land a remark
// about new code on whatever used to be there.
//
// Threads are returned in the order given, so a caller that renders them keeps the
// conversation's order.
func PlaceThreads(files []FileHunks, threads []types.ReviewThread) []types.ReviewThread {
	if len(threads) == 0 {
		return nil
	}
	byPath := make(map[string][]Hunk, len(files))
	for _, f := range files {
		byPath[f.Path] = f.Hunks
	}
	out := make([]types.ReviewThread, 0, len(threads))
	for _, t := range threads {
		t.Hunk = -1
		for _, h := range byPath[t.Path] {
			if t.Line >= h.NewStart && t.Line < h.NewStart+h.NewCount {
				// Hunk.Index, not the slice position. They agree while a caller passes every
				// hunk of a file, and Index exists precisely so one that filters cannot
				// silently renumber what a thread is anchored to.
				t.Hunk = h.Index
				break
			}
		}
		out = append(out, t)
	}
	return out
}
