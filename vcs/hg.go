package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

type hgVCS struct{}

func (v hgVCS) Name() string     { return "hg" }
func (v hgVCS) Claims() []string { return []string{".hg"} }

// Base is the "default" BRANCH, not "tip".
//
// tip is the newest commit in the repository, which after any commit of your own is your
// own commit - so ChangedFiles compared the checkout against itself and `magus affected`
// reported nothing affected, building nothing. Measured: working on a named branch,
// `hg status --rev tip` returns empty where `--rev default` correctly names the changed
// file.
//
// default is the mainline every hg repository has (`hg init` creates it and it cannot be
// renamed), so it is the direct analogue of git's origin/main and sl's remote/main - a
// fixed name for the line of development, rather than a pointer at whatever is newest. It
// is a strict improvement rather than a trade: on a named branch it is correct where tip
// was wrong, and for work committed straight onto default the two are equivalent, since
// both then name the same commit.
func (v hgVCS) Base() string { return "default" }

// ParentRef is the first parent of the working directory. `p1(.)` names it
// explicitly; a bare `.^` is p1 too but reads as a typo next to git's form.
func (v hgVCS) ParentRef() string { return "p1(.)" }

// IsSecondaryCheckout reports whether dir is an `hg share` checkout: the share
// extension writes .hg/sharedpath naming the source repo's store, so the working
// copy re-exposes the shared repo's files. A standalone repo has no sharedpath.
func (v hgVCS) IsSecondaryCheckout(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".hg", "sharedpath"))
	return err == nil
}

func (v hgVCS) Root(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "hg", "root")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (v hgVCS) ChangedFiles(ctx context.Context, dir, base string) ([]string, error) {
	if err := checkRef(base); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "hg", "status",
		"--no-status", "--added", "--modified", "--removed", "--rev", base)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("hg status: %w", err)
	}
	return splitLines(out), nil
}

func (v hgVCS) DiffCommands(ctx context.Context, dir, base string) (types.DiffCommandHints, error) {
	out, err := exec.CommandContext(ctx, "hg", "-R", dir, "log", "-r", ".", "--template", "{node}").Output()
	if err != nil {
		return types.DiffCommandHints{}, fmt.Errorf("hg log: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	return types.DiffCommandHints{
		CLI: fmt.Sprintf("hg diff -r %s -r %s", base, sha),
		// GUI omitted: hg extdiff requires the extdiff extension; can't assume it's enabled.
	}, nil
}

func (v hgVCS) Bisect(ctx context.Context, dir string, opts types.BisectOptions) (types.Culprit, error) {
	if err := checkRef(opts.Good); err != nil {
		return types.Culprit{}, err
	}
	if err := checkRef(opts.Bad); err != nil {
		return types.Culprit{}, err
	}
	if opts.Good == "" {
		sha, err := v.commitBeforeTime(ctx, dir, opts.GoodBefore)
		if err != nil {
			return types.Culprit{}, err
		}
		opts.Good = sha
	}
	if err := v.isAncestor(ctx, dir, opts.Good); err != nil {
		return types.Culprit{}, fmt.Errorf("good commit %q is not an ancestor of current revision: %w", opts.Good, err)
	}
	bad := opts.Bad
	if bad == "" {
		bad = "."
	}

	if err := v.start(ctx, dir, bad, opts.Good); err != nil {
		return types.Culprit{}, fmt.Errorf("hg bisect start: %w", err)
	}
	defer func() { _ = v.reset(context.WithoutCancel(ctx), dir) }()

	if err := v.run(ctx, dir, opts.TestCmd); err != nil {
		slog.WarnContext(ctx, "hg bisect run exited with error", slog.String("err", err.Error()))
	}

	sha, err := v.culprit(ctx, dir)
	if err != nil {
		return types.Culprit{}, err
	}
	info, _ := v.commitInfo(ctx, dir, sha)
	return types.Culprit{ID: sha, Info: info}, nil
}

func (v hgVCS) Metadata(ctx context.Context, dir string) (types.VCSMeta, error) {
	shortHash, err := vcsOutput(ctx, dir, "hg", "log", "-r", ".", "--template", "{short(node)}")
	if err != nil {
		return types.VCSMeta{}, err
	}
	hash, _ := vcsOutput(ctx, dir, "hg", "log", "-r", ".", "--template", "{node}")
	branch, _ := vcsOutput(ctx, dir, "hg", "branch")
	commitDate, _ := vcsOutput(ctx, dir, "hg", "log", "-r", ".", "--template", "{date|isodate}")
	// Don't swallow the dirty-probe error: a failed status must not be reported as
	// a clean tree.
	dirtyOut, err := vcsOutput(ctx, dir, "hg", "status")
	if err != nil {
		return types.VCSMeta{}, fmt.Errorf("hg status: %w", err)
	}
	return types.VCSMeta{
		Short:      shortHash,
		ID:         hash,
		Ref:        branch,
		CommitDate: commitDate,
		IsDirty:    dirtyOut != "",
	}, nil
}

// Dirty reports whether the working directory (optionally scoped to paths) has
// changes, via `hg status`. Non-empty output = dirty.
func (v hgVCS) Dirty(ctx context.Context, dir string, paths []string) (bool, error) {
	files, err := v.DirtyFiles(ctx, dir, paths)
	return len(files) > 0, err
}

// DirtyFiles implements types.VCSDriver, returning repo-relative paths. Mercurial prints
// one status column and a space before each.
func (v hgVCS) DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	args := []string{"status"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, hgFamilyGlobs(paths)...)
	}
	out, err := vcsOutputRaw(ctx, dir, "hg", args...)
	if err != nil {
		return nil, fmt.Errorf("hg status: %w", err)
	}
	return trimStatusColumns(splitStatusLines(out), 2), nil
}

// DirtyDiff implements types.VCSDriver: uncommitted changes against the working parent.
func (v hgVCS) DirtyDiff(ctx context.Context, dir string, paths []string) (string, error) {
	args := []string{"diff", "-U", "1"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, hgFamilyGlobs(paths)...)
	}
	out, err := vcsOutputRaw(ctx, dir, "hg", args...)
	if err != nil {
		return "", fmt.Errorf("hg diff: %w", err)
	}
	return out, nil
}

// Describe returns the working revision's latest reachable tag (Mercurial's
// {latesttag}), with a -dirty suffix for a modified tree. A repo with no tags
// reports "" (Mercurial's "null" sentinel is normalized away), matching the
// interface's "no describe available" contract.
func (v hgVCS) Describe(ctx context.Context, dir string) (string, error) {
	tag, err := vcsOutput(ctx, dir, "hg", "log", "-r", ".", "--template", "{latesttag}")
	if err != nil {
		return "", err
	}
	if tag == "" || tag == "null" {
		return "", nil
	}
	if dirty, _ := vcsOutput(ctx, dir, "hg", "status"); dirty != "" {
		tag += "-dirty"
	}
	return tag, nil
}

// Tags lists tags newest-first. hg always reports a synthetic "tip" tag - a
// moving pointer at the newest revision rather than a marker anyone set - so it
// is filtered out; leaving it in would make every repository look freshly tagged.
func (v hgVCS) Tags(ctx context.Context, dir, pattern string) ([]types.VCSTag, error) {
	out, err := vcsOutput(ctx, dir, "hg", "tags", "--template", "{tag}\t{date|rfc3339date}\t{node}\n")
	if err != nil {
		return nil, err
	}
	tags, err := parseTags(out, pattern)
	if err != nil {
		return nil, err
	}
	kept := tags[:0]
	for _, t := range tags {
		if t.Name != "tip" {
			kept = append(kept, t)
		}
	}
	return kept, nil
}

// hgCommitTemplate emits the NUL-delimited fields parseCommit expects: node,
// short node, author name/email, the record date (RFC 3339), parents, and the
// full message. \0 is the field delimiter Mercurial converts to NUL.
//
// Parents are {p1node} {p2node} and NOT `{parents % "{node} "}`, which is what this used
// and which reports NOTHING for ordinary linear history: Mercurial's `parents` keyword
// filters through meaningfulparents, so it lists nodes only for a merge or after a
// non-linear update. Every plain hg commit therefore came back with Parents nil - which
// reads as a root commit at the Buzz boundary, and made `len(Parents) > 1` merge detection
// permanently false. {p2node} is all zeros off a merge and parseCommit's parents() already
// drops an all-zero id. Verified against both hg and sl, which is what lets the two share
// this constant: sl's `parents` keyword DOES report linear parents, so the bug was
// invisible from the Sapling side.
const hgCommitTemplate = `{node}\0{node|short}\0{person(author)}\0{email(author)}\0{date|rfc3339date}\0{p1node} {p2node}\0{desc}`

func (v hgVCS) FindCommit(ctx context.Context, dir, rev string) (types.Commit, error) {
	if rev == "" {
		rev = "."
	}
	if err := checkRef(rev); err != nil {
		return types.Commit{}, err
	}
	out, err := vcsOutput(ctx, dir, "hg", "log", "-r", rev, "--template", hgCommitTemplate)
	if err != nil {
		return types.Commit{}, fmt.Errorf("hg log %s: %w", rev, err)
	}
	c := parseCommit(out)
	if c.ID == "" {
		return types.Commit{}, fmt.Errorf("hg: no commit for %q", rev)
	}
	return c, nil
}

func (v hgVCS) History(ctx context.Context, dir string, limit int) ([]types.Commit, error) {
	if limit <= 0 {
		limit = 1
	}
	// hg log is newest-first by default, so -l N is the N most recent.
	out, err := vcsOutput(ctx, dir, "hg", "log", "-l", fmt.Sprintf("%d", limit), "--template", "{node}\n")
	if err != nil {
		return nil, fmt.Errorf("hg log: %w", err)
	}
	return resolveEach(ctx, dir, v, splitLines([]byte(out)))
}

// isAncestor, commitBeforeTime and commitInfo run via `hg -R dir` so they target
// the repository under bisect, not the process cwd: the dir-scoping the VCSDriver
// contract requires for correctness under concurrent runs.
func (v hgVCS) isAncestor(ctx context.Context, dir, sha string) error {
	out, err := exec.CommandContext(ctx, "hg", "-R", dir, "log",
		"-r", "("+sha+") and (ancestors(.) or .)",
		"--template", "{node}").Output()
	if err != nil {
		return fmt.Errorf("hg log: %w", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return fmt.Errorf("hg: %s is not an ancestor of current revision", sha)
	}
	return nil
}

func (v hgVCS) commitBeforeTime(ctx context.Context, dir string, t time.Time) (string, error) {
	// hg date() predicate requires a date string, not an epoch integer.
	revset := fmt.Sprintf("max(date('<%s'))", t.UTC().Format("2006-01-02 15:04:05"))
	out, err := exec.CommandContext(ctx, "hg", "-R", dir, "log", "-r", revset, "--template", "{node}").Output()
	if err != nil {
		return "", fmt.Errorf("hg log: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", errors.New("no commit found before the last passing run")
	}
	return sha, nil
}

func (v hgVCS) commitInfo(ctx context.Context, dir, sha string) (string, error) {
	out, err := exec.CommandContext(ctx, "hg", "-R", dir, "log", "-r", sha,
		"--template", "{desc|firstline}  ({author|user}, {date|shortdate})").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (v hgVCS) start(ctx context.Context, dir, bad, good string) error {
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "hg", args...)
		cmd.Dir = dir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if err := run("bisect", "--reset"); err != nil {
		return err
	}
	if err := run("bisect", "--bad", bad); err != nil {
		return err
	}
	return run("bisect", "--good", good)
}

func (v hgVCS) run(ctx context.Context, dir, shellCmd string) error {
	cmd := exec.CommandContext(ctx, "hg", "bisect", "--command", shellCmd)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v hgVCS) reset(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "hg", "bisect", "--reset")
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v hgVCS) culprit(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "hg", "-R", dir, "log",
		"-r", "bisect(bad)", "--template", "{node}\n").Output()
	if err != nil {
		return "", fmt.Errorf("hg log bisect(bad): %w", err)
	}
	sha := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	if sha == "" {
		return "", errors.New("hg bisect(bad) returned no revision")
	}
	return sha, nil
}

// Managed-section markers; see the git.go block for why each pair is a locator MARK
// plus the full line written.
const (
	hgRCBegin     = "# BEGIN magus-generated"
	hgRCBeginLine = hgRCBegin + " - do not edit this section manually"
	hgRCEnd       = "# END magus-generated"

	hgHookBegin     = "# BEGIN magus-refresh"
	hgHookBeginLine = hgHookBegin + " - do not edit this section manually"
	hgHookEnd       = "# END magus-refresh"
)

// InstallMergeDriver writes [merge-patterns] and [merge-tools] to .hg/hgrc.
func (v hgVCS) InstallMergeDriver(_ context.Context, root string, outputGlobs []string) error {
	hgrcPath := filepath.Join(root, ".hg", "hgrc")
	var section strings.Builder
	section.WriteString(hgRCBeginLine + "\n")
	section.WriteString("[merge-patterns]\n")
	for _, glob := range outputGlobs {
		fmt.Fprintf(&section, "glob:%s = magus\n", glob)
	}
	section.WriteString("\n[merge-tools]\n")
	section.WriteString("magus.executable = magus\n")
	section.WriteString("magus.args = vcs merge-driver $base $local $other 0 $local\n")
	section.WriteString("magus.premerge = False\n")
	section.WriteString("magus.gui = False\n")
	section.WriteString(hgRCEnd + "\n")
	existing, _ := os.ReadFile(hgrcPath)
	updated := replaceManagedSection(string(existing), section.String(), hgRCBegin, hgRCEnd)
	return os.WriteFile(hgrcPath, []byte(updated), 0o644)
}

// CheckMergeDriver reports whether the magus merge driver is registered in .hg/hgrc.
func (v hgVCS) CheckMergeDriver(_ context.Context, root string) (bool, error) {
	data, _ := os.ReadFile(filepath.Join(root, ".hg", "hgrc"))
	return strings.Contains(string(data), hgRCBegin), nil
}

// EnsureMergeDriver implements types.MergeDriverInstaller. hgrc holds the glob list
// inline, so a byte comparison of the rendered section answers both questions the git
// side needs two checks for: registered at all, and registered for the current globs.
func (v hgVCS) EnsureMergeDriver(ctx context.Context, root string, outputGlobs []string) (bool, error) {
	if len(outputGlobs) == 0 {
		return false, nil
	}
	before, _ := os.ReadFile(filepath.Join(root, ".hg", "hgrc"))
	if err := v.InstallMergeDriver(ctx, root, outputGlobs); err != nil {
		return false, err
	}
	after, _ := os.ReadFile(filepath.Join(root, ".hg", "hgrc"))
	return !bytes.Equal(before, after), nil
}

// InstallRefreshHook implements types.RefreshHookInstaller: it registers an hg `update`
// hook (fires after a working-directory change - checkout, pull-update) that runs
// command. It shares replaceManagedSection with the merge-driver install, under its own
// markers so the two managed sections coexist in .hg/hgrc. Returns the hook label.
func (v hgVCS) InstallRefreshHook(_ context.Context, root, command string) ([]string, error) {
	hgrcPath := filepath.Join(root, ".hg", "hgrc")
	var section strings.Builder
	section.WriteString(hgHookBeginLine + "\n")
	section.WriteString("[hooks]\n")
	fmt.Fprintf(&section, "update.magus-refresh = %s >/dev/null 2>&1 || true\n", command)
	section.WriteString(hgHookEnd + "\n")
	existing, err := os.ReadFile(hgrcPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("vcs: read %s: %w", hgrcPath, err)
	}
	updated := replaceManagedSection(string(existing), section.String(), hgHookBegin, hgHookEnd)
	if updated == string(existing) {
		return nil, nil
	}
	if err := os.WriteFile(hgrcPath, []byte(updated), 0o644); err != nil {
		return nil, fmt.Errorf("vcs: write %s: %w", hgrcPath, err)
	}
	return []string{"update"}, nil
}

// ConflictResolver (below) is implemented for hg so `magus vcs resolve` is not a
// git-only command. The mapping is close because Mercurial models resolution
// explicitly - it keeps a resolve state per path, which is exactly the
// mark-resolved step the interface names.
//
// The assertion is compile-time on purpose: the interface is reached by type assertion
// at the call site, so dropping a method would not fail the build, it would silently
// demote hg back to "resolve this merge by hand".
var _ types.ConflictResolver = hgVCS{}

// hgPathChunks splits paths so a long conflict list cannot overflow the argv limit.
// Mercurial takes pathspecs the same way git does, so it needs the same guard.
func hgPathChunks(paths []string) [][]string {
	var chunks [][]string
	for start := 0; start < len(paths); start += gitArgChunkSize {
		chunks = append(chunks, paths[start:min(start+gitArgChunkSize, len(paths))])
	}
	return chunks
}

func runHgBatched(ctx context.Context, root string, args []string, paths []string) error {
	for _, chunk := range hgPathChunks(paths) {
		argv := append([]string{}, args...)
		argv = append(argv, "--")
		argv = append(argv, chunk...)
		cmd := exec.CommandContext(ctx, "hg", argv...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("hg %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	return nil
}

// parseHgConflicts turns `hg resolve --list` output into unresolved paths.
//
// The format is one "<status> <path>" line per file: U unresolved, R resolved. Only U
// is a conflict; R lines are paths this command (or the user) already settled and must
// not be re-resolved.
//
// Every U is reported as ConflictKindContent, and that is a real limitation rather than
// a simplification: `resolve --list` does not distinguish a content conflict from a
// modify/delete, so the kind a caller needs to settle ConflictKindDeleted is not in this
// output. hgConflictKinds refines it from `hg status` afterwards.
func parseHgConflicts(out string) []types.Conflict {
	var conflicts []types.Conflict
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 3 || line[1] != ' ' {
			continue
		}
		if line[0] != 'U' {
			continue
		}
		if p := strings.TrimSpace(line[2:]); p != "" {
			conflicts = append(conflicts, types.Conflict{Path: p, Kind: types.ConflictKindContent})
		}
	}
	return conflicts
}

// parseHgRemovalCandidates reads `hg debugmergestate` and returns the conflicted paths
// whose OTHER side carries no content - Mercurial's modify/delete, the shape a
// regeneration cannot settle and no merge tool is invoked for.
//
// `hg resolve --list` cannot answer this: it reports every unresolved path as plain "U",
// with no arity or deletion clause (jj's list, by contrast, says "including 1 deletion").
// The merge state is the only place the distinction is recorded:
//
//	file: gone.txt (state "u")
//	  other path: gone.txt (node 0000000000000000000000000000000000000000)
//	  extra: merge-removal-candidate = yes
//
// Keyed on merge-removal-candidate rather than the null node id because it is the
// explicit statement of the same fact and survives a hash-length change.
//
// debugmergestate is a DEBUG command, so its output is not a stability promise. A parse
// that finds nothing therefore degrades to "no deletions", leaving every path a content
// conflict - the same answer this code gave before the probe existed, and the safe one:
// a content conflict that is really a delete fails loudly at resolution, where treating a
// content conflict as a delete would remove a file nobody asked to remove.
func parseHgRemovalCandidates(out string) map[string]bool {
	removal := map[string]bool{}
	var current string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "file: "); ok {
			if i := strings.Index(rest, " (state"); i > 0 {
				current = rest[:i]
			} else {
				current = strings.TrimSpace(rest)
			}
			continue
		}
		if current != "" && strings.Contains(line, "merge-removal-candidate = yes") {
			removal[current] = true
		}
	}
	return removal
}

// hgDeletedPaths reports which of paths the merge left without content on the other side.
func hgDeletedPaths(ctx context.Context, root string, paths []string) map[string]bool {
	out, err := vcsOutputRaw(ctx, root, "hg", "debugmergestate")
	if err != nil {
		return map[string]bool{} // best effort: every path stays ConflictKindContent
	}
	removal := parseHgRemovalCandidates(out)
	deleted := make(map[string]bool, len(paths))
	for _, p := range paths {
		if removal[p] {
			deleted[p] = true
		}
	}
	return deleted
}

// Conflicts implements types.ConflictResolver. No merge in progress is not an error:
// `resolve --list` simply prints nothing, which parses to no conflicts.
func (v hgVCS) Conflicts(ctx context.Context, root string) ([]types.Conflict, error) {
	out, err := vcsOutputRaw(ctx, root, "hg", "resolve", "--list")
	if err != nil {
		return nil, fmt.Errorf("hg resolve --list: %w", err)
	}
	conflicts := parseHgConflicts(out)
	if len(conflicts) == 0 {
		return nil, nil
	}
	paths := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		paths = append(paths, c.Path)
	}
	deleted := hgDeletedPaths(ctx, root, paths)
	for i := range conflicts {
		if deleted[conflicts[i].Path] {
			conflicts[i].Kind = types.ConflictKindDeleted
		}
	}
	return conflicts, nil
}

// KeepIncoming implements types.ConflictResolver. `:other` is Mercurial's merge tool
// that takes the incoming side wholesale, the counterpart of git's `checkout --theirs`.
// `--tool` with an internal tool re-runs resolution for the named paths without opening
// anything interactive.
//
// It deliberately does NOT mark anything resolved: `resolve --tool` leaves the path
// unresolved so the caller can regenerate first and record that instead. Mercurial marks
// on `resolve --mark`, which is MarkResolved's job.
func (v hgVCS) KeepIncoming(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := runHgBatched(ctx, root, []string{"resolve", "--tool", ":other"}, paths); err == nil {
		return nil
	}
	// A path the incoming side deleted has no other-side content to take; fall back to
	// the surviving side, matching git's ours-after-theirs ordering.
	for _, p := range paths {
		if err := runHgBatched(ctx, root, []string{"resolve", "--tool", ":other"}, []string{p}); err == nil {
			continue
		}
		if err := runHgBatched(ctx, root, []string{"resolve", "--tool", ":local"}, []string{p}); err != nil {
			return fmt.Errorf("hg resolve %q: the merge left content on neither side; resolve it by hand: %w", p, err)
		}
	}
	return nil
}

// MarkResolved implements types.ConflictResolver, recording the working-tree content as
// the resolution. Mercurial has no index, so unlike git's `add` this only clears the
// resolve state; the content is already in the working copy.
func (v hgVCS) MarkResolved(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return runHgBatched(ctx, root, []string{"resolve", "--mark"}, paths)
}

// RemoveConflicts implements types.ConflictResolver. `--mark` runs first because
// Mercurial refuses to forget a path that is still unresolved, and the removal is the
// resolution here.
func (v hgVCS) RemoveConflicts(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := runHgBatched(ctx, root, []string{"resolve", "--mark"}, paths); err != nil {
		return err
	}
	return runHgBatched(ctx, root, []string{"remove", "--force"}, paths)
}

// IgnoredPaths implements types.ConflictResolver. It asks about the RULES, not about
// tracking: `hg debugignore <path>` answers for a path whether or not it is tracked,
// which is the question a conflicted-but-now-ignored generated file poses. A tracked
// path is never reported by `status --ignored`, so that command cannot answer it.
func (v hgVCS) IgnoredPaths(ctx context.Context, root string, paths []string) (map[string]bool, error) {
	ignored := make(map[string]bool, len(paths))
	for _, p := range paths {
		out, err := vcsOutputRaw(ctx, root, "hg", "debugignore", p)
		if err != nil {
			// debugignore exits non-zero when no rule matches on some versions; that is
			// the "not ignored" answer, not a failure to report.
			continue
		}
		if strings.Contains(out, "is ignored") {
			ignored[p] = true
		}
	}
	return ignored, nil
}

// The capability ladder below brings hg level with git and sl. Every command was verified
// against Mercurial 7.x rather than ported from sapling.go on the assumption that a fork
// keeps its parent's behavior - which this session established it does not, in both
// directions.
var (
	_ types.RemoteReporter      = hgVCS{}
	_ types.DefaultRefReporter  = hgVCS{}
	_ types.TrackedFileReporter = hgVCS{}
	_ types.IgnoredFileReporter = hgVCS{}
	_ types.ChurnReporter       = hgVCS{}
	_ types.RevisionExporter    = hgVCS{}
	_ types.MergeStarter        = hgVCS{}
)

// RemoteURL implements types.RemoteReporter. `hg paths default` prints the default
// pull/push URL; a repository with none exits non-zero with "not found!" on stderr, which
// is the ErrVCSUnsupported case callers degrade on rather than a failure to report.
func (v hgVCS) RemoteURL(ctx context.Context, dir string) (string, error) {
	out, err := vcsOutput(ctx, dir, "hg", "paths", "default")
	if err != nil || out == "" {
		return "", types.ErrVCSUnsupported
	}
	return out, nil
}

// DefaultRef implements types.DefaultRefReporter. Mercurial's primary line of development
// is the branch literally named "default" - it is created by `hg init` and cannot be
// renamed - so unlike git there is nothing to look up. It is still CONFIRMED to resolve
// rather than returned blind: a repository whose history is entirely on named branches can
// have no revision on default, and answering with a ref that resolves to nothing would put
// a dead link in a committed artifact.
func (v hgVCS) DefaultRef(ctx context.Context, dir string) (string, error) {
	if _, err := vcsOutput(ctx, dir, "hg", "log", "-r", "default", "-l", "1", "--template", "{node}"); err != nil {
		return "", types.ErrVCSUnsupported
	}
	return "default", nil
}

// TrackedFiles implements types.TrackedFileReporter. `hg files -- <paths>` prints the
// subset those pathspecs match in the manifest.
//
// Exit status 1 means "no pathspec in this batch matched a tracked file" and is a RESULT,
// not a failure - Mercurial and Sapling agree here and git does not, since `git ls-files`
// exits 0 for the same question. Treating it as an error would fail the most ordinary
// answer this method gives, and only for some inputs: the call is batched, so a long path
// list would fail exactly when one chunk happened to hold no tracked path.
func (v hgVCS) TrackedFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	var tracked []string
	for _, chunk := range gitPathChunks(paths) {
		args := append([]string{"files", "--"}, chunk...)
		cmd := exec.CommandContext(ctx, "hg", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == 1 {
				continue // nothing in this batch is tracked
			}
			return nil, fmt.Errorf("hg files: %w", err)
		}
		tracked = append(tracked, splitLines(out)...)
	}
	return tracked, nil
}

// IgnoredFiles implements types.IgnoredFileReporter, returning the given paths the ignore
// rules cover, AS GIVEN.
//
// It shares IgnoredPaths' probe rather than calling `hg status --ignored`, because that
// command answers a different question than the interface asks: given a directory it
// EXPANDS it, so `status --ignored -- genx` reports "genx/a.o" where the contract says to
// echo "genx". A caller testing set membership against its own input would miss every
// directory it passed. Verified on Mercurial 7.x; Sapling behaves the same way.
//
// Sharing the probe also means the two cannot drift into disagreeing - the defect the git
// pair had, where its IgnoredFiles and IgnoredPaths returned opposite answers for a
// tracked-but-ignored path.
func (v hgVCS) IgnoredFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	ignored, err := v.IgnoredPaths(ctx, dir, paths)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if ignored[p] {
			out = append(out, p)
		}
	}
	return out, nil
}

// hgChurnTemplate opens each commit with its NUL-separated node, author and record date,
// then lists that commit's files one per line - the stream shape parseChangesByCommit
// reads, with the leading NUL sentinel supplied by the template itself.
const hgChurnTemplate = `\0{node}\0{person(author)}\0{date|rfc3339date}\n{files % "{file}\n"}`

// ChangesByCommit implements types.ChurnReporter. `-r` scopes the walk to the working
// directory's ancestors so a repository with several heads cannot attribute churn from a
// line of development this checkout is not on, and `not merge()` keeps a merge's sprawling
// file list from skewing attribution, matching git's --no-merges.
//
// reverse() is load-bearing: a bare `hg log` is newest-first, but `hg log -r <revset>`
// follows the REVSET's order, and ancestors() is ascending - so without it `-l N` returns
// the N OLDEST commits while the interface promises the newest.
//
// The `.` pathspec limits which COMMITS appear but NOT the files each lists: `{files}` is
// the changeset's whole file list, so a commit touching both root.txt and sub/a.txt reports
// both even when the log runs in sub/, where git's --name-only reports only sub/a.txt.
// Measured, and it matters because churn is attributed per project - the unfiltered list
// credits a nested workspace with edits made outside it. Hence the subtree filter here.
func (v hgVCS) ChangesByCommit(ctx context.Context, dir string, commits int, since string) ([]types.CommitChange, error) {
	if commits <= 0 {
		commits = 1
	}
	scope := "ancestors(.)"
	if since != "" {
		if err := checkRef(since); err != nil {
			return nil, err
		}
		scope = fmt.Sprintf("ancestors(.) and date('>%s')", since)
	}
	revset := fmt.Sprintf("reverse(%s) and not merge()", scope)
	out, err := vcsOutput(ctx, dir, "hg", "log", "-r", revset,
		"-l", fmt.Sprintf("%d", commits), "--template", hgChurnTemplate, "--", ".")
	if err != nil {
		return nil, fmt.Errorf("hg log: %w", err)
	}
	changes := parseChangesByCommit(out)
	_, prefix, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return nil, err
	}
	if prefix == "" {
		return changes, nil // dir IS the repository root; every file is in the subtree
	}
	for i := range changes {
		kept := changes[i].Files[:0]
		for _, f := range changes[i].Files {
			if strings.HasPrefix(f, prefix) {
				kept = append(kept, f)
			}
		}
		changes[i].Files = kept
	}
	return changes, nil
}

// hgArchivalMeta is the provenance file `hg archive` injects into every export. It belongs
// to no commit, so leaving it in would put a file in the exported tree that no revision
// contains - which a graph diff reads as a change.
const hgArchivalMeta = ".hg_archival.txt"

// ExportRevision implements types.RevisionExporter via `hg archive -t files`.
//
// Unlike Sapling's, hg's archive needs no explicit include set - `sl archive` refuses a
// whole-tree export without one, and hg does not. Both inject a provenance file, and both
// keep repository-relative paths where git's `archive <rev> -- .` re-roots them, so dir's
// prefix is stripped here to give the caller the subtree it asked about.
func (v hgVCS) ExportRevision(ctx context.Context, dir, rev, dstDir string) error {
	if rev == "" {
		rev = "."
	}
	if err := checkRef(rev); err != nil {
		return err
	}
	staging, err := os.MkdirTemp("", "magus-hg-export-")
	if err != nil {
		return fmt.Errorf("hg archive: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	cmd := exec.CommandContext(ctx, "hg", "archive", "-r", rev, "-t", "files",
		"-X", hgArchivalMeta, staging)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("hg archive %q: %w\n%s", rev, err, strings.TrimSpace(string(out)))
	}

	_, prefix, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return err
	}
	// A revision predating dir yields no subtree. That is an empty tree, not a failure -
	// git reports the same case as "everything was added".
	staged := filepath.Join(staging, filepath.FromSlash(prefix))
	if _, err := os.Stat(staged); os.IsNotExist(err) {
		return os.MkdirAll(dstDir, 0o755)
	}
	return copyTree(staged, dstDir)
}

// StartMerge begins a merge of ref without committing it. See types.MergeStarter.
//
// `--tool internal:merge` is not a preference, it is what keeps this from HANGING. With no
// tool named, Mercurial resolves ui.merge from the user's config and will happily launch an
// interactive one; measured here, a plain `hg merge` on a conflicting file blocked until
// killed, and `--noninteractive` alone did NOT prevent it. Naming an internal tool settles
// what it can and leaves the rest marked unresolved, which is exactly the state Conflicts
// reads. Sapling needed the same treatment for the same reason.
//
// There is no --no-commit to pass, and none is needed: `hg merge` never commits.
//
// A merge already underway is refused BEFORE starting, because it cannot be detected
// afterwards - the leftover merge's own conflicts would satisfy any "did conflicts appear"
// test, and the caller would resolve against a merge of a ref it never asked for.
func (v hgVCS) StartMerge(ctx context.Context, root, ref string) error {
	if err := checkRef(ref); err != nil {
		return err
	}
	if underway, err := v.mergeInProgress(ctx, root); err != nil {
		return err
	} else if underway {
		return fmt.Errorf("hg merge %s: a merge is already in progress; conclude or abandon it first", ref)
	}
	cmd := exec.CommandContext(ctx, "hg", "--noninteractive", "merge", "--tool", "internal:merge", ref)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// A merge that CONFLICTS exits non-zero, and that is the case this exists to set up.
	// Having ruled out a pre-existing operation above, merge state now means THIS merge began.
	if underway, uErr := v.mergeInProgress(ctx, root); uErr == nil && underway {
		return nil
	}
	return fmt.Errorf("hg merge %s: %w\n%s", ref, err, strings.TrimSpace(string(out)))
}

// mergeInProgress reports whether Mercurial has merge state recorded. `hg debugmergestate`
// prints "no merge state found" and exits 0 when there is none, so the absence has to be
// read from the output rather than the status.
func (v hgVCS) mergeInProgress(ctx context.Context, root string) (bool, error) {
	out, err := vcsOutputRaw(ctx, root, "hg", "debugmergestate")
	if err != nil {
		return false, fmt.Errorf("hg debugmergestate: %w", err)
	}
	return !strings.Contains(out, "no merge state found"), nil
}

// AbortMerge abandons the in-progress merge. See types.MergeStarter.
//
// `hg merge --abort` refuses on its own when nothing is underway, unlike Sapling's
// whole-tree revert - but the guard is kept so all three backends give the same error for
// the same condition instead of three different messages from three different CLIs.
func (v hgVCS) AbortMerge(ctx context.Context, root string) error {
	underway, err := v.mergeInProgress(ctx, root)
	if err != nil {
		return err
	}
	if !underway {
		return errors.New("hg: no merge is in progress to abort")
	}
	cmd := exec.CommandContext(ctx, "hg", "--noninteractive", "merge", "--abort")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("hg merge --abort: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
