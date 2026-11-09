package proofread

import (
	"slices"
	"strings"
)

// SetRules are the rules [JudgeSet] runs: each reads a rate or a spread that
// one short text is too small to show.
var SetRules = []Rule{RuleUniform, RuleTriad}

// JudgeSet judges texts of kind as one set, such as a branch's commits. It
// pools them as [BuildVoice] pools a window, so the author's ranges apply at
// their own scale. Only [SetRules] run, narrowed by any [WithOnly]. Under
// 200 words it judges nothing and reports false. A finding names no line.
func JudgeSet(texts []string, kind Kind, opts ...Option) ([]Finding, bool) {
	var kept []string

	for _, text := range texts {
		if text = strings.TrimSpace(text); text != "" {
			kept = append(kept, text)
		}
	}

	pooled := strings.Join(kept, "\n\n")
	if Measure(pooled, kind).Words < voiceWindowWords {
		return nil, false
	}

	rules := slices.Clone(SetRules)
	if asked := collect(opts).only; len(asked) > 0 {
		rules = slices.DeleteFunc(rules, func(r Rule) bool { return !slices.Contains(asked, r) })
	}

	if len(rules) == 0 {
		return nil, true
	}

	setOnly := func(o *options) { o.only = rules }

	out := JudgeText(pooled, kind, append(slices.Clone(opts), setOnly)...)
	for i := range out {
		out[i].Line, out[i].Column, out[i].EndLine, out[i].EndColumn, out[i].Protected = 0, 0, 0, 0, nil
	}

	return out, true
}
