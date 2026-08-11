package std

import (
	"context"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

//go:generate go run ../cmd/magus-utils bindings -module sort -lang buzz -out ../internal/interp/bindings/gen/sort.go

func init() { Register(Sort) }

// Sort is the "sort" host module: the orderings a build tool actually needs over
// a list of strings.
//
// Buzz's list.sort takes a comparator, so ordering is not strictly missing - but
// the language has no `<` on str, which means even a plain alphabetical sort is a
// hand-written byte loop (docs/lib/text.buzz carries one, strLess). strings.compare
// gave that a primitive; this gives the three orderings worth not rewriting.
//
// Each RETURNS A NEW LIST rather than sorting in place. list.sort mutates and
// requires a `mut` list, which means a caller holding a `final` list from
// vcs.changed_files or archive.list has to copy it first. Returning a fresh list
// makes the common case one call, and leaves the in-place form available for a
// caller that wants it.
var Sort = Module{
	Name: "sort",
	WASM: true,
	Doc:  "Ordering for string lists: lexicographic, natural (digit-aware), and semver.",
	Methods: []Method{
		{
			Name:    "strings",
			Doc:     "Return a new list ordered lexicographically by byte. The plain alphabetical sort, and the one to reach for when the goal is a stable order rather than a meaningful one - a listing that must not change between runs.",
			Args:    []Arg{{Name: "items", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    SortStrings,
		},
		{
			Name:    "natural",
			Doc:     "Return a new list ordered so embedded numbers compare as numbers: file2 before file10, where a lexicographic sort puts file10 first. Use it for anything a human will read - filenames, shard names, versioned directories - and note it is NOT a version sort; semver is.",
			Args:    []Arg{{Name: "items", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    SortNatural,
		},
		{
			Name:    "semver",
			Doc:     "Return a new list ordered by semantic version, oldest first, so v1.9.0 precedes v1.10.0 and a prerelease precedes its release. Accepts tags with or without a leading v. Anything that is not a valid version sorts AFTER every valid one, in lexicographic order among themselves - so a stray tag is visible at the end rather than silently reordering the releases.",
			Args:    []Arg{{Name: "items", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    SortSemver,
		},
	},
}

// SortStrings returns a new list ordered lexicographically.
func SortStrings(_ context.Context, items []string) ([]string, error) {
	out := append([]string(nil), items...)
	sort.Strings(out)
	return out, nil
}

// SortNatural returns a new list with embedded digit runs compared numerically.
func SortNatural(_ context.Context, items []string) ([]string, error) {
	out := append([]string(nil), items...)
	sort.SliceStable(out, func(i, j int) bool { return naturalLess(out[i], out[j]) })
	return out, nil
}

// naturalLess compares a and b treating each run of digits as one number.
//
// Digit runs are compared by VALUE with leading zeros ignored for the comparison
// but used as a tiebreak, so "01" and "1" are ordered deterministically rather
// than by whichever the sort happened to see first. Comparing the runs as numbers
// via their length-then-content avoids parsing, so an arbitrarily long digit run
// (a timestamp, a hash-like id) cannot overflow.
func naturalLess(a, b string) bool {
	for len(a) > 0 && len(b) > 0 {
		ad, bd := isDigit(a[0]), isDigit(b[0])
		if ad != bd {
			// A digit sorts before a letter, so "file2" precedes "fileA".
			return ad
		}
		if !ad {
			if a[0] != b[0] {
				return a[0] < b[0]
			}
			a, b = a[1:], b[1:]
			continue
		}
		ar, an := digitRun(a)
		br, bn := digitRun(b)
		if ar != br {
			// Trimmed of leading zeros, a longer run is the larger number; equal
			// lengths compare as text, which for digits is numeric order.
			if len(ar) != len(br) {
				return len(ar) < len(br)
			}
			return ar < br
		}
		// Same value: the one written with fewer leading zeros sorts first, purely
		// so the order is total and stable.
		if an != bn {
			return an < bn
		}
		a, b = a[an:], b[bn:]
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// digitRun returns the leading digit run of s trimmed of leading zeros, and the
// full untrimmed length of that run.
func digitRun(s string) (trimmed string, n int) {
	for n < len(s) && isDigit(s[n]) {
		n++
	}
	return strings.TrimLeft(s[:n], "0"), n
}

// SortSemver returns a new list ordered by semantic version, oldest first.
func SortSemver(_ context.Context, items []string) ([]string, error) {
	out := append([]string(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		ai, bi := semverCanonical(out[i]), semverCanonical(out[j])
		aValid, bValid := semver.IsValid(ai), semver.IsValid(bi)
		switch {
		case aValid && bValid:
			if c := semver.Compare(ai, bi); c != 0 {
				return c < 0
			}
			// Same version written two ways ("1.2.3" and "v1.2.3"): order by the
			// original text so the result is total.
			return out[i] < out[j]
		case aValid != bValid:
			// Invalid entries collect at the END, where they are visible.
			return aValid
		default:
			return out[i] < out[j]
		}
	})
	return out, nil
}

// semverCanonical adds the leading v that golang.org/x/mod/semver requires, so a
// tag written either way compares.
func semverCanonical(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}
