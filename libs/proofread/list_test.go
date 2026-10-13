package proofread

import (
	"reflect"
	"strings"
	"testing"
)

func TestListsGroupItemsByIndentAndMarker(t *testing.T) {
	cases := []struct {
		name, text string
		want       [][]int
	}{
		{"bullets", "- a\n- b\n- c", [][]int{{1, 2, 3}}},
		{"numbers", "1. a\n2. b", [][]int{{1, 2}}},
		{"a wrapped item stays one", "- a\n  wraps\n- b", [][]int{{1, 3}}},
		{"loose items stay one list", "- a\n\n- b", [][]int{{1, 3}}},
		{"a nested list is its own", "- a\n  - x\n  - y\n- b", [][]int{{1, 4}, {2, 3}}},
		{"a paragraph ends a list", "- a\n\nText.\n\n- b", [][]int{{1}, {5}}},
		{"a heading ends a list", "- a\n## H\n- b", [][]int{{1}, {3}}},
		{"another bullet starts another list", "- a\n* b", [][]int{{1}, {2}}},
		{"a bullet ends a numbered list", "1. a\n- b\n2. c", [][]int{{1}, {2}, {3}}},
		{"an item's paragraph keeps its list", "- a\n\n  More on a.\n\n- b", [][]int{{1, 5}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, prose := readMarkdown(strings.Split(tc.text, "\n"), KindReference)

			var got [][]int

			for _, l := range lists(prose, keep) {
				var lines []int
				for _, item := range l.items {
					lines = append(lines, item.head.line)
				}

				got = append(got, lines)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("lists: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListMarksReportsTheItemsOutsideTheListsForm(t *testing.T) {
	runTextCases(t, RuleListMarks, []textCase{
		{"one form", KindReference, "- Keeps the key.\n- Sorts the inputs.\n- Hashes them.", nil},
		{"a lowercase item", KindReference, "- Keeps the key\n- sorts the inputs\n- Hashes them", []string{"2:sorts"}},
		{"a full stop on one", KindReference, "- keeps the key\n- sorts the inputs.\n- hashes them", []string{"2:inputs."}},
		{"a missing full stop", KindReference, "- Keeps the key.\n- Sorts the inputs\n- Hashes them.", []string{"2:inputs"}},
		{"a wrapped item ends on its last line", KindReference, "- Keeps the key.\n- Sorts the\n  inputs\n- Hashes them.",
			[]string{"3:inputs"}},
		{"two items are too few", KindReference, "- Keeps the key.\n- sorts the inputs", nil},
		{"code and links are skipped", KindReference,
			"- Keeps the key.\n- `magus ls`\n- [The guide](g.md)\n- Hashes it.", nil},
		{"code skips only the case", KindReference, "- Keeps the key.\n- `ls` lists them\n- Hashes it.", []string{"2:them"}},
		{"a name keeps its capital", KindReference, "- keeps the key\n- GitHub hosts it\n- hashes it", nil},
		{"a colon introduces", KindReference, "- Keeps the key.\n- Sorts these:\n- Hashes it.", nil},
		{"each nested list on its own", KindReference,
			"- Keeps the key.\n  - one\n  - two\n  - three\n- Sorts it.\n- Hashes it.", nil},
		{"a bold lead", KindChangelog, "- **cache:** Keeps the key.\n- **sort:** Sorts it.\n- **hash:** hashes it.",
			nil},
	})
}

func TestListParallelReportsAnItemOfAnotherShape(t *testing.T) {
	runTextCases(t, RuleListParallel, []textCase{
		{"imperatives", KindGuide, "- Run the build.\n- Open the page.\n- Save the key.", nil},
		{"a noun among verbs", KindReference,
			"- Run the build.\n- Open the page.\n- Save the key.\n- The cache for each run.",
			[]string{"4:The"}},
		{"a verb among noun phrases", KindReference,
			"- A cache for each run.\n- The key of each target.\n- Fast builds on every push.\n- Run the build.",
			[]string{"4:Run"}},
		{"a past tense among the present", KindChangelog,
			"- Adds a flag for the cache.\n- Keeps the key stable.\n- Sorts the inputs before hashing.\n" +
				"- Fixed the race on the lock.",
			[]string{"4:Fixed"}},
		{"no two thirds", KindReference, "- Run the build.\n- The cache for each run.\n- Fast builds on every push.\n" +
			"- Open the page.", nil},
		{"two items are too few", KindReference, "- Run the build.\n- The cache for each run.", nil},
		{"code openers are skipped", KindReference, "- Run the build.\n- `ls` lists them.\n- Open the page.", nil},
		{"a contraction opens with its verb", KindReference,
			"- A cache for each run.\n- The key of each target.\n- Fast builds on every push.\n- Don't run the build.",
			[]string{"4:Don't"}},
		{"a hyphenated opener stays one word", KindReference,
			"- Re-run the build.\n- Re-open the page.\n- Save the key.\n- The cache for each run.",
			[]string{"4:The"}},
	})
}
