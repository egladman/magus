package proofread

import (
	"cmp"
	"slices"
	"strings"
)

// A commit shape is where a hit sits, so a forge trailer and a parenthesis in
// the subject are not one finding. The rules still report the marks. The
// shapes are what a corpus counts.
const (
	shapeTitleCase     = "title-case"
	shapeForgeTrailer  = "forge-trailer"
	shapeInteriorParen = "interior-paren"
	shapeComma         = "comma"
	shapeBang          = "bang"
	shapeQuestion      = "question"
	shapeSpacedHyphen  = "spaced-hyphen"
	shapeSquashList    = "squash-list"
	shapeProse         = "prose"
)

// shapeMinCount and shapeMinShare are the word-class dictionary's gate
// (dictMinCount and dictMinShare): 20 hits, and 97% of that rule's hits.
// Below either number the shape is residue and the rule stays as it is.
const (
	shapeMinCount = 20
	shapeMinShare = 0.97
)

// commitShape is one hit of a commit rule, named by the rule and where the
// hit sits. The forge trailer and the squash list are shapes even when the
// rule leaves them unreported.
type commitShape struct {
	Rule  Rule
	Shape string
}

// shapeTally is one shape's count against every hit of its rule.
type shapeTally struct {
	commitShape
	Count int
	Hits  int
	Share float64
}

// commitShapes reads text as a commit message and returns each hit by where
// it sits. A conventional prefix is not a hit. A trailing " (#123)" is a
// forge trailer; a parenthesis that remains is interior. A body of only "* "
// lines is a squash list, and one prose line makes the body prose.
func commitShapes(text string) []commitShape {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	subject := subjectLine(input{source: raw})
	if subject == "" {
		return nil
	}

	var out []commitShape

	if titleCaseWord.MatchString(subject) {
		out = append(out, commitShape{Rule: RuleSubjectCase, Shape: shapeTitleCase})
	}

	rest := conventionalPrefix.ReplaceAllString(subject, "")
	if pullRequestSuffix.MatchString(rest) {
		out = append(out, commitShape{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer})
		rest = pullRequestSuffix.ReplaceAllString(rest, "")
	}

	for _, mark := range punctMarks {
		if mark.re.MatchString(rest) {
			out = append(out, commitShape{Rule: RuleSubjectPunct, Shape: punctShape(mark)})
		}
	}

	squash, prose := false, false
	for _, ln := range raw[1:] {
		if strings.TrimSpace(ln) == "" || trailerLine.MatchString(ln) {
			continue
		}
		if squashBullet.MatchString(ln) {
			squash = true
			continue
		}
		prose = true
		break
	}

	switch {
	case prose:
		out = append(out, commitShape{Rule: RuleNoBody, Shape: shapeProse})
	case squash:
		out = append(out, commitShape{Rule: RuleNoBody, Shape: shapeSquashList})
	}

	return out
}

// punctShape is the shape name of a punctuation mark. The parenthesis mark
// is interior because the forge trailer was peeled off before the marks run.
func punctShape(mark punctMark) string {
	switch mark.re.String() {
	case ",":
		return shapeComma
	case "[()]":
		return shapeInteriorParen
	case "!":
		return shapeBang
	case `\?`:
		return shapeQuestion
	case " - ":
		return shapeSpacedHyphen
	default:
		return mark.re.String()
	}
}

// shapesClearing returns the shapes that are one population: at least
// shapeMinCount hits and shapeMinShare of that rule's hits, and the rule
// has another shape. One shape is the rule. The order is rule, then shape.
func shapesClearing(counts map[commitShape]int) []shapeTally {
	hits := map[Rule]int{}
	kinds := map[Rule]int{}
	for s, n := range counts {
		hits[s.Rule] += n
		kinds[s.Rule]++
	}

	var out []shapeTally
	for s, n := range counts {
		total := hits[s.Rule]
		if kinds[s.Rule] < 2 || total == 0 || n < shapeMinCount {
			continue
		}
		share := float64(n) / float64(total)
		if share < shapeMinShare {
			continue
		}
		out = append(out, shapeTally{commitShape: s, Count: n, Hits: total, Share: share})
	}

	slices.SortFunc(out, func(a, b shapeTally) int {
		return cmp.Or(cmp.Compare(a.Rule, b.Rule), cmp.Compare(a.Shape, b.Shape))
	})

	return out
}
