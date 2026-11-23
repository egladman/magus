package edit

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

const blockKinds = "old, old all, pattern, new or create"

// ParseBlocks reads a block document: `## <path>` sections of
// fenced old, old all, pattern, new and create blocks. A block's
// text is the bytes between its fences, less the last line's
// terminator; a create block keeps every terminator. A
// refusal names the document, the line and the form expected
// there, and voids every change.
func ParseBlocks(name string, text []byte) types.EditDocument {
	b := blockParser{name: name, lines: splitLines(text), text: text}
	b.parse()
	if len(b.doc.Refused) > 0 {
		b.doc.Changes = nil
	}
	if b.doc.Changes == nil && len(b.doc.Refused) == 0 {
		b.refuse(1, "the document holds no `## <path>` section")
	}
	return b.doc
}

// line is one line of a document as offsets; end is before its
// terminator.
type line struct{ start, end int }

func splitLines(text []byte) []line {
	var out []line
	for start := 0; start < len(text); {
		end, next := len(text), len(text)
		if nl := bytes.IndexByte(text[start:], '\n'); nl >= 0 {
			end, next = start+nl, start+nl+1
		}
		out = append(out, line{start: start, end: end})
		start = next
	}
	return out
}

type blockParser struct {
	name  string
	text  []byte
	lines []line
	doc   types.EditDocument

	path    string // the open section's path, "" before the first
	heading int    // the open section's line
	blocks  int    // blocks the open section holds
	pending *pendingBlock
}

// pendingBlock is an old or pattern block waiting for its new.
type pendingBlock struct {
	line  int
	kind  string
	text  string
	count int
	regex bool
}

func (b *blockParser) refuse(n int, reason string) {
	b.doc.Refused = append(b.doc.Refused, types.EditRefusal{Path: b.name, Line: n, Column: 1, Reason: reason})
}

func (b *blockParser) content(i int) string {
	return strings.TrimRight(string(b.text[b.lines[i].start:b.lines[i].end]), "\r")
}

func (b *blockParser) parse() {
	for i := 0; i < len(b.lines); i++ {
		ln := b.content(i)
		if rest, ok := strings.CutPrefix(ln, "##"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			b.section(i+1, strings.TrimSpace(rest))
			continue
		}
		fence, info, ok := fenceOpen(ln)
		if !ok {
			continue
		}
		end := b.closing(i+1, fence)
		if end < 0 {
			b.refuse(i+1, fmt.Sprintf("the %s fence opened here never closes: "+
				"close it with a line of %d or more %c", fence, len(fence), fence[0]))
			return
		}
		b.block(i+1, info, i+1, end)
		i = end
	}
	b.section(0, "")
}

// closing is the index of the line that closes fence, searching
// from line from, or -1.
func (b *blockParser) closing(from int, fence string) int {
	for j := from; j < len(b.lines); j++ {
		ln := strings.TrimRight(b.content(j), " \t")
		if len(ln) >= len(fence) && strings.Trim(ln, fence[:1]) == "" {
			return j
		}
	}
	return -1
}

// body is the text of lines [from, to) without the last line's
// terminator.
func (b *blockParser) body(from, to int) string {
	if from == to {
		return ""
	}
	end := b.lines[to-1].end
	if end > b.lines[to-1].start && b.text[end-1] == '\r' {
		end--
	}
	return string(b.text[b.lines[from].start:end])
}

// fenceOpen reads a line that opens a fence: three or more
// backticks or tildes at the start, then the info string.
func fenceOpen(ln string) (string, string, bool) {
	if ln == "" || (ln[0] != '`' && ln[0] != '~') {
		return "", "", false
	}
	n := len(ln) - len(strings.TrimLeft(ln, ln[:1]))
	if n < 3 {
		return "", "", false
	}
	info := strings.TrimSpace(ln[n:])
	// CommonMark: a backtick fence's info string holds no backtick.
	if ln[0] == '`' && strings.Contains(info, "`") {
		return "", "", false
	}
	return ln[:n], info, true
}

// section closes the open section and opens one at path; a zero
// line closes the last.
func (b *blockParser) section(n int, path string) {
	if b.pending != nil {
		b.unpaired()
	}
	if b.path != "" && b.blocks == 0 {
		b.refuse(b.heading, "the section holds no block: expected ```old then ```new, or ```create")
	}
	if n > 0 && path == "" {
		b.refuse(n, "a section heading names a workspace-relative path: `## <path>`")
	}
	b.path, b.heading, b.blocks = path, n, 0
}

func (b *blockParser) unpaired() {
	b.refuse(b.pending.line, fmt.Sprintf("```%s has no ```new after it: follow it with a ```new block", b.pending.kind))
	b.pending = nil
}

// block reads the block on lines [from, to), whose fence opens on
// line n.
func (b *blockParser) block(n int, info string, from, to int) {
	if b.path == "" {
		if b.heading == 0 {
			b.refuse(n, "a block before the first `## <path>` heading belongs to no file: open a section first")
		}
		return
	}
	b.blocks++
	kind := strings.Join(strings.Fields(info), " ")
	switch kind {
	case "old", "old all", "pattern":
		if b.pending != nil {
			b.unpaired()
		}
		b.pending = &pendingBlock{line: n, kind: kind, text: b.body(from, to), count: 1}
		if kind != "old" {
			b.pending.count, b.pending.regex = -1, kind == "pattern"
		}
	case "new":
		if b.pending == nil {
			b.refuse(n, "```new has no ```old, ```old all or ```pattern before it")
			return
		}
		p := b.pending
		b.pending = nil
		b.doc.Changes = append(b.doc.Changes, types.EditChange{Path: b.path, Old: p.text, New: b.body(from, to), Count: p.count, Regex: p.regex})
	case "create":
		if b.pending != nil {
			b.unpaired()
		}
		content := string(b.text[b.lines[from].start:b.lines[to].start])
		b.doc.Changes = append(b.doc.Changes, types.EditChange{Path: b.path, New: content, Create: true})
	case "":
		b.refuse(n, "the fence names no kind: expected "+blockKinds)
	default:
		b.refuse(n, fmt.Sprintf("unknown block kind %q: expected %s", kind, blockKinds))
	}
}
