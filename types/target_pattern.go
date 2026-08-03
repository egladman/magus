package types

import (
	"regexp"
	"slices"
	"strings"
)

// MatchTargetPatterns selects target names against ctx.glob's pattern list, sorted and
// deduplicated so a name matched by two patterns runs once and the order never depends on
// map iteration.
//
// Three pattern forms, and the third is why this function exists at all:
//
//   - "build"          suffix shorthand: every name ending in "-build" (^.*-build$). It does
//     NOT match a target named exactly "build".
//   - "*-generate"     glob: "*" is the only metacharacter, anchored end to end.
//   - "!site-generate" negation: subtracts from whatever the other patterns matched.
//
// Negation exists because the alternative was to rename around the matcher. A target that
// legitimately ends in "-generate" but must stay out of `ctx.needs(ctx.glob("*-generate"))`
// previously had exactly one remedy - do not name it that - and the reason was invisible at
// both the glob and the definition.
//
// A negation takes a NAME or a GLOB, never suffix shorthand: "!site-generate" excludes the
// target actually called site-generate. Making it suffix shorthand instead would have it
// compile to ^.*-site-generate$ and silently exclude nothing, which is the one outcome a
// subtraction must never produce. Excluding a whole family is still available, spelled the
// same way it is included: "!*-generate". The include shorthand is deliberately left alone -
// widening it to match bare names too would make ctx.glob("generate") match the `generate`
// target that contains it, turning a convenience into self-recursion.
//
// Exclusions apply to the union of the includes regardless of order, so
// ("*-generate", "!site-generate") and ("!site-generate", "*-generate") are the same set.
// Patterns that are ONLY negations select nothing: subtracting from an empty set is empty,
// not "everything else" - a glob that silently grew to the whole workspace because its one
// positive pattern was deleted is a worse failure than matching nothing.
//
// One implementation, deliberately. The runtime binding, the dry-run tracer, and the static
// describe extractor each carried their own copy of the compile step, each commented as
// mirroring the others - three places to update in lockstep for a matcher whose whole job is
// that the traced, described, and executed edge sets agree.
func MatchTargetPatterns(names, patterns []string) []string {
	include, exclude := compileTargetPatterns(patterns)
	if len(include) == 0 {
		return nil
	}
	var matched []string
	for _, name := range names {
		if !matchesAny(include, name) || matchesAny(exclude, name) {
			continue
		}
		if !slices.Contains(matched, name) {
			matched = append(matched, name)
		}
	}
	slices.Sort(matched)
	return matched
}

// compileTargetPatterns splits a pattern list into the anchored regexps to include and the
// ones to subtract. Every pattern is QuoteMeta'd before "*" is translated, so an authored
// regexp is matched as literal text: the surface is glob, not regex, and the compiled result
// is always valid however the pattern was written.
func compileTargetPatterns(patterns []string) (include, exclude []*regexp.Regexp) {
	for _, pat := range patterns {
		if negated, ok := strings.CutPrefix(pat, "!"); ok {
			exclude = append(exclude, regexp.MustCompile(anchoredPattern(negated, false)))
			continue
		}
		include = append(include, regexp.MustCompile(anchoredPattern(pat, true)))
	}
	return include, exclude
}

// anchoredPattern renders one pattern as an anchored regexp source. suffixShorthand governs
// only the no-"*" case: includes read a bare word as "-<word>" at the end of a name, while a
// negation reads it as the whole name.
func anchoredPattern(pat string, suffixShorthand bool) string {
	quoted := regexp.QuoteMeta(pat)
	if !strings.Contains(pat, "*") {
		if suffixShorthand {
			return `^.*-` + quoted + `$`
		}
		return `^` + quoted + `$`
	}
	return `^` + strings.ReplaceAll(quoted, `\*`, `.*`) + `$`
}

func matchesAny(res []*regexp.Regexp, name string) bool {
	for _, re := range res {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
