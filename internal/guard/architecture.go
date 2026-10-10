package guard

import (
	"regexp"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
)

// architectureSkill answers structure questions from the workspace's own edges: what
// imports what, where a cycle closes, which layer a package sits in, how far a change
// reaches. Reading files answers none of those, and a proposal argued from the files
// in view is argued from taste.
var architectureSkill = agent.MustSkill("magus-architecture-review")

// topicArchitecture marks a session whose person asked a structure question. It is a
// FACT about the session, keyed like a skill load's, and recording it says nothing to
// the model: architecture-unbriefed reads it on the next graded call.
const topicArchitecture hint.MarkerKind = "topic-architecture"

// architectureVocabulary is the phrasing a structure question arrives in. Every
// alternative is a whole word or phrase, so "imports" matches and "important" does not.
var architectureVocabulary = regexp.MustCompile(`(?i)\b(?:` +
	`architect(?:ure|ures|ural|urally)` +
	`|seams?|boundary|boundaries|layers?|layering|coupling|cohesion|domains?|bounded\s+contexts?` +
	`|imports?|importing|(?:circular|cyclic)\s+dependenc(?:y|ies)|dependency\s+direction` +
	`|package\s+layout|new\s+(?:packages?|modules?)|co-?locat\w*|sprawl\w*|god\s+(?:packages?|objects?)` +
	`|fan-?(?:in|out)|blast\s+radius|restructur\w*|reorgani[sz]\w*` +
	`|where\s+(?:does|do|should|would|will|can)\s+(?:[\w./-]+\s+){1,5}?(?:belong|live|go)` +
	`|(?:fold|folds|folding|folded|extract|extracts|extracting|extracted|split|splits|splitting)\s+(?:[\w./-]+\s+){0,5}?(?:packages?|modules?)` +
	`|(?:respect\w*|fits?|fitting)\s+(?:[\w./-]+\s+){0,3}?(?:patterns?|conventions?)` +
	`)\b`)

// architectureElsewhere is the same word meaning something else: an instruction set, or a
// figure someone wants redrawn. Both are cut from the prompt before the vocabulary is
// asked, so the rest of the prompt can still match on its own terms.
var architectureElsewhere = regexp.MustCompile(`(?i)\b(?:` +
	`(?:cpu|platform|target|host|machine|processor|instruction[- ]set)\s+architectures?` +
	`|architectures?\s+(?:diagrams?|figures?)` +
	`)\b`)

// cpuArchitecture names an instruction set. A prompt that names one means a CPU by the
// bare word "architecture", so that word alone stops counting there.
var cpuArchitecture = regexp.MustCompile(`(?i)\b(?:arm64|aarch64|amd64|x86[-_]64|x86|i386|riscv64|ppc64le|s390x|goarch)\b`)

var architectureWord = regexp.MustCompile(`(?i)\barchitect(?:ure|ures|ural|urally)\b`)

// asksArchitecture reports whether a submitted prompt is a structure question.
func asksArchitecture(prompt string) bool {
	text := architectureElsewhere.ReplaceAllString(prompt, " ")
	if cpuArchitecture.MatchString(text) {
		text = architectureWord.ReplaceAllString(text, " ")
	}
	return architectureVocabulary.MatchString(text)
}

// recordPromptTopics marks the topics a submitted prompt raises on the session's facts,
// and reports whether it marked any. It judges nothing and returns no text: the person's
// words are not something to answer with an advisory, and the rule that reads the mark
// speaks on the model's next call instead.
func recordPromptTopics(facts hint.Gate, prompt string) bool {
	if facts.Session() == "" || !asksArchitecture(prompt) {
		return false
	}
	facts.MarkFired(topicArchitecture)
	return facts.AlreadyFired(topicArchitecture)
}

// denyArchitectureWithoutSkill is architecture-unbriefed: the verdict for a graded call by
// an agent that has not loaded the architecture skill, made in a session whose person asked
// a structure question, or for a write that creates a new directory. newDir is that
// directory, "" when the write creates none or the call is not a write.
//
// facts holds the session's topic mark and skills the calling agent's own loads, so a
// subagent working in an architecture conversation is briefed by its own load and never
// by its parent's.
func denyArchitectureWithoutSkill(facts, skills hint.Gate, observesSkillLoads bool, workspace, newDir string) string {
	const carries = "The knowledge graph answers imports, cycles, layering and coupling from the workspace's own edges; reading files does not."
	if newDir != "" {
		return denyUntilSkillLoaded(skills, observesSkillLoads, workspace, architectureSkill,
			"creating the new directory `"+newDir+"`", carries)
	}
	if !facts.AlreadyFired(topicArchitecture) {
		return ""
	}
	return denyUntilSkillLoaded(skills, observesSkillLoads, workspace, architectureSkill,
		"acting on an architecture question", carries)
}

// rankArchitectureUnbriefed ranks the reason under every other deny on the line, as
// rankBuzzAuthor does: a call refused anyway has nothing to learn first.
func rankArchitectureUnbriefed(v ShellVerdict, reason string) ShellVerdict {
	if reason == "" || v.Deny != "" {
		return v
	}
	return ShellVerdict{Deny: reason, Rule: denyRule{Name: denyArchitectureUnbriefed}}
}
