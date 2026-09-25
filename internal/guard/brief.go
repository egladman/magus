package guard

import (
	"regexp"
	"strings"
)

// A spawn brief that teaches a command the guard denies. The worker copies commands from
// its brief as written, so a denied one is refused in every worker the brief reaches, or
// worse, teaches each of them a workaround. Measured 2026-09-24: 33 briefs seeded 462
// prefixes of a retired MAGUS_NO_WAIT variable the day it was removed.
//
// Only a command the brief presents as one is graded: a fenced shell block, or an inline
// code span. A line that names a command in order to forbid it ("never `git add -A`") is
// passed over, because it teaches the opposite.

// briefFenceRe matches a fenced block and its info string.
var briefFenceRe = regexp.MustCompile("(?ms)^[ \t]*```([A-Za-z]*)[^\n]*\n((?:.*?\n)??)[ \t]*```")

// briefSpanRe matches an inline code span on one line.
var briefSpanRe = regexp.MustCompile("`([^`\n]+)`")

// shellFence reports an info string that marks a block as shell: a shell's name, the sh
// family's tags, or a console transcript. An unlabeled block counts too: that is how most
// briefs write one.
func shellFence(lang string) bool {
	return lang == "" || lang == "console" || shells[lang] || strings.HasPrefix(lang, "sh")
}

// briefNegationRe matches the words that turn a named command into one NOT to run. It is
// read over the whole line a span sits on, or the line that introduces a block.
var briefNegationRe = regexp.MustCompile(`(?i)\b(?:never|not|don't|do not|no longer|avoid|instead of|rather than|deny|denies|denied|refuse[sd]?|forbid(?:den)?|banned|retired|removed|stop|without)\b|n't\b`)

// briefPlaceholderRe matches a `<placeholder>`, which a shell would read as a redirect.
var briefPlaceholderRe = regexp.MustCompile(`<[A-Za-z][^<>\n]*>`)

// denyBriefCommand refuses a brief that teaches a denied command, naming the command and
// the rule, or returns the zero verdict.
func denyBriefCommand(deps Dependencies, brief string) ShellVerdict {
	for _, cmd := range briefCommands(brief) {
		v := Evaluate(deps, briefPlaceholderRe.ReplaceAllString(cmd, "X"))
		if v.Deny == "" {
			continue
		}
		first, _, _ := strings.Cut(v.Deny, "\n")
		return ShellVerdict{
			Deny: "Fix the brief: it teaches `" + elideCommand(cmd) + "`, which the guard denies [" + v.RuleName() + "]. " + first + "\n" +
				"A worker runs a brief's commands as written, so every worker it reaches is refused, or learns a way around the refusal. A line that names it to forbid it (\"never ...\") is not graded.",
			Rule: denyRule{Name: denyRuleBriefCommand, Arg: v.RuleName()},
		}
	}
	return ShellVerdict{}
}

// briefCommands are the commands a brief presents as ones to run: each shell block whole,
// then each inline span outside the blocks.
func briefCommands(brief string) []string {
	var out []string
	lines := strings.Split(brief, "\n")
	for _, m := range briefFenceRe.FindAllStringSubmatchIndex(brief, -1) {
		lang := strings.ToLower(brief[m[2]:m[3]])
		if !shellFence(lang) {
			continue
		}
		if briefNegationRe.MatchString(lineBefore(lines, strings.Count(brief[:m[0]], "\n"))) {
			continue
		}
		var block []string
		for _, l := range strings.Split(brief[m[4]:m[5]], "\n") {
			block = append(block, strings.TrimPrefix(strings.TrimSpace(l), "$ "))
		}
		out = append(out, strings.Join(block, "\n"))
	}
	for _, line := range strings.Split(briefFenceRe.ReplaceAllString(brief, ""), "\n") {
		if briefNegationRe.MatchString(line) {
			continue
		}
		for _, m := range briefSpanRe.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// lineBefore is the last non-blank line above line index i, which is where a block's
// lead-in sentence sits.
func lineBefore(lines []string, i int) string {
	for j := i - 1; j >= 0; j-- {
		if strings.TrimSpace(lines[j]) != "" {
			return lines[j]
		}
	}
	return ""
}
