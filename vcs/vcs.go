package vcs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	semver "github.com/Masterminds/semver/v3"
	"github.com/egladman/magus/types"
)

// builtin is probed IN ORDER by autodetect, so the most specific marker has to come
// first. `jj git init` writes both .jj and .git (git is jj's storage backend, not a
// second VCS), so a colocated repo satisfies git's claim too. With git first, every
// colocated jj workspace resolved to git, and the revision magus recorded was git's
// HEAD, which lags jj's working-copy commit (@) until jj syncs refs. That put a
// revision on an output ref describing a tree other than the one built, and
// `magus x <ref>` compared against it.
//
// .jj, .hg and .sl are unambiguous: their presence means that VCS is driving the
// working copy. .git is the fallback precisely because another tool may have created
// it. gitVCS is LAST because it is also the default when nothing claims the directory
// (see Resolve).
//
// Sapling's position in the list is not load-bearing the way jj's is: `sl clone` of a
// git repository writes .sl and NO .git, so unlike a colocated jj workspace there is no
// tree that satisfies both claims and no ordering that could resolve it wrongly. It
// sits with the other unambiguous markers so the rule reads as one rule.
var builtin = []types.VCSDriver{jjVCS{}, hgVCS{}, saplingVCS{}, gitVCS{}}

// IsSecondaryCheckout reports whether dir is a second checkout of a repository
// under any supported VCS (a git linked worktree, an `hg share`, a jj secondary
// workspace). Discovery skips such dirs so a repo's projects and spells are not
// re-indexed and made to shadow the originals (MGS1002). Each backend matches its
// own on-disk signature, so detection favors no single VCS and needs no resolution
// step (the active VCS is not yet known when discovery walks the tree).
func IsSecondaryCheckout(dir string) bool {
	for _, v := range builtin {
		if v.IsSecondaryCheckout(dir) {
			return true
		}
	}
	return false
}

// Resolve picks the active VCS for root: disabled → explicit → auto (claim dir) → default (git).
// Base ref: runtimeBase → opts.BaseRef → MAGUS_VCS_BASE_REF → per-VCS env → built-in default.
func Resolve(_ context.Context, root, runtimeBase string, opts types.VCSOptions) (types.VCSResolution, error) {
	if opts.Enabled != nil && !*opts.Enabled {
		return types.VCSResolution{Source: types.VCSSourceDisabled}, nil
	}
	if opts.Enabled == nil && os.Getenv("MAGUS_VCS_ENABLED") == "false" {
		return types.VCSResolution{Source: types.VCSSourceDisabled}, nil
	}

	name := opts.Name
	if name == "" {
		name = os.Getenv("MAGUS_VCS_NAME")
	}

	var (
		v      types.VCSDriver
		source types.VCSSource
	)

	if name != "" {
		impl, ok := lookupImpl(name)
		if !ok {
			return types.VCSResolution{}, fmt.Errorf("%w: %q", types.ErrVCSUnknown, name)
		}
		v = impl
		source = types.VCSSourceExplicit
	} else {
		for _, e := range builtin {
			if claimsExist(root, e.Claims()) {
				v = e
				name = e.Name()
				source = types.VCSSourceAuto
				break
			}
		}
		if v == nil {
			// Nothing claimed the directory. The default is git, named explicitly
			// rather than taken as builtin[0], which now heads a list ordered by
			// marker specificity rather than by which VCS is the sane fallback.
			v = gitVCS{}
			name = v.Name()
			source = types.VCSSourceDefault
		}
	}

	globalBaseRef := opts.BaseRef
	if globalBaseRef == "" {
		globalBaseRef = os.Getenv("MAGUS_VCS_BASE_REF")
	}
	perVCSBaseRef := os.Getenv(perVCSEnv(name, "BASE_REF"))

	base := chooseBase(runtimeBase, globalBaseRef, perVCSBaseRef, v.Base())

	return types.VCSResolution{Name: name, Source: source, Base: base, VCS: v}, nil
}

func lookupImpl(name string) (types.VCSDriver, bool) {
	for _, v := range builtin {
		if v.Name() == name {
			return v, true
		}
	}
	return nil, false
}

// InstallableVCSes returns the names of built-in VCS drivers that support
// merge-driver installation.
func InstallableVCSes() []string {
	var names []string
	for _, v := range builtin {
		if installsMergeDriver(v) {
			names = append(names, v.Name())
		}
	}
	return names
}

// Installer returns the merge-driver installer for the named VCS, or (nil, false) for an
// unknown name or a backend that cannot install one.
func Installer(name string) (types.MergeDriverInstaller, bool) {
	v, ok := lookupImpl(name)
	if !ok || !installsMergeDriver(v) {
		return nil, false
	}
	return v, true
}

// installsMergeDriver asks v through a read, under a context already cancelled, so nothing
// runs: a backend that declines answers with its *VCSUnsupportedError before looking at
// the context, and one that installs fails to start its read.
func installsMergeDriver(v types.VCSDriver) bool {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := v.MergeDriverCommand(ctx, "")
	var declined *types.VCSUnsupportedError
	return !errors.As(err, &declined)
}

func chooseBase(runtime, global, perVCS, def string) string {
	if runtime != "" {
		return runtime
	}
	if global != "" {
		return global
	}
	if perVCS != "" {
		return perVCS
	}
	if def != "" {
		return def
	}
	return "origin/main"
}

func perVCSEnv(name, suffix string) string {
	return "MAGUS_VCS_" + strings.ToUpper(name) + "_" + suffix
}

// checkRef rejects a base ref / rev / sha that begins with "-", which a VCS would
// otherwise read as a flag (argument injection) when passed as a standalone token.
func checkRef(ref string) error {
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("vcs: refusing ref %q that looks like a flag", ref)
	}
	return nil
}

// checkRev is checkRef for git revisions, which also refuses what `fetch` or `push` would
// read as a refspec: a leading "+", or a ":" anywhere. `+refs/heads/*:refs/heads/*` passed
// where a revision belongs overwrites every local branch. A revision naming a commit or a
// tree never needs either character. Empty passes, as with checkRef.
func checkRev(revs ...string) error {
	for _, rev := range revs {
		if err := checkRef(rev); err != nil {
			return err
		}
		if strings.HasPrefix(rev, "+") || strings.Contains(rev, ":") {
			return fmt.Errorf("vcs: refusing revision %q that reads as a refspec", rev)
		}
	}
	return nil
}

// checkRequiredRev is checkRev for a revision the call cannot do without.
func checkRequiredRev(revs ...string) error {
	for _, rev := range revs {
		if rev == "" {
			return errors.New("vcs: a revision is required")
		}
	}
	return checkRev(revs...)
}

// checkRequiredRevsetRef is checkRevsetRef for revisions the call cannot do without.
func checkRequiredRevsetRef(refs ...string) error {
	for _, ref := range refs {
		if ref == "" {
			return errors.New("vcs: a revision is required")
		}
		if err := checkRevsetRef(ref); err != nil {
			return err
		}
	}
	return nil
}

// checkCommitID accepts a full lowercase hex object id (SHA-1 or SHA-256), nothing else.
func checkCommitID(id string) error {
	if len(id) != 40 && len(id) != 64 {
		return fmt.Errorf("vcs: %q is not a full object id", id)
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("vcs: %q is not a full object id", id)
		}
	}
	return nil
}

// checkRefName accepts a full ref name: it starts with "refs/", passes git's
// check-ref-format rules, and holds none of the characters that would let it read as a
// refspec or a revision expression, so a fetch or a push can only ever name that one ref.
func checkRefName(ref string) error {
	bad := func(why string) error { return fmt.Errorf("vcs: refusing ref name %q: %s", ref, why) }
	rest, ok := strings.CutPrefix(ref, "refs/")
	if !ok || rest == "" {
		return bad(`it does not start with "refs/"`)
	}
	if i := strings.IndexFunc(ref, func(r rune) bool {
		return r < 0x20 || r == 0x7f || r == ' ' || strings.ContainsRune(`:+*^~?[\`, r)
	}); i >= 0 {
		return bad(fmt.Sprintf("%q is not allowed", ref[i:i+1]))
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.Contains(ref, "//") ||
		strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || ref == "@" {
		return bad("git's check-ref-format refuses it")
	}
	for _, part := range strings.Split(rest, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return bad("git's check-ref-format refuses it")
		}
	}
	return nil
}

// checkRemoteName accepts only what can name a configured remote: no leading "-" or ".",
// no "/", "\", ":" or whitespace, so the value can never be read as an option, a URL or a
// path ("." is the repository itself to git).
func checkRemoteName(name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") ||
		strings.ContainsFunc(name, func(r rune) bool {
			return r == '/' || r == ':' || r == '\\' || unicode.IsSpace(r) || unicode.IsControl(r)
		}) {
		return fmt.Errorf("vcs: refusing remote %q: a remote is named by a configured name, never a URL or a path", name)
	}
	return nil
}

// checkRevsetRef is checkRef plus the characters that would EXTEND the expression a ref is
// placed INSIDE, rather than name a revision within it.
//
// hg, Sapling and jj compose revsets (ancestor(base,head), heads(::base & ::head)), so a
// ref carrying a comma, a paren or a boolean operator rewrites the expression and the diff
// silently covers a range nobody asked for. A wrong answer is worse here than an error,
// because nothing downstream can tell it from a right one. git needs only checkRef: it
// takes base...head as a single argv token and composes nothing.
//
// `~` and `^` are deliberately allowed: HEAD~1 and HEAD^ name revisions, and refusing them
// would reject the ordinary way to say "one before".
func checkRevsetRef(ref string) error {
	if err := checkRef(ref); err != nil {
		return err
	}
	if i := strings.IndexAny(ref, "(),&|! \t"); i >= 0 {
		return fmt.Errorf("vcs: refusing ref %q: %q would extend the revset it sits in rather than name a revision", ref, ref[i:i+1])
	}
	return nil
}

// repoPathPrefix returns dir's path relative to the repository root, forward-slashed with
// a trailing slash, or "" when dir IS the root.
//
// It exists because the four backends disagree about which directory their paths are
// relative to, while every caller in magus assumes the repository root: a driver that
// answers in cwd-relative paths hands back a name that resolves to a DIFFERENT existing
// file once the caller rebases it, with nothing to error on. git and hg report from the
// root already; sl needs --root-relative; jj has no such flag and has to be run from the
// root instead, which is what needs this prefix to translate pathspecs.
//
// It asks the driver for its own root rather than reading a marker directory, so a backend
// whose root is not simply "the dir containing the claim" stays correct.
//
// Both sides are symlink-resolved before being related, and that is load-bearing rather
// than defensive: every backend reports a root with symlinks already resolved, while dir
// arrives as the caller wrote it. On macOS a path under /var is really /private/var, so
// relating the two unresolved yields "../../../.." and a prefix that matches nothing,
// which silently filters every file out instead of failing. The same happens anywhere a
// repository is reached through a symlinked parent.
//
// The returned root is the driver's own answer, unresolved-by-us, because callers use it
// as a working directory rather than for comparison.
func repoPathPrefix(ctx context.Context, v types.VCSDriver, dir string) (root, prefix string, err error) {
	root, err = v.Root(ctx, dir)
	if err != nil {
		return "", "", fmt.Errorf("vcs: locate repository root: %w", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	// EvalSymlinks fails on a path that does not exist; fall back to the literal path so a
	// caller naming a directory that is about to be created still gets a usable answer.
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	rel, err := filepath.Rel(resolve(root), resolve(abs))
	if err != nil {
		return "", "", err
	}
	if rel == "." {
		return root, "", nil
	}
	return root, filepath.ToSlash(rel) + "/", nil
}

// keepSubtree drops every file outside prefix from each commit. hg, Sapling and jj all
// narrow WHICH commits a log lists to a pathspec but still list each commit's files whole,
// so without this a nested workspace is credited with edits made outside it. An empty
// prefix is the repository root, where every file is in the subtree.
func keepSubtree(changes []types.CommitChange, prefix string) []types.CommitChange {
	if prefix == "" {
		return changes
	}
	for i := range changes {
		changes[i].Files = slices.DeleteFunc(changes[i].Files, func(f types.FileChange) bool {
			return !strings.HasPrefix(f.Path, prefix)
		})
	}
	return changes
}

// copySubtree copies prefix's subtree of an exported tree at root into dstDir. A revision
// predating the subtree exported none of it, which is an empty tree rather than a failure:
// git reports the same case as "everything was added".
func copySubtree(root, prefix, dstDir string) error {
	staged := filepath.Join(root, filepath.FromSlash(prefix))
	if _, err := os.Stat(staged); os.IsNotExist(err) {
		return os.MkdirAll(dstDir, 0o755)
	}
	return copyTree(staged, dstDir)
}

// ConfiguredRemote returns the default remote recorded in the config of whichever
// backend claims root, read without running it. Empty when nothing claims root or the
// claiming backend records no remote a file read can find.
//
// Walks builtin so the claim ordering has ONE definition; see its comment for why a
// colocated jj workspace must not resolve as git.
//
// Honors no MAGUS_VCS_NAME override, unlike Resolve: this keys a state store, and one
// that moves because an env var was exported for a command looks empty for reasons its
// owner cannot see.
func ConfiguredRemote(root string) string {
	for _, d := range builtin {
		if !claimsExist(root, d.Claims()) {
			continue
		}
		u, err := d.ConfiguredRemote(root)
		if err != nil {
			return ""
		}
		return u
	}
	return ""
}

// Checkouts finds the checkout containing dir and returns its root plus every other
// live checkout of the same repository, primary first. others is nil when the claiming
// VCS cannot list checkouts, or when dir is in no checkout at all.
func Checkouts(dir string) (root string, others []string, err error) {
	for level := dir; ; level = filepath.Dir(level) {
		for _, d := range builtin {
			if !claimsExist(level, d.Claims()) {
				continue
			}
			others, err := d.OtherCheckouts(level)
			if errors.Is(err, types.ErrVCSUnsupported) {
				return level, nil, nil
			}
			return level, others, err
		}
		if filepath.Dir(level) == level {
			return "", nil, nil
		}
	}
}

func claimsExist(root string, claims []string) bool {
	for _, c := range claims {
		if _, err := os.Stat(filepath.Join(root, c)); err == nil {
			return true
		}
	}
	return false
}

// parseTags reads "<name>\t<rfc3339 date>\t<id>" lines, one tag per line, in the
// order the backend emitted them, keeping only names matching pattern ("" keeps
// all). Both git's for-each-ref --format and hg's tag template are configured to
// produce this shape, so the two backends share one parser AND one matcher: git
// could filter refs server-side via for-each-ref's own pattern, but then a glob
// would mean subtly different things per backend, which is worse than the cost of
// matching a handful of strings here.
//
// path.Match is the matcher because its wildcards stop at "/", so "v*" selects
// v0.3.0 while correctly skipping a namespaced tag like backup/pre-reword. A
// malformed pattern is a caller bug and is returned, not silently treated as
// "match nothing". A line missing a name is skipped; an unparsable date is left
// zero rather than dropping the tag, since the name is what callers rely on.
func parseTags(out, pattern string) ([]types.VCSTag, error) {
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	tags := make([]types.VCSTag, 0, len(lines))
	for _, line := range lines {
		name, rest, ok := strings.Cut(line, "\t")
		if !ok || name == "" {
			continue
		}
		if pattern != "" {
			match, err := path.Match(pattern, name)
			if err != nil {
				return nil, fmt.Errorf("vcs: tag pattern %q: %w", pattern, err)
			}
			if !match {
				continue
			}
		}
		when, id, _ := strings.Cut(rest, "\t")
		tag := types.VCSTag{Name: name, ID: id}
		tag.Prefix, tag.Version = splitTagVersion(name)
		if ts, err := time.Parse(time.RFC3339, when); err == nil {
			tag.Date = ts
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

// splitTagVersion splits a tag name into its module prefix and parsed
// version: "libs/gopherbuzz/v0.1.0" -> ("libs/gopherbuzz/", 0.1.0), "v0.3.0"
// -> ("", 0.3.0). A name with no "/" has an empty prefix. A version portion
// that fails to parse (an annotated tag like "checkpoint", or a namespaced
// non-release tag the pattern filter let through) is not an error; it
// leaves Version at its zero value. Parses with Masterminds/semver, the same
// library std/semver.go's SemverParse uses; the two can't share a call
// because vcs can't import std (std already imports vcs).
func splitTagVersion(name string) (prefix string, version types.SemverVersion) {
	verPart := name
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		prefix, verPart = name[:i+1], name[i+1:]
	}
	sv, err := semver.NewVersion(verPart)
	if err != nil {
		return prefix, types.SemverVersion{}
	}
	return prefix, types.SemverVersion{
		Major:      int(sv.Major()),
		Minor:      int(sv.Minor()),
		Patch:      int(sv.Patch()),
		Prerelease: sv.Prerelease(),
		Metadata:   sv.Metadata(),
		Original:   sv.Original(),
	}
}

// trimStatusColumns drops a fixed-width status prefix from each of a backend's status
// lines, leaving the path, and discards any line left empty.
//
//	git  "XY path"   width 3: two status columns and a space
//	hg   "X path"    width 2: one status column and a space
//	sl   "X path"    width 2: Sapling kept Mercurial's status shape
//	jj   "path"      no prefix, so jj does not call this
//
// width is passed by the driver rather than derived from the line, because a line's own
// bytes cannot distinguish a prefix from a path that happens to look like one: a jj file
// named "A note.txt" is indistinguishable from an added "note.txt" without knowing which
// backend printed it. The driver always knows.
//
// A line SHORTER than width is passed through whole rather than sliced away. Slicing
// would silently turn a short path into "", dropping a changed file from the result,
// and a caller cannot tell an empty answer from a clean tree.
func trimStatusColumns(lines []string, width int) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		p := line
		if len(p) > width {
			p = p[width:]
		}
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// preserveMessage is what Preserve leaves on the object it mints, so a reader meeting the
// commit or shelf in their own history knows what put it there. PrunePreserved reads it
// back as the proof that magus wrote the object.
const preserveMessage = "magus preserved working copy"

// preserveRetention is how long a preserved state stays resolvable. Both enforcement sides
// are needed: every Preserve prunes, so the bound holds on a machine that runs no
// maintenance job, and the prune-preserved job calls [PrunePreserved] on a schedule, so it
// also holds in a repository that preserved once and never again.
//
// Thirty days: long enough that a handle recorded in a ledger still resolves when someone
// reads that ledger, short enough that a busy repository does not accumulate a year of
// snapshots.
const preserveRetention = 30 * 24 * time.Hour

// shelfPrefix marks a shelf as magus's. PrunePreserved deletes only these, so a shelf a
// person made by hand is never in scope.
const shelfPrefix = "magus-"

// shelfName mints the name Mercurial stores a shelf under: the prefix, the mint time in
// unix seconds, then random bytes.
//
// Random, because two preserved copies of one tree are distinct events that a
// content-derived name would collapse into one. Timestamped, because `hg shelve --list`
// prints ages as prose ("2m ago") and takes no template, so a retention pass reading it
// would be parsing a UI string.
func shelfName() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A snapshot that cannot be named is worse than one named predictably.
		return fmt.Sprintf("%s%d-%d", shelfPrefix, time.Now().Unix(), time.Now().UnixNano())
	}
	return fmt.Sprintf("%s%d-%s", shelfPrefix, time.Now().Unix(), hex.EncodeToString(b[:]))
}

// shelfMinted reads back the time shelfName stamped, and reports false for any name it
// did not mint.
//
// A "magus-" name carrying no stamp reports false too: it cannot be judged against a
// retention window without guessing, and PrunePreserved must never guess. Such a shelf
// leaks until someone runs `hg shelve --delete`.
func shelfMinted(name string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(name, shelfPrefix)
	if !ok {
		return time.Time{}, false
	}
	secs, _, ok := strings.Cut(rest, "-")
	if !ok {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(n, 0), true
}
