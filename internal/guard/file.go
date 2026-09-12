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

// nameSeps are the separators a filename uses to carry a word before the rest of the
// name. A dot is not one of them: `guard.test.ts` is a test twin, not a variant.
var nameSeps = []string{"_", "-"}

// adviseNewFileName fires when a write would CREATE a file in a directory that already
// holds files, or "" when it would not.
//
// adviseNewSourceDir's sibling, one step earlier in the same trigger, and the two are
// mutually exclusive: that rule speaks when the directory is empty, which makes the
// write a new boundary, and this one when it is not, which makes it a new name inside a
// boundary whose conventions already exist.
//
// It SURFACES and does not JUDGE, and that is the whole design rather than modesty. A
// unit creating a file sees the file it came from and not the directory it is landing
// in, so the names already there are the fact it is missing; "that name is unidiomatic"
// is an opinion with no bound on how often it is wrong.
//
// A STUTTER CLAIM was built here and REMOVED, and it is worth saying why so nobody
// rebuilds it. It flagged a name repeating its directory, held unless no sibling carried
// the shape. That predicate cannot separate the two cases it must: `guard_thing.go` in
// internal/guard, which is the defect 484c72310 swept when it renamed 26 files, and
// `go-build.buzz` in spells/examples/go, which is the spell-op naming formula. Both are
// the directory's word plus a discriminator, in a directory where a sibling already
// carries the shape. Telling them apart needs a hardcoded token list, which is what
// adviseNewSourceDir's doc argues against, and holding the claim instead made it silent
// in the one package that proves the defect is real. Silence a reader cannot distinguish
// from approval is worse than no rule at all.
//
// FIRING POLICY: once per session, enrolled as advisoryNewFile. Creating files is
// ordinary work, so an ungated rule would arrive dozens of times in a session where
// adviseNewSourceDir arrives once or never. The lesson is standing rather than
// per-file, and a reader who has it can run ls. No brief on the repeat, because this
// reports a condition and carries no command to run.
//
// THE WRONG-FIRING CASE: a name that was DERIVED rather than chosen. `sourcedir_test.go`
// beside `sourcedir.go` had no naming decision in it, so the list would buy nothing.
// derivedFromSibling suppresses those. The directory's EPONYMOUS file is excepted, since
// a name hung off the entry point (`guard_thing.go` in guard/) is one somebody picked
// rather than one the tree computed, and picking is what this rule speaks to.
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
	return newFileNameAdvice(dir, name, siblings)
}

// fileStem drops the final extension, so a name compares the same across the languages
// one directory may hold.
func fileStem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// derivedFromSibling reports whether stem extends a sibling's name, the shape of a twin
// (`sourcedir_test.go` beside `sourcedir.go`) or a variant (`rusage_darwin.go` beside
// `rusage_unix.go`). The directory answered the naming question when it named the file
// this one hangs off.
//
// The eponymous sibling does not count. Every `guard_*.go` in guard/ extends `guard.go`
// by this test, and treating those as derived would suppress the rule on exactly the
// names a person chose rather than the tree computed.
func derivedFromSibling(stem, base string, siblings []string) bool {
	if stem == "" {
		return false
	}
	for _, sibling := range siblings {
		root := fileStem(sibling)
		if root == "" || root == base || root == stem {
			continue
		}
		for _, sep := range nameSeps {
			if strings.HasPrefix(stem, root+sep) {
				return true
			}
		}
	}
	return false
}


func newFileNameAdvice(dir, name string, siblings []string) string {
	shown, more := siblings, ""
	if len(shown) > siblingsShown {
		shown = shown[:siblingsShown]
		more = fmt.Sprintf(", and %d more", len(siblings)-siblingsShown)
	}
	advice := fmt.Sprintf("magus workspace: this write CREATES `%s`, a NEW FILE in `%s`, which already holds %d:\n  %s%s\n",
		name, dir, len(siblings), strings.Join(shown, ", "), more)
	advice += "Name it against that list rather than by analogy with the file you came from. A unit that sees only its own working directory is how one tree ends up carrying two conventions for the same thing. The list is a fact and not a verdict: the convention is yours to read off it.\n"
	return advice
}
