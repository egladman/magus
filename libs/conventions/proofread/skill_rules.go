package proofread

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// RuleSkillDescriptionBudget reports a skill's frontmatter description over
	// a word budget, and a full form's description that mostly repeats the
	// short form's.
	RuleSkillDescriptionBudget Rule = "skill-description-budget"
	// RuleInstructionPosition reports a long full form of a skill whose first
	// "must" or "never" sits past the middle of its body.
	RuleInstructionPosition Rule = "instruction-position"
)

var skillKind = []Kind{KindSkillTemplate}

// Neither check has a judge: each reads both renders of a skill body, which
// only [judgeSkillSource] holds.
var (
	descriptionCheck = check{rule: RuleSkillDescriptionBudget, on: skillKind, advise: skillKind}
	positionCheck    = check{rule: RuleInstructionPosition, on: skillKind, advise: skillKind}
	skillChecks      = []check{descriptionCheck, positionCheck}
)

var skillTexts = map[Rule]ruleText{
	RuleSkillDescriptionBudget: {
		code:      "PRF7020",
		dimension: DimensionEconomy,
		catches: fmt.Sprintf("a skill description over %d words, or a full form's description sharing %d%% or "+
			"more of its content words with the short form's", maxDescriptionWords, int(maxTwinRepeat*100)),
		why: "A host lists every installed skill by its description, so each one loads into every session " +
			"whether or not the skill is used, and a full twin's loads beside its short sibling's. Measured " +
			"2026-10-13 over the 16 skills magus ships: 1444 words of authored description, 49 to 111 words " +
			"each (p50 94, p75 101), 2396 words loaded in all with the twins and their back-references. The " +
			"budget sits at p75, so the longest quarter advises. A twin that restated its sibling's " +
			"description cost 10105 of 19113 description bytes before magus gave it a line of its own. " +
			"Today's standalone twin lines share 44 to 62% of their content words with the short form's " +
			"(back-references included), and each sibling's restated description plus a sentence shares 84 " +
			"to 92%, so the line sits between them at 75%.",
	},
	RuleInstructionPosition: {
		code:      "PRF7021",
		dimension: DimensionStructure,
		catches: fmt.Sprintf("a full skill form of %d words or more whose first \"must\" or \"never\" sits past "+
			"the middle of its body", minPositionWords),
		why: "\"Lost in the Middle\" (Liu et al., TACL 2023) measured models using what sits at the start and " +
			"the end of a long context better than the middle, from its shortest setting of about 2000 " +
			"tokens up. IFScale (Jaroslawicz et al., arXiv 2507.11538) found frontier models favor early " +
			"instructions only at very high instruction counts, so the rule judges the full form alone, " +
			"the copy written for less capable models. Long is that 2000 tokens, about 1500 words at 1.3 " +
			"tokens a word, near the median of the 16 full forms magus ships (479 to 4052 words, p50 1577).",
	},
}

// maxDescriptionWords and maxTwinRepeat are the budgets RuleSkillDescriptionBudget's
// why measures.
const (
	maxDescriptionWords = 100
	maxTwinRepeat       = 0.75
)

// description is the frontmatter description of a render and the line of the
// render it sits on, or "" when the render opens with no frontmatter or none
// is given on one line.
func (f skillForm) description() (string, int) {
	lines := strings.Split(f.text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", 0
	}

	for i := 1; i < len(lines) && strings.TrimSpace(lines[i]) != "---"; i++ {
		if v := descriptionValue(lines[i]); v != "" {
			return v, i + 1
		}
	}

	return "", 0
}

// sourceLine is the line of the source that line of the render reads from.
func (f skillForm) sourceLine(line int) int {
	if line > 0 && line <= len(f.lines) {
		return f.lines[line-1]
	}

	return line
}

func judgeDescriptions(forms [2]skillForm, o options) []Finding {
	d := o.decide(descriptionCheck, KindSkillTemplate)
	if d == DecisionOff {
		return nil
	}

	var out []Finding

	short, shortLine := forms[0].description()
	full, fullLine := forms[1].description()

	for i, desc := range []string{short, full} {
		line := forms[i].sourceLine([]int{shortLine, fullLine}[i])
		if n := len(strings.Fields(desc)); n > maxDescriptionWords && (i == 0 || desc != short) {
			out = append(out, o.settle(descriptionCheck, Finding{
				Message: fmt.Sprintf("Cut the description to %d words (it has %d): it loads into every session "+
					"whether or not the skill is used.", maxDescriptionWords, n),
				Line: line,
			}, d))
		}
	}

	if short == "" || full == "" {
		return out
	}

	fullWords := descriptionWords(full)
	if len(fullWords) == 0 {
		return out
	}

	repeat := float64(shared(fullWords, descriptionWords(short))) / float64(len(fullWords))
	if repeat >= maxTwinRepeat {
		out = append(out, o.settle(descriptionCheck, Finding{
			Message: fmt.Sprintf("Give the full form a description of its own: %d%% of its words repeat the short "+
				"form's, and both load into every session.", int(repeat*100)),
			Line: forms[1].sourceLine(fullLine),
		}, d))
	}

	return out
}

// descriptionWords are the content words of a description, as
// RuleCompactDuplicate reads a line's.
func descriptionWords(desc string) map[string]bool {
	return readLineWords(proseLine{text: desc}).content
}

// minPositionWords is a full form long enough to lose its middle: about
// 2000 tokens, Liu et al.'s shortest context, at 1.3 tokens a word.
const minPositionWords = 1500

var mustOrNever = regexp.MustCompile(`(?i)\b(?:must|never)\b`)

func judgeInstructionPosition(full skillForm, o options) []Finding {
	d := o.decide(positionCheck, KindSkillTemplate)
	if d == DecisionOff {
		return nil
	}

	_, prose := readMarkdown(blankBoilerplate(strings.Split(full.text, "\n")), KindReference)

	total, before, at := 0, 0, 0

	for _, ln := range prose {
		body := mentionsMasked(ln.body())

		if loc := mustOrNever.FindStringIndex(body); loc != nil && at == 0 {
			before, at = total+countWords(body[:loc[0]]), ln.line
		}

		total += countWords(body)
	}

	if at == 0 || total < minPositionWords || 2*before <= total {
		return nil
	}

	return []Finding{o.settle(positionCheck, Finding{
		Message: fmt.Sprintf("Move the first must or never up: it sits %d%% of the way into a %d-word body, "+
			"past the middle a weaker model reads least.", 100*before/total, total),
		Line: full.sourceLine(at),
	}, d)}
}
