package proofread

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// prefilteredPatterns are the patterns findAllIndex and mayMatch serve.
func prefilteredPatterns() []*regexp.Regexp {
	out := []*regexp.Regexp{
		fillerPattern, writtenFillerPattern, wordyPattern, writtenWordyPattern, hedging, dateStyle,
		condescending, presuming, obviously, booster, impatient, markRun, counterfactual, contempt,
		verdictWord, pastAbsolute, motive,
		// A match may start inside a run of spaces, and a long s or a Kelvin
		// sign may stand for an ASCII letter.
		regexp.MustCompile(`[ ]run\b`), regexp.MustCompile(`\s+run\b`), regexp.MustCompile(`(?i)kelvin|simply`),
	}

	for _, tells := range [][]tell{
		signposts, chatbots, leaks, buzzwords, weakBuzzwords, contrasts, vagues, closers, ingTails,
	} {
		for _, t := range tells {
			out = append(out, t.re)
		}
	}

	return out
}

// prefilterTexts are the paragraphs of the case files and benchmark inputs,
// each also in capitals after a word character with every space a run of
// mixed spaces, plus texts that fold outside ASCII.
func prefilterTexts(t *testing.T) []string {
	t.Helper()

	files, err := filepath.Glob("testdata/cases/*.txtar")
	if err != nil {
		t.Fatal(err)
	}

	bench, err := filepath.Glob("testdata/bench/*.txt")
	if err != nil {
		t.Fatal(err)
	}

	texts := []string{"", "ſimply put", "Kelvin, it is crucial", "  just\t\n run  it", "x\xffjust"}

	for _, file := range slices.Concat(files, bench) {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}

		for para := range strings.SplitSeq(string(b), "\n\n") {
			texts = append(texts, para)
		}
	}

	var out []string

	for _, text := range texts {
		out = append(out, text, "a"+strings.ToUpper(strings.ReplaceAll(text, " ", "\t \n"))+"z")
	}

	return out
}

func TestFindAllIndexMatchesRegexp(t *testing.T) {
	texts := prefilterTexts(t)

	for i, re := range prefilteredPatterns() {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()

			for _, text := range texts {
				got, want := findAllIndex(re, text, false), re.FindAllStringIndex(text, -1)
				if !slices.EqualFunc(got, want, slices.Equal) {
					t.Fatalf("findAllIndex(%q, %q) = %v, want %v", re, text, got, want)
				}

				got, want = findAllIndex(re, text, true), re.FindAllStringSubmatchIndex(text, -1)
				if !slices.EqualFunc(got, want, slices.Equal) {
					t.Fatalf("findAllIndex(%q, %q, submatch) = %v, want %v", re, text, got, want)
				}
			}
		})
	}
}

func TestEveryHotPatternHasAPrefilter(t *testing.T) {
	for _, re := range prefilteredPatterns() {
		if loadPrefilter(re) == nil {
			t.Errorf("%q has no prefilter: an alternative opens with no literal", re)
		}
	}
}
