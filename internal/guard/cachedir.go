package guard

import (
	"cmp"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
)

// The checkout's own magus cache dir, on both surfaces.
//
// Every marker in here is an INPUT to the verdicts the agent is graded by, so an agent
// that edits them rewrites its own evidence, and nothing in a later verdict says it
// happened. The rule therefore holds for every role, unbound sessions included: the lease
// rules protect a lane somebody handed out, this one protects the thing that decides
// whether any lane was checked at all.
//
// Reads are untouched, and internal/guard/parse.go's reader list is what separates them.
// That list is where the rule fires wrongly, in two shapes worth knowing before reading a
// verdict: a command nobody listed as a reader is refused for merely NAMING a path in the
// dir (`echo .magus/logs/x.log`), and a `.magus` segment is matched wherever it appears,
// so a write into a DIFFERENT checkout's cache dir is refused here too. Both are the safe
// direction, and the denial says reading is fine.

// workspaceCacheDirName is the cache dir's default name, matched literally in addition to
// the resolved location.
//
// Both, because the two answer different failures. The resolved path is the real dir when
// MAGUS_CACHE_DIR or cache.dir has moved it; the literal name is what a command spells
// when the resolution is unavailable, which is any hook whose workspace magus could not
// locate. Matching only one of them leaves a deny that fails silently open.
const workspaceCacheDirName = ".magus"

// cacheDirSegmentRe matches the cache dir's name as a PATH SEGMENT anywhere in a word.
//
// Anywhere, not as a prefix, because the word is not always a path: an interpreter's
// inline script carries the path inside a program (`python3 -c "open('.magus/lease','a')"`),
// and a word whose value came from a parameter renders with the expansion dropped
// (`"$REPO/.magus/lease"` renders `/.magus/lease`), which resolves against nothing. A
// sibling name is still not a match: `.magus-notes` ends the segment with a character the
// pattern refuses.
var cacheDirSegmentRe = regexp.MustCompile(
	`(?:^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(workspaceCacheDirName) + `(?:/|$|[^A-Za-z0-9_./-])`)

// namesWorkspaceCacheDir reports whether a word points into the workspace's magus cache
// dir, either by resolving inside the dir magus located or by carrying its name as a path
// segment. A relative path is read against the directory the tool call RUNS in, which is
// what the shell would do, falling back to the workspace root when the host reported none.
func namesWorkspaceCacheDir(location location, candidate string) bool {
	p := strings.TrimSpace(candidate)
	if p == "" {
		return false
	}
	if cacheDirSegmentRe.MatchString(filepath.ToSlash(p)) {
		return true
	}
	if location.cacheDir == "" {
		return false
	}
	abs := filepath.Clean(filepath.FromSlash(p))
	if !filepath.IsAbs(abs) {
		dir := cmp.Or(location.dir, location.workspace)
		if dir == "" {
			return false
		}
		abs = filepath.Join(dir, abs)
	}
	// Symlinks resolved on both sides, for the reason denyNotesWrite records: on macOS a
	// tmpdir-rooted workspace yields the dir under one spelling and the incoming path
	// under the other, and a deny that compares them literally looks enforced while
	// passing everything.
	rel, err := filepath.Rel(resolveSymlinks(location.cacheDir), resolveSymlinks(abs))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// advisoryMarkerDirName names the marker directory for this denial's prose. hint keeps
// its own copy of this literal unexported, so this one exists only to be printed.
const advisoryMarkerDirName = "advisories"

// cacheDirDenial is the single text both surfaces refuse with, naming what was aimed at the
// dir. One text because the mistake is one mistake however it is spelled: a host's editor
// tool and a shell redirect reach the same bytes.
func cacheDirDenial(what string) string {
	return fmt.Sprintf("magus guard denied a write to %s, which is inside this checkout's magus cache dir. magus is the only writer of it.\n\n"+
		"That directory is not a pile of build leftovers any more. `%s` records which lease this checkout is bound to, `%s/` holds the fire-once advisory markers, the touched-project set, and the served-next journal whose entries pre-authorize commands, and the activity trail, run logs, outputs and locks sit beside them. The guard's verdicts are computed FROM those files, so editing one rewrites the evidence you are being graded by and no later verdict says so.\n\n"+
		"The verbs that do what you were probably after:\n"+
		"  `"+hint.JobExec.With("<job>")+"` takes a job's lease here, and writes the marker for you.\n"+
		"  `"+hint.Clean.String()+"` removes the declared outputs.\n"+
		"  `"+hint.QueryOutput.With("<ref>")+"` prints a run's captured log.\n"+
		"`"+hint.Session.With("hook")+"` maintains its own markers and never needs you to edit them.\n"+
		"READING in there is fine; it is writing that belongs to magus.",
		what, job.LeaseMarkerName, advisoryMarkerDirName)
}

// denyCacheDirPath is the path surface: the reason a file write into the cache dir is
// refused, or "" for every other path.
//
// Ranked above the lease rules in Judge, so a bound worker whose lane happens to cover
// the dir reads this rather than a lane verdict about the same path.
func denyCacheDirPath(location location, writePath string) string {
	if !namesWorkspaceCacheDir(location, writePath) {
		return ""
	}
	return cacheDirDenial(strings.TrimSpace(writePath))
}

// denyCacheDirCommand is the command surface: the reason a shell line writes into the
// cache dir, or "" when it does not.
//
// Two shapes reach those bytes. A redirect names the file directly, and a command takes it
// as an operand. Both are read off the parsed line rather than matched as text, for the
// reason internal/guard/parse.go's doc gives: a quoted string that merely NAMES the marker is
// not a write to it.
func denyCacheDirCommand(location location, command string) string {
	for _, candidate := range writeTargetCandidates(command, 0) {
		if namesWorkspaceCacheDir(location, candidate) {
			return cacheDirDenial(candidate)
		}
	}
	return ""
}

// rankCacheDirWrite ranks the cache-dir reason against the verdict the other command rules
// reached.
//
// It OUTRANKS an existing deny, which no other rule here does. `sed -i .magus/lease` earns
// the in-place refusal too, and that text sends the reader to an editor tool, which is the
// same write through the surface that would refuse it again.
func rankCacheDirWrite(v BashVerdict, reason string) BashVerdict {
	if reason == "" {
		return v
	}
	return BashVerdict{Deny: reason, Rule: denyRule{Name: denyRuleCacheDirWrite}}
}
