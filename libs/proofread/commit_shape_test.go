package proofread

import (
	"reflect"
	"testing"
)

func TestCommitShapesNameWhereAHitSits(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []commitShape
	}{
		{
			name: "a forge trailer",
			text: "fix(harness): close the gaps (#615)",
			want: []commitShape{{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer}},
		},
		{
			name: "a parenthesis before the trailer",
			text: "keep the key (stable) (#615)",
			want: []commitShape{
				{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer},
				{Rule: RuleSubjectPunct, Shape: shapeInteriorParen},
			},
		},
		{
			name: "a comma",
			text: "keep the key, stable",
			want: []commitShape{{Rule: RuleSubjectPunct, Shape: shapeComma}},
		},
		{
			name: "a bang",
			text: "keep the key!",
			want: []commitShape{{Rule: RuleSubjectPunct, Shape: shapeBang}},
		},
		{
			name: "a question",
			text: "keep the key?",
			want: []commitShape{{Rule: RuleSubjectPunct, Shape: shapeQuestion}},
		},
		{
			name: "a spaced hyphen",
			text: "keep the key - stable",
			want: []commitShape{{Rule: RuleSubjectPunct, Shape: shapeSpacedHyphen}},
		},
		{
			name: "title case",
			text: "Fix the port",
			want: []commitShape{{Rule: RuleSubjectCase, Shape: shapeTitleCase}},
		},
		{name: "a capital after a conventional prefix", text: "feat: Add a hook"},
		{
			name: "a squash list",
			text: "fix(harness): close the gaps (#615)\n\n* close the gaps\n\n* leave the lock",
			want: []commitShape{
				{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer},
				{Rule: RuleNoBody, Shape: shapeSquashList},
			},
		},
		{
			name: "prose",
			text: "keep the key\n\nThe detail belongs in the description.",
			want: []commitShape{{Rule: RuleNoBody, Shape: shapeProse}},
		},
		{
			name: "prose after a squash list",
			text: "keep the key\n\n* close the gaps\n\nThe detail belongs in the description.",
			want: []commitShape{{Rule: RuleNoBody, Shape: shapeProse}},
		},
		{name: "a trailer", text: "keep the key\n\nSigned-off-by: A Person <a@example.com>"},
		{name: "a merge", text: "Merge branch 'main'\nThe merge brought the key."},
		{name: "a scope", text: "feat(cache): keep the key"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commitShapes(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("commitShapes = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestShapeGateClearsAtTwentyHitsAndNinetySevenPercent(t *testing.T) {
	trailer := commitShape{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer}
	comma := commitShape{Rule: RuleSubjectPunct, Shape: shapeComma}

	clears := shapesClearing(map[commitShape]int{trailer: 97, comma: 3})
	if len(clears) != 1 || clears[0].Shape != shapeForgeTrailer || clears[0].Share < shapeMinShare {
		t.Errorf("97 of 100 = %+v, want the trailer at the share floor", clears)
	}

	if got := shapesClearing(map[commitShape]int{trailer: 19}); len(got) != 0 {
		t.Errorf("19 of 19 = %+v, want none: the count floor is 20", got)
	}

	if got := shapesClearing(map[commitShape]int{trailer: 96, comma: 4}); len(got) != 0 {
		t.Errorf("96 of 100 = %+v, want none: the share floor is 0.97", got)
	}

	title := commitShape{Rule: RuleSubjectCase, Shape: shapeTitleCase}
	if got := shapesClearing(map[commitShape]int{title: 74}); len(got) != 0 {
		t.Errorf("74 of 74 = %+v, want none: one shape is the rule", got)
	}
}
