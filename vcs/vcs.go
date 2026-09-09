package vcs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
		if _, ok := v.(types.MergeDriverInstaller); ok {
			names = append(names, v.Name())
		}
	}
	return names
}

// Installer returns the merge-driver installer for the named VCS, or (nil, false).
func Installer(name string) (types.MergeDriverInstaller, bool) {
	v, ok := lookupImpl(name)
	if !ok {
		return nil, false
	}
	inst, ok := v.(types.MergeDriverInstaller)
	return inst, ok
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
// relating the two unresolved yields "../../../.." and a prefix that matches nothing -
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

// hgFamilyGlobs prefixes each pathspec with Mercurial's "glob:" pattern kind, for the two
// backends that speak Mercurial's pathspec syntax (hg and sl).
//
// It is a silent-wrong-answer fix, not a nicety. An hg pathspec defaults to the "relpath"
// kind (a literal path), so a caller-supplied GLOB matches nothing, and hg reports that by
// writing "gen/**: No such file or directory" to STDERR while exiting 0 with empty stdout.
// The drivers read stdout, so the answer came back "no files changed". Callers pass globs:
// magus.diagnoseDrift hands DirtyFiles a project's declared output globs verbatim, so under
// hg and sl the generate drift gate reported every project clean having matched nothing -
// in CI, with no diagnostic. git and jj both handle "gen/**" natively, which is why only
// these two were wrong. Measured on Mercurial 7.x and Sapling 0.2.x.
//
// A pattern with no wildcards still matches itself under glob:, so this is safe for the
// literal paths some callers pass. It also removes a latent ambiguity: an unprefixed
// pathspec containing a colon would be read as "<kind>:<pattern>" and rejected.
func hgFamilyGlobs(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, "glob:"+p)
	}
	return out
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
// would silently turn a short path into "", dropping a changed file from the result -
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

// hgFamilyPending is the working-copy state preserving consumes and has to put
// back: the files the backend calls unknown, and the tracked files missing from disk.
//
// Mercurial and Sapling capture unknown files by ADDING them and missing files by marking
// them REMOVED, so a capture that does not restore leaves the user with files staged for a
// commit they never made and a deletion they never scheduled.
type hgFamilyPending struct {
	// root is the repository root, and both the directory the paths below are relative to
	// and the only directory putting them back may run in; see hgFamilyRoot.
	root    string
	unknown []string
	missing []string
}

// hgFamilyRoot resolves the repository root, where reading the pending state and putting
// it back both have to run.
//
// `hg status` answers in ROOT-relative paths while `hg revert` and `hg forget` resolve
// their arguments against the CWD, so the same string names two files whenever the caller
// passes a subdirectory. Measured 2026-09-09 on Mercurial 7.2.3: `hg revert --no-backup
// -- sub/tracked.txt` run inside sub/ prints "no such file in rev" and exits ZERO, leaving
// the file scheduled for a removal the user never asked for. Sapling needs the anchor for
// the mirror-image reason, its status being CWD-relative from a subdirectory.
//
// The capture itself (hg's shelve, Sapling's commit --addremove) carries no pathspec and
// acts on the whole repository, so it runs unanchored in the caller's dir.
//
// The root comes back with symlinks RESOLVED, so a repository reached through a symlink
// gets its real path here and every command anchored on it reports that path back.
func hgFamilyRoot(ctx context.Context, prog, dir string) (string, error) {
	cmd := vcsExec(ctx, prog, "root")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s root: %w", prog, err)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", fmt.Errorf("%s root: no repository root for %s", prog, dir)
	}
	return root, nil
}

// hgFamilyReadPending reads that state. It must run BEFORE the capture, since the capture
// is what changes it.
func hgFamilyReadPending(ctx context.Context, prog, dir string) (hgFamilyPending, error) {
	root, err := hgFamilyRoot(ctx, prog, dir)
	if err != nil {
		return hgFamilyPending{}, err
	}
	unknown, err := hgFamilyStatusPaths(ctx, prog, root, "--unknown")
	if err != nil {
		return hgFamilyPending{}, err
	}
	missing, err := hgFamilyStatusPaths(ctx, prog, root, "--deleted")
	if err != nil {
		return hgFamilyPending{}, err
	}
	return hgFamilyPending{root: root, unknown: unknown, missing: missing}, nil
}

// hgFamilyRestorePending returns the working copy to the state hgFamilyReadPending saw.
//
// forget un-adds, which is exact. A removal has no un-mark: `add` and `forget` both leave
// it scheduled (measured), and only revert clears it, which writes the file back, so it is
// deleted again immediately after. Those bytes are the committed ones the user had already
// deleted, so nothing of theirs is at risk in between.
func hgFamilyRestorePending(ctx context.Context, prog string, p hgFamilyPending) error {
	if len(p.unknown) > 0 {
		if err := hgFamilyRun(ctx, prog, p.root, append([]string{"forget", "--"}, p.unknown...)); err != nil {
			return err
		}
	}
	if len(p.missing) == 0 {
		return nil
	}
	if err := hgFamilyRun(ctx, prog, p.root, append([]string{"revert", "--no-backup", "--"}, p.missing...)); err != nil {
		return err
	}
	for _, rel := range p.missing {
		if err := os.Remove(filepath.Join(p.root, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("%s: re-delete %s: %w", prog, rel, err)
		}
	}
	return nil
}

// hgFamilyStatusPaths lists the paths in one status class, NUL-delimited.
//
// The template is what makes the list unambiguous. A newline is legal in a filename, so a
// line-split parse turns "we\nird.txt" into two paths that name nothing: measured
// 2026-09-09 on Mercurial 7.2.3, `hg revert --no-backup -- we` prints "no such file in
// rev" and exits ZERO, so the restore reports success while the user's file stays
// scheduled for a removal they never asked for. Trimming compounds it, a leading space
// being legal too.
//
// Sapling takes the same template (measured on 0.2.20260811-150444) and refuses a newline
// in a name outright, so one spelling covers both backends.
func hgFamilyStatusPaths(ctx context.Context, prog, dir, class string) ([]string, error) {
	cmd := vcsExec(ctx, prog, "status", class, "--no-status", "-T", `{path}\0`)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s status %s: %w", prog, class, err)
	}
	var files []string
	for path := range strings.SplitSeq(string(out), "\x00") {
		if path != "" {
			files = append(files, path)
		}
	}
	return files, nil
}

func hgFamilyRun(ctx context.Context, prog, dir string, args []string) error {
	cmd := vcsExec(ctx, prog, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", prog, args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}
