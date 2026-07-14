package vcs

import (
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
func (v hgVCS) Base() string     { return "tip" }

func (v hgVCS) Root(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "hg", "root")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (v hgVCS) Diff(ctx context.Context, dir, base string) ([]string, error) {
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
	return types.Culprit{SHA: sha, Info: info}, nil
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
		ShortHash:  shortHash,
		Hash:       hash,
		Branch:     branch,
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

func (v hgVCS) DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	args := []string{"status"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := vcsOutput(ctx, dir, "hg", args...)
	if err != nil {
		return nil, fmt.Errorf("hg status: %w", err)
	}
	return splitStatusLines(out), nil
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

// hgCommitTemplate emits the NUL-delimited fields parseCommit expects: node,
// short node, author name/email, the record date (RFC 3339), parents, and the
// full message. \0 is the field delimiter Mercurial converts to NUL.
const hgCommitTemplate = `{node}\0{node|short}\0{person(author)}\0{email(author)}\0{date|rfc3339date}\0{parents % "{node} "}\0{desc}`

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

const (
	hgRCBegin = "# BEGIN magus-generated — do not edit this section manually"
	hgRCEnd   = "# END magus-generated"

	hgHookBegin = "# BEGIN magus-refresh — do not edit this section manually"
	hgHookEnd   = "# END magus-refresh"
)

// InstallMergeDriver writes [merge-patterns] and [merge-tools] to .hg/hgrc.
func (v hgVCS) InstallMergeDriver(_ context.Context, root string, outputGlobs []string) error {
	hgrcPath := filepath.Join(root, ".hg", "hgrc")
	var section strings.Builder
	section.WriteString(hgRCBegin + "\n")
	section.WriteString("[merge-patterns]\n")
	for _, glob := range outputGlobs {
		fmt.Fprintf(&section, "glob:%s = magus\n", glob)
	}
	section.WriteString("\n[merge-tools]\n")
	section.WriteString("magus.executable = magus\n")
	section.WriteString("magus.args = merge-driver $base $local $other 0 $local\n")
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

// InstallRefreshHook implements types.RefreshHookInstaller: it registers an hg `update`
// hook (fires after a working-directory change - checkout, pull-update) that runs
// command. It shares replaceManagedSection with the merge-driver install, under its own
// markers so the two managed sections coexist in .hg/hgrc. Returns the hook label.
func (v hgVCS) InstallRefreshHook(_ context.Context, root, command string) ([]string, error) {
	hgrcPath := filepath.Join(root, ".hg", "hgrc")
	var section strings.Builder
	section.WriteString(hgHookBegin + "\n")
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
