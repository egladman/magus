// Package stamp records which magus build wrote a piece of shared state, and refuses to
// let an older build replace what a newer one wrote.
//
// Shared state is anything magus writes that another checkout or another magus version
// reads: the managed sections in .gitattributes and the hooks dir, the merge driver
// registration, and the user config. An older binary rewriting any of them drops what the
// newer one added, and nothing said so: an old branch's ./magus once rewrote the shared
// registration and dumped a key main had removed into the user config, and the next,
// correct binary refused to start.
package stamp

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"golang.org/x/mod/semver"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// Writer is the magus build that wrote something. A field the build did not stamp is "".
type Writer struct {
	Version string
	Commit  string
	// Date is the commit's date, RFC 3339.
	Date string
}

// Self is the running binary's Writer, from the build identity main puts on ctx. A ctx
// without one, as in a test, yields the zero Writer, which records no stamp and is never
// refused.
func Self(ctx context.Context) Writer {
	b := types.MagusBuildFromContext(ctx)
	return Writer{Version: known(b.Version), Commit: known(b.Commit), Date: known(b.Date)}
}

func known(s string) string {
	if s == "unknown" {
		return ""
	}
	return s
}

// Known reports whether w names a build at all.
func (w Writer) Known() bool { return w.Version != "" || w.Commit != "" }

// String is the stamp as magus writes it, and the form Parse reads back.
func (w Writer) String() string {
	return fmt.Sprintf("magus %s (commit %s, %s)", orUnknown(w.Version), orUnknown(w.Commit), orUnknown(w.Date))
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

var writerRe = regexp.MustCompile(`magus (\S+) \(commit (\S+), (\S+)\)`)

// Parse finds the stamp String wrote anywhere in text. ok is false when text holds none,
// which is what state written before stamps existed looks like.
func Parse(text string) (w Writer, ok bool) {
	m := writerRe.FindStringSubmatch(text)
	if m == nil {
		return Writer{}, false
	}
	return Writer{Version: known(m[1]), Commit: known(m[2]), Date: known(m[3])}, true
}

// Ancestry reports whether ancestor is reachable from descendant, a commit being its own
// ancestor. An error means it could not say, such as a commit the repository lacks.
type Ancestry func(ancestor, descendant string) (bool, error)

// Judge decides whether Self may replace what a recorded Writer wrote.
type Judge struct {
	Self Writer
	// Ancestry orders two commits; nil when no repository is at hand.
	Ancestry Ancestry
}

// Newer reports whether recorded is provably newer than j.Self. Anything that cannot be
// ordered is not newer, so an unstamped or unknown writer on either side never refuses.
//
// The order, first answer wins:
//  1. The same commit is equal.
//  2. Commit ancestry, when both commits are in the repository at hand. It is exact.
//  3. The release each version descends from, by semver: v0.5.0 is newer than
//     v0.4.3-164-gabc. A dev build's `-N-g<sha>` distance is ignored.
//  4. The commit date. Squash merges make main diverge from the branch that built a
//     stamp, and a describe distance counts the branch's commits, so a five-commit branch
//     would outrank the main commit that squashed it. The later commit orders them right.
func (j Judge) Newer(recorded Writer) bool {
	self := j.Self
	if !recorded.Known() || !self.Known() {
		return false
	}
	if recorded.Commit != "" && self.Commit != "" {
		if sameCommit(recorded.Commit, self.Commit) {
			return false
		}
		if j.Ancestry != nil {
			if in, err := j.Ancestry(recorded.Commit, self.Commit); err == nil && in {
				return false
			}
			if in, err := j.Ancestry(self.Commit, recorded.Commit); err == nil && in {
				return true
			}
		}
	}
	rb, sb := releaseBase(recorded.Version), releaseBase(self.Version)
	if rb != "" && sb != "" {
		if c := semver.Compare(rb, sb); c != 0 {
			return c > 0
		}
	}
	rd, rerr := time.Parse(time.RFC3339, recorded.Date)
	sd, serr := time.Parse(time.RFC3339, self.Date)
	if rerr == nil && serr == nil {
		return rd.After(sd)
	}
	return false
}

// sameCommit compares two revisions that may be abbreviated.
func sameCommit(a, b string) bool {
	n := min(len(a), len(b))
	return n >= 7 && a[:n] == b[:n]
}

var describeRe = regexp.MustCompile(`^(v\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)(?:-\d+-g[0-9a-f]+)?(?:-dirty)?$`)

// releaseBase is the release a version descends from: v0.4.3 for v0.4.3-164-gabc-dirty,
// "" for anything that is not a tag or git describe output.
func releaseBase(version string) string {
	m := describeRe.FindStringSubmatch(version)
	if len(m) < 2 || !semver.IsValid(m[1]) {
		return ""
	}
	return m[1]
}

// Check returns a *DowngradeError when recorded is newer than j.Self. file names the
// state in the message.
func (j Judge) Check(file string, recorded Writer) error {
	if j.Newer(recorded) {
		return &DowngradeError{File: file, Recorded: recorded, Writer: j.Self}
	}
	return nil
}

// DowngradeError is a refusal to replace state a newer magus wrote.
type DowngradeError struct {
	File     string
	Recorded Writer
	Writer   Writer
}

func (e *DowngradeError) Error() string {
	return fmt.Sprintf("%s was written by %s, newer than this binary, %s, and replacing it would undo what the newer one wrote. "+
		"Update this binary to at least %s (`%s` for a release, or bring a checkout of magus up to commit %s and rebuild it), "+
		"or rerun this command with a magus at least that new",
		e.File, e.Recorded, e.Writer, orUnknown(e.Recorded.Version), hint.SelfUpdate, orUnknown(e.Recorded.Commit))
}
