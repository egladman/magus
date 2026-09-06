package hint

import (
	"fmt"
	"slices"
	"strings"
)

// CheckKeys splits the keys of a schema map into a typo, which is fatal, and keys this
// binary does not recognize at all, which the caller may ignore.
//
// Rejecting the second kind deadlocks a workspace: a magusfile key added upstream aborts
// load, and that takes out `magus run go-build` too, so the workspace cannot build the
// binary that would understand the key.
//
// ignored is only the keys preceding the typo, and MapKeys is insertion-ordered, so a
// caller reports what it dropped above the key it refuses.
func CheckKeys(present, known []string, where string) (ignored []string, err error) {
	for _, k := range present {
		if slices.Contains(known, k) {
			continue
		}
		if sug := Nearest(k, known); sug != "" {
			return ignored, fmt.Errorf("%s: unknown option %q; did you mean %q? (known options: %s)",
				where, k, sug, strings.Join(slices.Sorted(slices.Values(known)), ", "))
		}
		ignored = append(ignored, k)
	}
	return ignored, nil
}

// RejectUnknownKeys errors on ANY key absent from known, for a vocabulary that cannot
// grow. Suggests a near miss where distance finds one; the verdict does not depend on it.
//
// Not every schema map wants CheckKeys. A closed vocabulary has no key from the future to
// tolerate, so tolerating one only converts a typo into an option that silently does
// nothing. The worked example is tool bounds: `minimum` is 4 edits from `min` against a
// threshold of 2, so no suggester reaches it, and under CheckKeys it loaded clean and
// enforced no version window at all.
//
// A vocabulary belongs here only while nothing has ever been ADDED to it. Growing one is
// what makes an unknown key ambiguous, and the moment min/below gains a third member this
// call has to become CheckKeys with a Since column beside it.
func RejectUnknownKeys(present, known []string, where string) error {
	for _, k := range present {
		if slices.Contains(known, k) {
			continue
		}
		sorted := strings.Join(slices.Sorted(slices.Values(known)), ", ")
		if sug := Nearest(k, known); sug != "" {
			return fmt.Errorf("%s: unknown option %q; did you mean %q? (known options: %s)",
				where, k, sug, sorted)
		}
		return fmt.Errorf("%s: unknown option %q (known options: %s)", where, k, sorted)
	}
	return nil
}

// IgnoredKeyAdvice explains an ignored key, so the reader stops hunting a typo that is
// not there.
func IgnoredKeyAdvice() string {
	return "nothing known is close to it, so this magus probably predates it; upgrade with `" +
		SelfUpdate.String() + "`, or delete the key if the workspace does not need it"
}
