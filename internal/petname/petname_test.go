package petname

import (
	"fmt"
	"regexp"
	"sort"
	"testing"
)

var nameShape = regexp.MustCompile(`^[a-z]+-[a-z]+$`)
var suffixedShape = regexp.MustCompile(`^[a-z]+-[a-z]+-[a-z]{2}$`)
var wordShape = regexp.MustCompile(`^[a-z]+$`)

// Pinned literals: a change to the hash or either word list must fail this
// test rather than silently renaming every job that used the old seed.
func TestOfDeterminism(t *testing.T) {
	cases := []struct {
		seed string
		want string
	}{
		{"", "cold-vestige"},
		{"alpha", "everlasting-scholar"},
		{"beta", "dappled-citadel"},
		{"wave3-move", "hushed-drake"},
		{"job-42", "sullen-behemoth"},
		{"adj3-job2", "ominous-spirit"},
	}

	for _, c := range cases {
		got := Of(c.seed)
		if got != c.want {
			t.Errorf("Of(%q) = %q, want %q", c.seed, got, c.want)
		}
		if again := Of(c.seed); again != got {
			t.Errorf("Of(%q) = %q then %q; not stable across calls", c.seed, got, again)
		}
	}
}

func TestOfShape(t *testing.T) {
	for i := 0; i < 200; i++ {
		seed := fmt.Sprintf("shape-seed-%d", i)
		if name := Of(seed); !nameShape.MatchString(name) {
			t.Errorf("Of(%q) = %q, does not match %s", seed, name, nameShape)
		}
	}
}

func TestOfWithSuffixShape(t *testing.T) {
	for n := 0; n < 5; n++ {
		for i := 0; i < 50; i++ {
			seed := fmt.Sprintf("suffix-seed-%d", i)
			if name := OfWithSuffix(seed, n); !suffixedShape.MatchString(name) {
				t.Errorf("OfWithSuffix(%q, %d) = %q, does not match %s", seed, n, name, suffixedShape)
			}
		}
	}
}

// OfWithSuffix must vary its output with n so a caller can retry past a
// collision; a suffix that ignored n would defeat the whole purpose of the
// function.
func TestOfWithSuffixVariesByN(t *testing.T) {
	seed := "collision-seed"
	seen := map[string]bool{}
	for n := 0; n < 10; n++ {
		seen[OfWithSuffix(seed, n)] = true
	}
	if len(seen) < 8 {
		t.Errorf("OfWithSuffix(%q, n) for n in [0,10) produced only %d distinct names, want at least 8", seed, len(seen))
	}
}

// Distribution sanity: over many distinct seeds, no single name should
// dominate the draws and both word lists should be broadly exercised. A
// hash collapsing onto one bucket, or onto a narrow band of either list,
// fails this even though every individual name still matches the shape
// regexp above.
func TestOfDistribution(t *testing.T) {
	const n = 5000
	nameCounts := map[string]int{}
	adjSeen := map[string]bool{}
	nounSeen := map[string]bool{}

	for i := 0; i < n; i++ {
		seed := fmt.Sprintf("dist-seed-%d", i)
		name := Of(seed)
		nameCounts[name]++

		adjIdx := hash(adjTag+seed) % uint64(len(adjectives))
		nounIdx := hash(nounTag+seed) % uint64(len(nouns))
		adjSeen[adjectives[adjIdx]] = true
		nounSeen[nouns[nounIdx]] = true
	}

	maxShare := n / 100 // no single full name may take more than 1% of the draws
	for name, count := range nameCounts {
		if count > maxShare {
			t.Errorf("name %q drawn %d/%d times, exceeds the 1%% (%d) bound", name, count, n, maxShare)
		}
	}

	minAdjCoverage := len(adjectives) * 9 / 10 // 90% of the adjective list must appear
	if len(adjSeen) < minAdjCoverage {
		t.Errorf("only %d/%d adjectives appeared over %d seeds, want at least %d (90%%)", len(adjSeen), len(adjectives), n, minAdjCoverage)
	}

	minNounCoverage := len(nouns) * 9 / 10 // 90% of the noun list must appear
	if len(nounSeen) < minNounCoverage {
		t.Errorf("only %d/%d nouns appeared over %d seeds, want at least %d (90%%)", len(nounSeen), len(nouns), n, minNounCoverage)
	}
}

func TestWordLists(t *testing.T) {
	for _, list := range []struct {
		name  string
		words []string
	}{
		{"adjectives", adjectives},
		{"nouns", nouns},
	} {
		t.Run(list.name, func(t *testing.T) {
			seen := make(map[string]bool, len(list.words))
			for _, w := range list.words {
				if !wordShape.MatchString(w) {
					t.Errorf("%q is not plain ASCII lowercase letters", w)
				}
				if seen[w] {
					t.Errorf("%q appears more than once", w)
				}
				seen[w] = true
			}
			if !sort.StringsAreSorted(list.words) {
				t.Errorf("%s is not sorted", list.name)
			}
		})
	}
}
