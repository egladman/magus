package edit

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// Creation writes a new file at Path holding Content.
type Creation struct{ Path, Content string }

// Expansion is what [Expand] made of a list of changes: the
// request to resolve, the changes the tree already holds, and
// the changes it refused.
type Expansion struct {
	Sites   []Site
	Creates []Creation
	Already []types.EditChange
	Refused []types.EditRefusal
}

// CommittedFile reads a workspace-relative path as the committed
// revision holds it. Any error, an absent path included, means
// the revision holds none of its text.
type CommittedFile func(rel string) (string, error)

// Expand reads each file once to build a [Request] without writing.
// Old inside new is skipped, so `a -> ab` applies once.
// Absent deletions and unmatched patterns are Already only if committed
// proves a prior match. A change that would apply again is refused.
func Expand(root string, changes []types.EditChange, committed CommittedFile) Expansion {
	x := expander{root: root, files: map[string]readResult{}, committed: committed}
	if len(changes) == 0 {
		x.refuse(types.EditChange{}, "nothing to edit")
	}
	for i, c := range changes {
		x.current = i
		x.expand(c)
	}
	// Convergence over a partly refused plan would judge output
	// the plan never writes.
	if len(x.out.Refused) == 0 {
		x.converge(changes)
	}
	return x.out
}

type readResult struct {
	body   []byte
	reason string
}

type committedRead struct {
	text string
	err  error
}

type expander struct {
	root      string
	files     map[string]readResult
	committed CommittedFile
	heads     map[string]committedRead
	out       Expansion

	// spans and owners run parallel to out.Sites: each site's byte
	// offsets, and the index of the change that made it.
	spans   []span
	owners  []int
	current int
}

func (x *expander) refuse(c types.EditChange, reason string) {
	x.out.Refused = append(x.out.Refused, types.EditRefusal{Path: c.Path, Reason: reason})
}

func (x *expander) already(c types.EditChange) { x.out.Already = append(x.out.Already, c) }

func (x *expander) read(rel string) readResult {
	r, ok := x.files[rel]
	if !ok {
		body, _, reason := readRegular(x.root, rel)
		r = readResult{body: body, reason: reason}
		x.files[rel] = r
	}
	return r
}

func (x *expander) readCommitted(rel string) (string, error) {
	r, ok := x.heads[rel]
	if !ok {
		r.text, r.err = x.committed(rel)
		if x.heads == nil {
			x.heads = map[string]committedRead{}
		}
		x.heads[rel] = r
	}
	return r.text, r.err
}

func (x *expander) site(rel string, body []byte, start, end int, repl string) {
	x.out.Sites = append(x.out.Sites, Site{
		Path:  rel,
		Start: positionAt(body, start),
		End:   positionAt(body, end),
		Old:   string(body[start:end]),
		New:   repl,
	})
	x.spans = append(x.spans, span{start: start, end: end, repl: repl})
	x.owners = append(x.owners, x.current)
}

func (x *expander) expand(c types.EditChange) {
	rel, ok := localPath(c.Path)
	switch {
	case !ok:
		x.refuse(c, notLocal)
		return
	case c.Create && (c.Regex || c.Old != ""):
		x.refuse(c, "a creation carries no old text or pattern; drop old, or drop create")
		return
	case c.Create:
		c.Path = rel
		x.create(c)
		return
	case c.Count != 1 && c.Count != -1:
		x.refuse(c, fmt.Sprintf("count %d is neither 1, one occurrence, nor -1, every occurrence", c.Count))
		return
	case c.Old == "":
		x.refuse(c, "old is empty: name the text to replace; a new file is a creation")
		return
	case !c.Regex && c.Old == c.New:
		x.refuse(c, "old and new are the same text, so the change changes nothing")
		return
	}
	c.Path = rel
	r := x.read(rel)
	switch {
	case r.reason == noSuchFile:
		x.refuse(c, "no such file; a new file is a creation")
	case r.reason != "":
		x.refuse(c, r.reason)
	case c.Regex:
		x.regex(c, r.body)
	default:
		x.literal(c, r.body)
	}
}

func (x *expander) create(c types.EditChange) {
	r := x.read(c.Path)
	switch {
	case r.reason == noSuchFile:
		x.out.Creates = append(x.out.Creates, Creation{Path: c.Path, Content: c.New})
	case r.reason != "":
		x.refuse(c, r.reason)
	case string(r.body) == c.New:
		x.already(c)
	default:
		x.refuse(c, "exists and holds other content; change it with old and new, or remove it first")
	}
}

func (x *expander) literal(c types.EditChange, body []byte) {
	content := string(body)
	olds := occurrences(content, c.Old)
	if strings.Contains(c.New, c.Old) {
		// Old inside an applied New is not Old: without this, replacing
		// "a" with "ab" grows the file on every rerun.
		olds = outside(olds, len(c.Old), occurrences(content, c.New), len(c.New))
	}
	switch {
	case len(olds) == 0:
		x.absent(c, content)
	case c.Count == 1 && len(olds) > 1:
		x.ambiguous(c, body, olds, "old occurs", "pass the line's own text, or replace every occurrence (`replaceAll`, or an `old all` block)")
	default:
		for _, at := range olds {
			x.site(c.Path, body, at, at+len(c.Old), c.New)
		}
	}
}

// absent places a literal change whose old text the file does not
// hold: applied already, or a miss whose fix the refusal names.
func (x *expander) absent(c types.EditChange, content string) {
	if c.New == "" {
		x.unproven(c, func(committed string) bool { return strings.Contains(committed, c.Old) },
			"old is absent", "this deletion", "name a line that stays, in old and in new")
		return
	}
	news := strings.Count(content, c.New)
	switch {
	case news == 1, c.Count == -1 && news > 1:
		x.already(c)
	case news > 1:
		x.refuse(c, fmt.Sprintf("old is absent and new occurs %d times, so whether "+
			"this change was applied cannot be told: pass more of the line's text", news))
	default:
		r := types.EditRefusal{Path: c.Path, Reason: "not found; the file holds neither old nor new"}
		if line, text, ok := nearest(content, c.Old); ok {
			r.Line, r.Column = line, 1
			r.Reason += fmt.Sprintf("; the nearest text is line %d: %q", line, text)
		}
		x.out.Refused = append(x.out.Refused, r)
	}
}

func (x *expander) ambiguous(c types.EditChange, body []byte, at []int, what, fix string) {
	where := make([]string, len(at))
	for i, off := range at {
		pos := positionAt(body, off)
		where[i] = fmt.Sprintf("%s:%d:%d", c.Path, pos.Line, pos.Column)
	}
	x.refuse(c, fmt.Sprintf("%s %d times, at %s: %s", what, len(at), strings.Join(where, ", "), fix))
}

func (x *expander) regex(c types.EditChange, body []byte) {
	re, err := regexp.Compile(c.Old)
	if err != nil {
		x.refuse(c, fmt.Sprintf("the pattern is not RE2: %v", err))
		return
	}
	matches := re.FindAllSubmatchIndex(body, -1)
	var found []span
	for _, m := range matches {
		repl := string(re.Expand(nil, []byte(c.New), body, m))
		// A match that already reads as its expansion is applied.
		if repl != string(body[m[0]:m[1]]) {
			found = append(found, span{start: m[0], end: m[1], repl: repl})
		}
	}
	switch {
	case len(matches) == 0:
		x.unproven(c, re.MatchString, "the pattern matches nothing", "this change",
			"write the pattern to match its own output too, such as `(?:old|new)`, so a rerun reads as applied")
	case len(found) == 0:
		x.already(c)
	case c.Count == 1 && len(matches) > 1:
		starts := make([]int, len(matches))
		for i, m := range matches {
			starts[i] = m[0]
		}
		x.ambiguous(c, body, starts, "the pattern matches", "anchor it to one match, or set count -1 to replace every match")
	default:
		for _, f := range found {
			x.site(c.Path, body, f.start, f.end, f.repl)
		}
	}
}

// unproven places a change its absence cannot prove applied. It is
// Already when holds finds the text in the committed revision.
func (x *expander) unproven(c types.EditChange, holds func(committed string) bool, what, change, fix string) {
	cannot := fmt.Sprintf("so whether %s was applied cannot be told: %s", change, fix)
	if x.committed == nil {
		x.refuse(c, fmt.Sprintf("%s and no version control holds a committed revision to compare, %s", what, cannot))
		return
	}
	text, err := x.readCommitted(c.Path)
	switch {
	case err != nil:
		x.refuse(c, fmt.Sprintf("%s and the committed revision cannot be read (%v), %s", what, err, cannot))
	case holds(text):
		x.already(c)
	default:
		x.refuse(c, fmt.Sprintf("%s here and in the committed revision, %s", what, cannot))
	}
}

// converge refuses each change that would apply again to the
// plan's own output. A file whose sites overlap is left to
// [Resolve], which refuses the overlap.
func (x *expander) converge(changes []types.EditChange) {
	after := map[string][]byte{}
	for rel, spans := range x.spansByFile(-1) {
		if body, ok := composed(x.files[rel].body, spans); ok {
			after[rel] = body
		}
	}
	for i, c := range changes {
		rel, _ := localPath(c.Path)
		body, edited := after[rel]
		if c.Create || !edited {
			continue
		}
		if left, again := x.reapply(c, rel, body); again {
			x.diverges(i, c, rel, left)
		}
	}
}

// spansByFile groups the spans of change owner's sites by file,
// or every change's for owner -1.
func (x *expander) spansByFile(owner int) map[string][]span {
	out := map[string][]span{}
	for i, s := range x.out.Sites {
		if owner < 0 || x.owners[i] == owner {
			out[s.Path] = append(out[s.Path], x.spans[i])
		}
	}
	return out
}

// reapply expands c alone over body, the text of rel once edited,
// and returns the first site it would still make.
func (x *expander) reapply(c types.EditChange, rel string, body []byte) (Site, bool) {
	again := expander{root: x.root, files: map[string]readResult{rel: {body: body}}}
	again.expand(c)
	if len(again.out.Sites) == 0 {
		return Site{}, false
	}
	return again.out.Sites[0], true
}

// diverges refuses change i, which would apply again at left. The
// fix depends on whose output it rematches: its own or another's.
func (x *expander) diverges(i int, c types.EditChange, rel string, left Site) {
	alone, _ := composed(x.files[rel].body, x.spansByFile(i)[rel])
	_, self := x.reapply(c, rel, alone)
	var what, fix string
	switch {
	case self && c.Regex:
		what, fix = "the pattern matches its own output",
			"write the pattern so its output does not match it, or matches as itself"
	case self:
		what, fix = "the replacement forms old again",
			"replace the whole run in one change, such as a pattern that matches every repeat"
	default:
		what, fix = "another change in the plan writes text this change replaces (a chain or a swap)",
			"write one change from the text the file holds to the text it should hold"
	}
	x.refuse(c, fmt.Sprintf("%s at line %d of the edited file, so a rerun would apply it again: %s", what, left.Start.Line, fix))
}

// composed writes spans into body, or reports that two overlap.
func composed(body []byte, spans []span) ([]byte, bool) {
	sorted := slices.Clone(spans)
	slices.SortFunc(sorted, func(a, b span) int {
		return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(a.end, b.end))
	})
	for k := 1; k < len(sorted); k++ {
		if sorted[k].start < sorted[k-1].end || sorted[k].start == sorted[k-1].start {
			return nil, false
		}
	}
	return compose(body, sorted), true
}

// occurrences are the offsets of every non-overlapping s in content.
func occurrences(content, s string) []int {
	var at []int
	for off := 0; ; {
		i := strings.Index(content[off:], s)
		if i < 0 {
			return at
		}
		at = append(at, off+i)
		off += i + len(s)
	}
}

// outside drops each n-byte occurrence that lies within an m-byte
// occurrence starting at one of within.
func outside(at []int, n int, within []int, m int) []int {
	var out []int
	for _, a := range at {
		inside := false
		for _, w := range within {
			if w <= a && a+n <= w+m {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, a)
		}
	}
	return out
}

// nearMissLimit caps the old text the near-miss search reads; past
// it the refusal names no nearest text.
const nearMissLimit = 2048

// nearest is the run of lines in content, as many as old holds,
// closest to old by edit distance. It reports none past a few
// characters, a tenth of old for long text.
func nearest(content, old string) (int, string, bool) {
	if len(old) > nearMissLimit {
		return 0, "", false
	}
	lines := strings.Split(content, "\n")
	n := strings.Count(old, "\n") + 1
	best := max(4, len(old)/10) + 1
	line, text := 0, ""
	for i := 0; i+n <= len(lines); i++ {
		window := strings.Join(lines[i:i+n], "\n")
		if d := distance(window, old, best); d < best {
			best, line, text = d, i+1, window
		}
	}
	return line, text, line > 0
}

// distance is the Levenshtein distance between a and b in bytes,
// or limit when it is limit or more. Only the band of width limit
// around the diagonal can stay under it, so only that is computed.
func distance(a, b string, limit int) int {
	if d := len(a) - len(b); d >= limit || -d >= limit {
		return limit
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = min(j, limit)
	}
	for i := 1; i <= len(a); i++ {
		lo, hi := max(1, i-limit), min(len(b), i+limit)
		cur[lo-1] = limit
		if lo == 1 {
			cur[0] = min(i, limit)
		}
		rowBest := cur[lo-1]
		for j := lo; j <= hi; j++ {
			sub := prev[j-1]
			if a[i-1] != b[j-1] {
				sub++
			}
			cur[j] = min(sub, prev[j]+1, cur[j-1]+1, limit)
			rowBest = min(rowBest, cur[j])
		}
		if hi < len(b) {
			cur[hi+1] = limit
		}
		if rowBest >= limit {
			return limit
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
