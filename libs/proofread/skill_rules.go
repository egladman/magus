package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// RuleSkillDescriptionBudget reports a skill's frontmatter description over
	// a word budget, and a full form's description that mostly repeats the
	// short form's.
	RuleSkillDescriptionBudget Rule = "skill-description-budget"
	// RuleSkillSetDescriptionBudget reports an installed set of skills whose
	// descriptions, both forms of each counted, sum past a word budget.
	RuleSkillSetDescriptionBudget Rule = "skill-set-description-budget"
	// RuleInstructionPosition reports a long full form of a skill whose first
	// "must" or "never" sits past the middle of its body.
	RuleInstructionPosition Rule = "instruction-position"
)

var skillKind = []Kind{KindSkillTemplate}

// No check here has a judge: each reads both renders of a skill body, which
// only [judgeSkillSource] holds.
var (
	descriptionCheck    = check{rule: RuleSkillDescriptionBudget, on: skillKind, advise: skillKind}
	setDescriptionCheck = check{rule: RuleSkillSetDescriptionBudget, on: skillKind, advise: skillKind}
	positionCheck       = check{rule: RuleInstructionPosition, on: skillKind, advise: skillKind}
	skillChecks         = []check{descriptionCheck, setDescriptionCheck, positionCheck}
)

var skillTexts = map[Rule]ruleText{
	RuleSkillDescriptionBudget: {
		code:      "PRF7020",
		dimension: DimensionEconomy,
		catches: fmt.Sprintf("a skill description over %d words, or a full form's description sharing %d%% or "+
			"more of its content words with the short form's", maxDescriptionWords, int(maxTwinRepeat*100)),
		why: "A host lists every installed skill by its description, so each one loads into every session " +
			"whether or not the skill is used, and a full twin's loads beside its short sibling's. The word " +
			"budget is the 75th percentile of the authored descriptions of the skills magus ships, counted in " +
			"words, so the longest quarter advises. A twin that restates its sibling's description loads the " +
			"same words twice. The repeat line was set by comparing, over the same skills, the content words " +
			"a twin's own line shares with its short form's against those a restated description plus a " +
			"sentence shares; it sits between the two, so the first passes and the second advises.",
	},
	RuleSkillSetDescriptionBudget: {
		code:      "PRF7022",
		dimension: DimensionEconomy,
		catches: fmt.Sprintf("an installed set of skills whose descriptions sum past %d words, each skill's short "+
			"and full form counted", maxSetDescriptionWords),
		why: "A per-skill budget passes a set that grows one skill at a time, yet the host loads every " +
			"description of the set into every session. The budget is the words of every short and full " +
			"description of the skills magus ships, back-references included, counted when the rule landed. " +
			"It is a ratchet: a skill added or a description grown advises until another description is cut " +
			"to make room.",
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
			"tokens a word, near the median length of the full forms magus ships.",
	},
}

// maxDescriptionWords and maxTwinRepeat are the budgets RuleSkillDescriptionBudget's
// why measures, and maxSetDescriptionWords RuleSkillSetDescriptionBudget's.
const (
	maxDescriptionWords    = 100
	maxTwinRepeat          = 0.75
	maxSetDescriptionWords = 2100
)

// skillEntry is one frontmatter block of a render: the skill it names, its
// one-line description, and the line of the render the description sits on.
type skillEntry struct {
	name, description string
	line              int
}

// entries are the frontmatter blocks a render opens with, back to back: one
// for a skill body, one per skill for a listing of an installed set. A block
// with no one-line description is left out.
func (f skillForm) entries() []skillEntry {
	lines := strings.Split(f.text, "\n")

	var out []skillEntry

	for i := 0; i < len(lines) && strings.TrimSpace(lines[i]) == "---"; i++ {
		var e skillEntry

		for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "---"; i++ {
			if v, ok := strings.CutPrefix(lines[i], "name:"); ok {
				e.name = strings.TrimSpace(v)
			}

			if v := descriptionValue(lines[i]); v != "" && e.line == 0 {
				e.description, e.line = v, i+1
			}
		}

		if e.line > 0 {
			out = append(out, e)
		}
	}

	return out
}

// sourceLine is the line of the source that line of the render reads from.
func (f skillForm) sourceLine(line int) int {
	if line > 0 && line <= len(f.lines) {
		return f.lines[line-1]
	}

	return line
}

// judgeDescriptions judges each skill's descriptions in forms, and then the
// set they make together.
func judgeDescriptions(forms [2]skillForm, o options) []Finding {
	var out []Finding

	if d := o.decide(descriptionCheck, KindSkillTemplate); d != DecisionOff {
		for _, pair := range pairEntries(forms[0].entries(), forms[1].entries()) {
			out = append(out, judgeDescription(forms, pair, d, o)...)
		}
	}

	return append(out, judgeSetDescriptions(forms, o)...)
}

// pairEntries pairs each short entry with the full entry of the same skill
// name, in the order the short form lists them, then each full entry left
// over alone. A form that drops or reorders a skill still pairs the rest.
// Entries of one name pair in the order each form lists them.
func pairEntries(shorts, fulls []skillEntry) [][2]skillEntry {
	byName := map[string][]int{}
	for i, e := range fulls {
		byName[e.name] = append(byName[e.name], i)
	}

	paired := make([]bool, len(fulls))

	var out [][2]skillEntry

	for _, e := range shorts {
		pair := [2]skillEntry{e}

		if at := byName[e.name]; len(at) > 0 {
			pair[1], paired[at[0]] = fulls[at[0]], true
			byName[e.name] = at[1:]
		}

		out = append(out, pair)
	}

	for i, e := range fulls {
		if !paired[i] {
			out = append(out, [2]skillEntry{{}, e})
		}
	}

	return out
}

// judgeDescription judges one skill's short and full descriptions, pair[0]
// read from forms[0] and pair[1] from forms[1].
func judgeDescription(forms [2]skillForm, pair [2]skillEntry, d Decision, o options) []Finding {
	var out []Finding

	short, full := pair[0].description, pair[1].description

	for i, e := range pair {
		if n := len(strings.Fields(e.description)); n > maxDescriptionWords && (i == 0 || e.description != short) {
			out = append(out, o.settle(descriptionCheck, Finding{
				Message: fmt.Sprintf("Cut the description to %d words (it has %d): it loads into every session "+
					"whether or not the skill is used.", maxDescriptionWords, n),
				Line: forms[i].sourceLine(e.line),
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
			Line: forms[1].sourceLine(pair[1].line),
		}, d))
	}

	return out
}

// judgeSetDescriptions sums the description words of every skill in forms,
// the short form's and the full form's both, since an install of both forms
// lists each skill twice.
func judgeSetDescriptions(forms [2]skillForm, o options) []Finding {
	d := o.decide(setDescriptionCheck, KindSkillTemplate)
	if d == DecisionOff {
		return nil
	}

	words := map[string]int{}

	var names []string

	total, twins := 0, 0

	for i, form := range forms {
		for _, e := range form.entries() {
			n := len(strings.Fields(e.description))
			total += n

			if i == 1 {
				twins += n
			}

			if _, ok := words[e.name]; !ok {
				names = append(names, e.name)
			}

			words[e.name] += n
		}
	}

	if total <= maxSetDescriptionWords {
		return nil
	}

	slices.SortStableFunc(names, func(a, b string) int { return words[b] - words[a] })

	largest := make([]string, 0, 3)
	for _, name := range names[:min(3, len(names))] {
		label := name
		if label == "" {
			label = "the unnamed skill"
		}

		largest = append(largest, fmt.Sprintf("%s %d", label, words[name]))
	}

	return []Finding{o.settle(setDescriptionCheck, Finding{
		Message: fmt.Sprintf("Cut the set's descriptions to %d words (they have %d): every one loads into every "+
			"session. The largest are %s; the full twins carry %d (%d%%).",
			maxSetDescriptionWords, total, strings.Join(largest, ", "), twins, 100*twins/total),
	}, d)}
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
