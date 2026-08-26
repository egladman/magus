// Package prompt assembles a prompt as a sequence of named sections.
//
// It exists because a prompt is a DELIVERABLE with rules, not a string somebody concatenates.
// Four of those rules kept being broken by hand, and each one produces a prompt that still looks
// perfectly good while telling the reader something false:
//
//   - A section with a heading and nothing under it reads as "we looked and found nothing" when
//     the truth is "nothing was added here". A section renders only once it receives CONTENT, and
//     the sentence explaining a section is deliberately not content - see [Builder.Note].
//   - A truncated list reads as a complete one. [Builder.Items] takes a limit and always states
//     the remainder, so silently dropping the tail is not something a caller can express.
//   - An unmeasured value rendered as a zero accuses. [Builder.Field] omits an empty value rather
//     than printing a blank or a nil-shaped default.
//   - A prompt that spends the reader's context restating what their tools already loaded is a
//     worse prompt for being longer. [Short] and [Long] are the same document at two lengths, so
//     the short one is a deliberate edit rather than whatever happened to get written.
//
// The output is markdown because that is what models read best and what a person pasting it can
// still skim. It is deliberately small: headings, prose, bullets, and fields. A prompt that needs
// tables or nesting is a prompt that has stopped being a briefing.
package prompt

import (
	"fmt"
	"strings"
)

// Variant selects how much of a prompt renders.
//
// It mirrors the skill catalog's two curated permutations rather than sharing its type: a skill
// variant carries template rendering, provenance and install stamping, none of which a prose
// builder should depend on. The CONCEPT is shared on purpose - one source, two lengths, the short
// one edited rather than truncated - and that is the part worth keeping consistent.
type Variant int

const (
	// Short is the default form: the facts, and nothing a reader's own tools already carry.
	Short Variant = iota
	// Long adds the rationale behind each instruction, for a reader who has not seen this before
	// or is deciding whether to trust it.
	Long
)

// Builder assembles a prompt. The zero value is not useful; call [New].
//
// There is ONE type and one method set, so a whole prompt is a single chain read top to bottom in
// the order it renders. [Builder.Section] closes whatever section was open and starts the next,
// which is what lets a section's emptiness be decided after its contents are known without the
// builder retaining a section object per heading.
type Builder struct {
	out     strings.Builder
	variant Variant
	// The open section: its heading, its lines so far, and whether any of them were content.
	// Held flat rather than as a separate value the caller keeps, because a caller holding one
	// could add to a section the builder has already flushed.
	title   string
	pending []string
	content bool
}

// New starts a prompt with the given H1 title, rendered at variant v.
func New(title string, v Variant) *Builder {
	b := &Builder{variant: v}
	fmt.Fprintf(&b.out, "# %s\n\n", title)
	return b
}

// Lead adds opening prose, above the first section. It always renders: it is the instruction the
// whole prompt exists to give, and a prompt whose framing depends on what was found would be a
// different prompt each time it runs.
func (b *Builder) Lead(lines ...string) *Builder {
	for _, line := range lines {
		b.out.WriteString(line)
		b.out.WriteString("\n")
	}
	return b
}

// Section closes the open section and starts one with the given H2 heading. A caller writes every
// section unconditionally and lets emptiness decide, rather than guarding each with a length check
// - which is the check that kept being forgotten.
func (b *Builder) Section(title string) *Builder {
	b.flush()
	b.title, b.pending, b.content = title, nil, false
	return b
}

// Note adds prose that does NOT count as content: the sentence telling a reader how to weigh what
// follows. A section holding only notes never renders, which is what keeps an explanation from
// appearing under a heading with no facts beneath it.
func (b *Builder) Note(lines ...string) *Builder {
	b.separate(false)
	b.pending = append(b.pending, lines...)
	return b
}

// Text adds prose that DOES count as content, for a section whose point is what it says rather
// than what it lists.
func (b *Builder) Text(lines ...string) *Builder {
	b.separate(false)
	b.pending = append(b.pending, lines...)
	b.content = true
	return b
}

// Because adds prose only in the [Long] variant: the rationale behind an instruction the short
// form states bare. Chained after the instruction it explains, a pair reads as one thought and
// renders as one or two depending on the variant.
//
// It is a Note, not Text - a section whose only long-form addition is rationale still has nothing
// to explain when there are no facts under it.
func (b *Builder) Because(lines ...string) *Builder {
	if b.variant != Long {
		return b
	}
	return b.Note(lines...)
}

// Only adds content in variant v and nothing otherwise. Two calls with the two variants are the
// builder's form of a short-or-long swap: the same fact, worded for the length.
func (b *Builder) Only(v Variant, lines ...string) *Builder {
	if b.variant != v {
		return b
	}
	return b.Text(lines...)
}

// Variant reports which form is rendering, for a caller assembling items rather than prose.
func (b *Builder) Variant() Variant { return b.variant }

// Skill points the reader at a skill they already have, by NAME, saying what it is for.
//
// Referencing rather than restating is the rule this method exists to make convenient. The skill
// is installed where the prompt gets pasted; a copy pasted in here would be a second definition
// free to drift from the installed one, and it would spend the reader's context on text their
// tools already loaded. So what travels is the name.
//
// It takes a [fmt.Stringer] rather than a string so a caller passes a CHECKED reference - see
// agent.MustSkill - instead of a bare literal nothing verifies. This package stays a prose
// builder and does not know what a skill is beyond a name worth pointing at; whether it exists is
// the referencing package's guarantee to make.
func (b *Builder) Skill(ref fmt.Stringer, purpose string) *Builder {
	return b.Bullet("`%s` - %s", ref, purpose)
}

// Field adds a "- name: value" line, and adds NOTHING when value is empty.
//
// The omission is the point. An unmeasured value has no honest rendering: printed blank it reads
// as a measurement that came back empty, and printed as a zero it accuses. Leaving the line out
// says the only true thing, which is that nobody knows.
func (b *Builder) Field(name, value string) *Builder {
	if value == "" {
		return b
	}
	return b.Bullet("%s: %s", name, value)
}

// Bullet adds one formatted list item.
func (b *Builder) Bullet(format string, a ...any) *Builder {
	b.separate(true)
	b.pending = append(b.pending, "- "+fmt.Sprintf(format, a...))
	b.content = true
	return b
}

// separate keeps a blank line between prose and an adjacent list, which markdown needs to read
// the list as one. Callers write a section as one chain and should not have to remember where the
// blank lines go; getting it wrong renders a list glued to the sentence above it.
func (b *Builder) separate(bullet bool) {
	last := len(b.pending) - 1
	if last < 0 || b.pending[last] == "" {
		return
	}
	if strings.HasPrefix(b.pending[last], "- ") != bullet {
		b.pending = append(b.pending, "")
	}
}

// Items adds up to limit list items and, when it dropped any, a final item saying how many and
// where the rest are. A limit of zero or less adds every item.
//
// The remainder line is not optional, because a truncated list a reader believes is complete is
// worse than no list: they stop looking. Callers pass ordered items, so the cut takes the tail
// rather than an arbitrary slice.
func (b *Builder) Items(items []string, limit int, more string) *Builder {
	shown := items
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, it := range shown {
		b.Bullet("%s", it)
	}
	if dropped := len(items) - len(shown); dropped > 0 {
		b.Bullet("... and %d more. %s", dropped, more)
	}
	return b
}

// String closes the open section and renders the prompt.
//
// It is safe to call more than once and does not end the chain: a caller may render, add another
// section, and render again.
func (b *Builder) String() string {
	b.flush()
	return b.out.String()
}

// flush writes the open section, or discards it when nothing but notes went in.
func (b *Builder) flush() {
	if b.content {
		fmt.Fprintf(&b.out, "\n## %s\n\n", b.title)
		for _, line := range b.pending {
			b.out.WriteString(line)
			b.out.WriteString("\n")
		}
	}
	b.title, b.pending, b.content = "", nil, false
}
