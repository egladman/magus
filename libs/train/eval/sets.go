package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/train/completion/fim"
)

// Set sizes. A test or dev file gets one span per SpanBytes of text, up to MaxPerFile.
const (
	SpanBytes  = 200
	MaxPerFile = 400
	// MaxTaskBody is the longest function body a pass@k task blanks.
	MaxTaskBody = 1500
	// MinTaskBody is the shortest: a body this short is a delegation the model cannot get wrong or right on its own.
	MinTaskBody = 24
)

// Span is one FIM evaluation example: a sample plus where it came from. Set is
// "<language>-test" or "<language>-dev".
type Span struct {
	ID    string `json:"id"`
	Set   string `json:"set"`
	Group string `json:"group"`
	fim.Sample
}

// SpanID is the stable id of a span: the first 16 hex digits of sha256(path, blob, range).
func SpanID(s fim.Sample) string {
	sum := sha256.Sum256([]byte(s.Path + "\x00" + s.Blob + "\x00" + strconv.Itoa(s.Start) + "\x00" + strconv.Itoa(s.End)))
	return hex.EncodeToString(sum[:8])
}

// FileSpans returns the evaluation spans of one file: FIM samples only, no two with the
// same range, with middles drawn from lim. The count is one per SpanBytes of the file
// text the limit leaves, up to MaxPerFile, and at least one.
func FileSpans(f fim.File, lim fim.Limit) []fim.Sample {
	to := lim.To
	if to == 0 {
		to = len(f.Text)
	}
	want := min(max((to-lim.From)/SpanBytes, 1), MaxPerFile)
	var out []fim.Sample
	seen := map[[2]int]bool{}
	for _, s := range fim.Build(f, want+want/5+2, lim) {
		if s.Kind == fim.KindLM || seen[[2]int{s.Start, s.End}] {
			continue
		}
		seen[[2]int{s.Start, s.End}] = true
		out = append(out, s)
		if len(out) == want {
			break
		}
	}
	return out
}

// Text is the text the 13-gram check holds training text against for a span: its middle,
// the reference answer. Context lines would flag every file that shares an import block or
// a license header with a held-out one, which says nothing about the answer leaking.
func Text(s fim.Sample) string { return s.Middle }

// Task is one Buzz pass@k task: a held-out function whose test blocks call it. The
// prompt is Prefix, which ends at the body's opening brace, and Suffix, which starts at the
// closing brace. Middle is the reference body. Tests are the names of the test blocks
// in the file that use the function.
type Task struct {
	ID     string   `json:"id"`
	Path   string   `json:"path"`
	Blob   string   `json:"blob"`
	Group  string   `json:"group"`
	Name   string   `json:"name"`
	Prefix string   `json:"prefix"`
	Middle string   `json:"middle"`
	Suffix string   `json:"suffix"`
	Tests  []string `json:"tests"`
}

// FileTasks returns the pass@k tasks of one Buzz file: every function with a body between
// MinTaskBody and MaxTaskBody bytes that the file's test blocks call, once its name is
// unique in the file. A file that does not parse has none.
func FileTasks(f fim.File, group string) []Task {
	funcs, tests, ok := fim.BuzzUnits(f.Text)
	if !ok {
		return nil
	}
	count := map[string]int{}
	for _, fn := range funcs {
		count[fn.Name]++
	}
	var out []Task
	for _, fn := range funcs {
		body := f.Text[fn.Body.Start:fn.Body.End]
		if count[fn.Name] != 1 || len(strings.TrimSpace(body)) < MinTaskBody || len(body) > MaxTaskBody {
			continue
		}
		var called []string
		for _, t := range tests {
			if t.Idents[fn.Name] && !(fn.Body.Start >= t.Body.Start && fn.Body.End <= t.Body.End) {
				called = append(called, t.Name)
			}
		}
		if len(called) == 0 {
			continue
		}
		t := Task{Path: f.Path, Blob: f.Blob, Group: group, Name: fn.Name, Tests: called,
			Prefix: f.Text[:fn.Body.Start], Middle: body, Suffix: f.Text[fn.Body.End:]}
		t.ID = SpanID(fim.Sample{Path: f.Path, Blob: f.Blob, Start: fn.Body.Start, End: fn.Body.End})
		out = append(out, t)
	}
	return out
}
