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

type gitVCS struct{}

func (gitVCS) Name() string     { return "git" }
func (gitVCS) Claims() []string { return []string{".git"} }
func (gitVCS) Base() string     { return "origin/main" }

func (gitVCS) Root(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (gitVCS) Diff(ctx context.Context, dir, base string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", base+"...HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	return splitLines(out), nil
}

func (gitVCS) DiffCommands(ctx context.Context, dir, base string) (types.DiffCommandHints, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return types.DiffCommandHints{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	return types.DiffCommandHints{
		CLI: fmt.Sprintf("git diff %s...%s", base, sha),
		GUI: fmt.Sprintf("git difftool %s...%s", base, sha),
	}, nil
}

func (v gitVCS) Bisect(ctx context.Context, dir string, opts types.BisectOptions) (types.Culprit, error) {
	if opts.Good == "" {
		sha, err := v.commitBeforeTime(ctx, dir, opts.GoodBefore)
		if err != nil {
			return types.Culprit{}, err
		}
		opts.Good = sha
	}
	if err := v.isAncestor(ctx, dir, opts.Good); err != nil {
		return types.Culprit{}, fmt.Errorf("good commit %q is not an ancestor of HEAD: %w", opts.Good, err)
	}
	bad := opts.Bad
	if bad == "" {
		bad = "HEAD"
	}

	if err := v.start(ctx, dir, bad, opts.Good); err != nil {
		return types.Culprit{}, fmt.Errorf("git bisect start: %w", err)
	}
	defer func() { _ = v.reset(context.WithoutCancel(ctx), dir) }()

	if err := v.run(ctx, dir, opts.TestCmd); err != nil {
		slog.WarnContext(ctx, "git bisect run exited with error", slog.String("err", err.Error()))
	}

	sha, err := v.culprit(ctx, dir)
	if err != nil {
		return types.Culprit{}, err
	}
	info, _ := v.commitInfo(ctx, dir, sha)
	return types.Culprit{SHA: sha, Info: info}, nil
}

// isAncestor, commitBeforeTime and commitInfo run via `git -C dir` so they target
// the repository under bisect, not the process cwd — the dir-scoping the
// VCSDriver contract requires for correctness under concurrent runs.
func (gitVCS) isAncestor(ctx context.Context, dir, sha string) error {
	return exec.CommandContext(ctx, "git", "-C", dir, "merge-base", "--is-ancestor", sha, "HEAD").Run()
}

func (gitVCS) commitBeforeTime(ctx context.Context, dir string, t time.Time) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "log",
		"--before="+t.UTC().Format(time.RFC3339),
		"-n", "1", "--format=%H").Output()
	if err != nil {
		return "", fmt.Errorf("git log: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", errors.New("no commit found before the last passing run")
	}
	return sha, nil
}

func (gitVCS) commitInfo(ctx context.Context, dir, sha string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "log", "-1",
		"--format=%s  (%an, %ad)", "--date=short", sha).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (gitVCS) start(ctx context.Context, dir, bad, good string) error {
	cmd := exec.CommandContext(ctx, "git", "bisect", "start", bad, good)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (gitVCS) run(ctx context.Context, dir, shellCmd string) error {
	cmd := exec.CommandContext(ctx, "git", "bisect", "run", "sh", "-c", shellCmd)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (gitVCS) reset(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "git", "bisect", "reset")
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (gitVCS) culprit(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "bisect", "log").Output()
	if err != nil {
		return "", fmt.Errorf("git bisect log: %w", err)
	}
	for _, line := range bytes.Split(out, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if strings.HasPrefix(s, "# first bad commit: [") {
			after := strings.TrimPrefix(s, "# first bad commit: [")
			sha := strings.SplitN(after, "]", 2)[0]
			if sha != "" {
				return sha, nil
			}
		}
	}
	return "", errors.New("could not parse culprit from git bisect log")
}

func (gitVCS) Metadata(ctx context.Context, dir string) (types.VCSMeta, error) {
	shortHash, err := vcsOutput(ctx, dir, "git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return types.VCSMeta{}, err
	}
	hash, _ := vcsOutput(ctx, dir, "git", "rev-parse", "HEAD")
	branch, _ := vcsOutput(ctx, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	commitDate, _ := vcsOutput(ctx, dir, "git", "log", "-1", "--format=%ci")
	// Don't swallow the dirty-probe error: a failed status must not be reported as
	// a clean tree (that would stamp a dirty build as clean).
	dirtyOut, err := vcsOutput(ctx, dir, "git", "status", "--porcelain")
	if err != nil {
		return types.VCSMeta{}, fmt.Errorf("git status: %w", err)
	}
	return types.VCSMeta{
		ShortHash:  shortHash,
		Hash:       hash,
		Branch:     branch,
		CommitDate: commitDate,
		IsDirty:    dirtyOut != "",
	}, nil
}

// gitCommitFormat emits the NUL-delimited fields parseCommit expects: id, short,
// author name/email, the commit (record) date as strict ISO 8601 / RFC 3339
// (%cI), parents, and the raw message (%B).
const gitCommitFormat = "%H%x00%h%x00%an%x00%ae%x00%cI%x00%P%x00%B"

func (gitVCS) FindCommit(ctx context.Context, dir, rev string) (types.Commit, error) {
	if rev == "" {
		rev = "HEAD"
	}
	out, err := vcsOutput(ctx, dir, "git", "log", "-1", "--format="+gitCommitFormat, rev)
	if err != nil {
		return types.Commit{}, fmt.Errorf("git log %s: %w", rev, err)
	}
	c := parseCommit(out)
	if c.ID == "" {
		return types.Commit{}, fmt.Errorf("git: no commit for %q", rev)
	}
	return c, nil
}

func (v gitVCS) History(ctx context.Context, dir string, limit int) ([]types.Commit, error) {
	if limit <= 0 {
		limit = 1
	}
	out, err := vcsOutput(ctx, dir, "git", "log", fmt.Sprintf("-%d", limit), "--format=%H")
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return resolveEach(ctx, dir, v, splitLines([]byte(out)))
}

const (
	gitAttrsBegin  = "# BEGIN magus-generated — do not edit this section manually"
	gitAttrsEnd    = "# END magus-generated"
	gitMergeDriver = "magus merge-driver %O %A %B %L %P"
)

// InstallMergeDriver writes .gitattributes entries and registers the magus merge driver.
func (v gitVCS) InstallMergeDriver(ctx context.Context, root string, outputGlobs []string) error {
	if err := v.writeGitAttrs(root, outputGlobs); err != nil {
		return err
	}
	return v.writeGitConfig(ctx, root)
}

// CheckMergeDriver reports whether both .gitattributes and git config driver registration are present.
func (v gitVCS) CheckMergeDriver(ctx context.Context, root string) (bool, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "config", "merge.magus.driver").Output()
	if err != nil {
		return false, nil // not configured; not an error
	}
	if strings.TrimSpace(string(out)) == "" {
		return false, nil
	}
	attrsPath := filepath.Join(root, ".gitattributes")
	data, err := os.ReadFile(attrsPath)
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(data), gitAttrsBegin), nil
}

func (gitVCS) writeGitAttrs(root string, outputGlobs []string) error {
	attrsPath := filepath.Join(root, ".gitattributes")
	var section strings.Builder
	section.WriteString(gitAttrsBegin + "\n")
	for _, glob := range outputGlobs {
		fmt.Fprintf(&section, "%s merge=magus linguist-generated\n", glob)
	}
	section.WriteString(gitAttrsEnd + "\n")
	existing, _ := os.ReadFile(attrsPath)
	updated := replaceManagedSection(string(existing), section.String(), gitAttrsBegin, gitAttrsEnd)
	return os.WriteFile(attrsPath, []byte(updated), 0o644)
}

func (gitVCS) writeGitConfig(ctx context.Context, root string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "config", "merge.magus.driver", gitMergeDriver)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git config merge.magus.driver: %w\n%s", err, out)
	}
	return nil
}

// vcsOutput runs a VCS subcommand in dir and returns its trimmed stdout.
// An empty dir uses the process working directory (the exec.Cmd.Dir convention).
func vcsOutput(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func splitLines(out []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
