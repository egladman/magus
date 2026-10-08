package prose

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ruleWord is the word a skill keeps for what magus enforces.
var ruleWord = regexp.MustCompile(`(?i)\brules?\b`)

// ruleQualifiers name a mechanism that enforces a rule. A rule is something
// magus refuses: a guard rule from its catalog, one a workspace declares with
// magus\guard.command, write, spawn, shell or builtins, or a lint rule. What a
// skill otherwise asks of an agent is an instruction, which the first denial
// teaches better than a restatement.
var ruleQualifiers = []string{
	"builtin", "builtins", "command", "guard", "lint", "shell", "spawn", "workspace", "write",
}

// bareRule reports "rule" or "rules" with no mechanism named before it and no
// rule id in code beside it.
func bareRule(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, at := range ruleWord.FindAllStringIndex(para.text, -1) {
			if ruleQualified(para.text, at[0], at[1]) {
				continue
			}

			m := para.text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Qualify '%s' with what enforces it (a guard, lint or workspace rule, or "+
					"its id in code), or write it as an instruction: a rule is only what magus refuses.", m),
				Match: m, Line: para.lineAt(at[0]),
			})
		}
	}

	return out
}

// ruleQualified reports whether the word at text[start:end] is qualified: the
// word before it, or the last part of a hyphenated one, is a qualifier, or a
// code span (masked to '#') sits on either side, naming the rule's id.
func ruleQualified(text string, start, end int) bool {
	if before := strings.Fields(text[:start]); len(before) > 0 {
		w := strings.ToLower(strings.Trim(before[len(before)-1], "*_(\"'"))
		w = strings.TrimSuffix(w, "'s")

		if strings.Trim(w, "#") == "" && w != "" {
			return true
		}

		if i := strings.LastIndexByte(w, '-'); i >= 0 {
			w = w[i+1:]
		}

		if slices.Contains(ruleQualifiers, w) {
			return true
		}
	}

	after := strings.Fields(text[end:])

	return len(after) > 0 && strings.HasPrefix(after[0], "#")
}
