package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/project"
)

// siblingsShown bounds the list. A directory of 70 files says what it has to say about
// its own convention in the first dozen names.
const siblingsShown = 12

// stutterSeps are the separators a filename uses to carry a word before the rest of the
// name. A dot is not one of them: `guard.test.ts` is a test twin, not a repeat.
var stutterSeps = []string{"_", "-"}

// adviseNewFileName fires when a write would CREATE a file in a directory that already
// holds files, or "" when it would not.
//
// adviseNewSourceDir's sibling, one step earlier in the same trigger, and the two are
// mutually exclusive: that rule speaks when the directory is empty, which makes the
// write a new boundary, and this one when it is not, which makes it a new name inside a
// boundary whose conventions already exist.
//
// It SURFACES and does not JUDGE. A unit creating a file sees the file it came from and
// not the directory it is landing in, so the names already there are the fact it is
// missing; "that name is unidiomatic" is an opinion with no bound on how often it is
// wrong. The one structural claim here is STUTTER, a name that repeats its directory,
// stated as a fact about the siblings rather than as taste. This package is the worked
// example: 484c72310 renamed 26 files to strip a `guard_` prefix from internal/guard,
// and the first of those files is where that sweep could have been avoided.
//
// FIRING POLICY: once per session, enrolled as advisoryNewFile. Creating files is
// ordinary work, so an ungated rule would arrive dozens of times in a session where
// adviseNewSourceDir arrives once or never. The lesson is standing rather than
// per-file, and a reader who has it can run ls. No brief on the repeat, because this
// reports a condition and carries no command to run.
//
// TWO WRONG-FIRING CASES, measured over this tree's 2,101 source files:
//  1. A name that was DERIVED rather than chosen. `sourcedir_test.go` beside
//     `sourcedir.go` had no naming decision in it. derivedFromSibling suppresses those,
//     excepting the directory's eponymous file, since taking THAT name as a prefix is
//     the stutter itself.
//  2. Stutter that is the local CONVENTION. 31 files here repeat their directory and
//     nearly every one is deliberate: `go-build.buzz` in spells/examples/go is the
//     spell-op naming formula, `run_unix.go` in internal/proc/run is a build
//     constraint. The stutter line is held unless NO sibling carries the shape, which
//     keeps it checkable and costs a false negative in a package that stutters already.
//
// THE FLOOR: one firing per session, against a budget where fire-once recovered 52% of
// advisory bytes. The observable is the created path, since a session shown this and
// then writing a different basename took it. Below the 21% internal/hint/next.go
// measured for an unhinted breadcrumb it teaches nobody, and the answer then is to
// delete it rather than reword it.
func adviseNewFileName(path string) string {
	clean := strings.TrimSpace(path)
	dir, ok := workspaceRelativeDir(clean)
	if !ok {
		return ""
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if project.IsIgnoreDir(seg) || slices.Contains(fixtureDirs, seg) {
			return ""
		}
	}
	// Runs BEFORE the write, so a file that is already there means an edit, and an edit
	// picks no name.
	if _, err := os.Stat(clean); err == nil {
		return ""
	}
	entries, err := os.ReadDir(filepath.FromSlash(dir))
	if err != nil {
		return "" // an absent directory is adviseNewSourceDir's case; an unreadable one, nobody's
	}
	siblings := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			siblings = append(siblings, entry.Name())
		}
	}
	if len(siblings) == 0 {
		return ""
	}
	base := dir
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		base = dir[i+1:]
	}
	name := filepath.Base(clean)
	if derivedFromSibling(fileStem(name), base, siblings) {
		return ""
	}
	stutter := stutters(fileStem(name), base) && !slices.ContainsFunc(siblings, func(s string) bool {
		return stutters(fileStem(s), base)
	})
	return newFileNameAdvice(dir, base, name, siblings, stutter)
}

// fileStem drops the final extension, so a name compares the same across the languages
// one directory may hold.
func fileStem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// stutters reports whether stem carries base as a leading or trailing word.
//
// The EPONYMOUS file is excluded: `guard.go` in guard/, `mod.rs`, `__init__.py` and
// `index.ts` are how several languages spell a directory's entry point. Only a name
// that carries the directory PLUS a discriminator repeats anything.
func stutters(stem, base string) bool {
	if base == "" || stem == "" || stem == base {
		return false
	}
	for _, sep := range stutterSeps {
		if strings.HasPrefix(stem, base+sep) || strings.HasSuffix(stem, sep+base) {
			return true
		}
	}
	return false
}

// derivedFromSibling reports whether stem extends a sibling's name, the shape of a twin
// (`sourcedir_test.go` beside `sourcedir.go`) or a variant (`rusage_darwin.go` beside
// `rusage_unix.go`). The directory answered the naming question when it named the file
// this one hangs off.
//
// The eponymous sibling does not count, and that exception is what keeps the stutter
// check alive: every `guard_*.go` in guard/ is derived from `guard.go` by this test.
func derivedFromSibling(stem, base string, siblings []string) bool {
	if stem == "" {
		return false
	}
	for _, sibling := range siblings {
		root := fileStem(sibling)
		if root == "" || root == base || root == stem {
			continue
		}
		for _, sep := range stutterSeps {
			if strings.HasPrefix(stem, root+sep) {
				return true
			}
		}
	}
	return false
}

// trimStutter spells name without the repeated directory word, so the advisory can show
// the alternative instead of describing it.
func trimStutter(name, base string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for _, sep := range stutterSeps {
		if trimmed := strings.TrimPrefix(stem, base+sep); trimmed != stem {
			return trimmed + ext
		}
		if trimmed := strings.TrimSuffix(stem, sep+base); trimmed != stem {
			return trimmed + ext
		}
	}
	return name
}

func newFileNameAdvice(dir, base, name string, siblings []string, stutter bool) string {
	shown, more := siblings, ""
	if len(shown) > siblingsShown {
		shown = shown[:siblingsShown]
		more = fmt.Sprintf(", and %d more", len(siblings)-siblingsShown)
	}
	advice := fmt.Sprintf("magus workspace: this write CREATES `%s`, a NEW FILE in `%s`, which already holds %d:\n  %s%s\n",
		name, dir, len(siblings), strings.Join(shown, ", "), more)
	advice += "Name it against that list rather than by analogy with the file you came from. A unit that sees only its own working directory is how one tree ends up carrying two conventions for the same thing. The list is a fact and not a verdict: the convention is yours to read off it.\n"
	if stutter {
		advice += fmt.Sprintf("`%s` also repeats its directory name (`%s`), which no file in there does today. `%s` is the same name without the repeat.\n",
			name, base, trimStutter(name, base))
	}
	return advice
}
