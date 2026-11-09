package proofread

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// RuleLocalBranch reports a branch that exists only on the author's machine,
// named in text a repository publishes. The names come from
// [WithLocalBranches]; proofread cannot tell a local branch from text alone.
const RuleLocalBranch Rule = "local-branch"

// WithLocalBranches names the branches only the author's machine holds, for
// [RuleLocalBranch]. Without it the rule judges nothing.
func WithLocalBranches(names ...string) Option {
	return func(o *options) { o.localBranches = append(o.localBranches, names...) }
}

// localBranch reports each local-only branch the written source names. A
// one-word name like `embed` is also a word, so it counts only in a code
// span. URLs are blanked first, as in [localPath].
func localBranch(in input) []Finding {
	names := slices.SortedFunc(slices.Values(in.opts.localBranches), func(a, b string) int {
		return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b))
	})
	names = slices.Compact(names)

	var out []Finding
	for i, line := range in.source {
		line = urls.ReplaceAllStringFunc(line, func(u string) string { return strings.Repeat(" ", len(u)) })
		for _, name := range names {
			if name == "" {
				continue
			}
			for at := range branchMentions(line, name) {
				if !wordLike(name) || inCodeSpan(line, at) {
					out = append(out, Finding{
						Message: fmt.Sprintf("Drop the branch '%s': it exists only on the author's machine, so no reader can open it.", name),
						Match:   name, Line: i + 1,
					})
					break
				}
			}
		}
	}
	return out
}

// branchMentions yields each offset in line where name stands as a whole
// token, so `embed` is not found inside `embed-index`.
func branchMentions(line, name string) func(yield func(int) bool) {
	return func(yield func(int) bool) {
		for from := 0; ; {
			at := strings.Index(line[from:], name)
			if at < 0 {
				return
			}
			at += from
			end := at + len(name)
			from = at + 1
			if at > 0 && nameByte(line[at-1]) {
				continue
			}
			if end < len(line) && nameByte(line[end]) && !closingPeriod(line, end) {
				continue
			}
			if !yield(at) {
				return
			}
		}
	}
}

// closingPeriod reports whether line[i] is a period that ends a sentence
// rather than continuing a name.
func closingPeriod(line string, i int) bool {
	return line[i] == '.' && (i+1 == len(line) || !nameByte(line[i+1]))
}

func nameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' ||
		b == '-' || b == '_' || b == '/' || b == '.'
}

func wordLike(name string) bool {
	return !strings.ContainsAny(name, "-_/.0123456789")
}

func inCodeSpan(line string, at int) bool {
	return strings.Count(line[:at], "`")%2 == 1
}
