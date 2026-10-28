package bench

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
)

// TaskKind says what a task asks for and so how it is graded.
type TaskKind string

const (
	// TaskBugfix is a code change graded fail-to-pass: the held-out TestPatch is applied
	// to the trial's tree, every FailToPass test must pass, and so must every test of
	// Packages (history) or every PassToPass test (SWE-bench).
	TaskBugfix TaskKind = "bugfix"
	// TaskInterrogation is a question about the code at BaseCommit, answered with a set of
	// items and graded by tp/fp/fn against Answer. The tree is not changed.
	TaskInterrogation TaskKind = "interrogation"
	// TaskRegeneration is a source change whose commit also rewrote Generated. The target
	// that writes each path (`magus describe file` in the trial tree) must leave no drift,
	// and FailToPass and Packages pass as for a bugfix when the task names them.
	TaskRegeneration TaskKind = "regeneration"
)

// TagCapability is the tag of a task that counts toward the headline numbers.
const TagCapability = "capability"

// tagRegression is a task every cell passed 3 of 3 twice running: reported, but out of
// the headline.
const tagRegression = "regression"

// Suite names a task file under benchmarks/agent/tasks/, which is <suite>.jsonl.
const (
	SuiteSWEBenchMultilingualGo = "swebench-multilingual-go"
	SuiteHistoryGo              = "history-go"
)

// Task is one benchmark task: a line of benchmarks/agent/tasks/<suite>.jsonl, which
// cmd/benchtasks writes and the runner reads with [LoadTasks]. The json tags are the file
// format. Diffs are unified diffs against BaseCommit as `git apply` takes them.
// A trial seeds a tree at BaseCommit, shows the agent Prompt and nothing else from the
// record, and grades the tree the agent leaves by Kind. The golden control applies Patch
// and TestPatch to the seed and must pass; the null control applies TestPatch alone and
// must fail. An interrogation task has neither diff, and its controls are Answer itself
// (all tp) and an empty answer (all fn).
type Task struct {
	Suite string `json:"suite"`
	// ID is unique within the suite: the SWE-bench instance_id, or a slug of the fix
	// commit's subject.
	ID string `json:"task_id"`
	// SHA is "sha256:<hex>" over the record encoded with SHA empty; see [Task.Digest]. A
	// reworded or regraded task gets a new SHA, so a result keyed on it drops out of any
	// pairing with results of the old wording.
	SHA  string   `json:"task_sha"`
	Kind TaskKind `json:"kind"`
	// Tag is TagCapability, or "regression" once the task leaves the headline.
	Tag string `json:"tag"`
	// Created is when the fix landed upstream, RFC 3339 in UTC; a report sets it beside the
	// model's training cutoff.
	Created string `json:"task_created"`
	// Repo is the GitHub owner/name the task comes from.
	Repo string `json:"repo"`
	// License is the SPDX id the task data is used under.
	License    string `json:"license"`
	BaseCommit string `json:"base_commit"`
	// FixCommit is the commit Patch and TestPatch come from; empty for SWE-bench, whose rows
	// name only the base.
	FixCommit string `json:"fix_commit,omitempty"`
	// Prompt is the problem statement: the issue for SWE-bench, the fix commit's message
	// for a history task, the question for an interrogation task.
	Prompt string `json:"prompt"`
	// Patch is the reference change minus tests, applied only by the golden control.
	Patch string `json:"patch,omitempty"`
	// TestPatch is the held-out tests, never in the trial's tree until it is graded.
	TestPatch string `json:"test_patch,omitempty"`
	// FailToPass are the `go test -v` names (Top or Top/Sub) that must fail at the seed
	// and pass after the change.
	FailToPass []string `json:"fail_to_pass,omitempty"`
	// PassToPass are the `go test -v` names that pass at the seed and must still pass.
	// History tasks leave it empty and name Packages instead.
	PassToPass []string `json:"pass_to_pass,omitempty"`
	// Packages are import paths whose whole `go test` run must pass at grade time, which
	// is what stops an agent from deleting a test it cannot fix.
	Packages []string `json:"packages,omitempty"`
	// Generated are the repo-relative generated paths the fix commit rewrote.
	Generated []string `json:"generated,omitempty"`
	// Answer is the key of an interrogation task.
	Answer *AnswerKey `json:"answer,omitempty"`
	// SWEBench carries how the dataset itself grades the row.
	SWEBench *SWEBenchGrading `json:"swebench,omitempty"`
}

// AnswerKey is the expected answer to an interrogation task, from an oracle independent of
// magus so neither arm is graded by its own tool. An answer is scored item by item: tp is
// an Items member the agent named, fp a name outside Items, fn an Items member it missed.
type AnswerKey struct {
	// Items is the expected set, sorted and without duplicates.
	Items []string `json:"items"`
	// Oracle is the command that produced Items, run at BaseCommit.
	Oracle string `json:"oracle"`
}

// SWEBenchGrading is the part of a SWE-bench row that grades it in the dataset's own
// container. It is kept verbatim so the grade means what it means on the leaderboard.
type SWEBenchGrading struct {
	// Image is the evaluation image as the row names it, by tag; the runner pulls it and
	// records the digest it got.
	Image string `json:"image"`
	// EvalScript is the shell script that applies TestPatch and runs the tests.
	EvalScript string `json:"eval_script"`
	// LogParser names the format of the eval log, which cmd/swegrade looks up.
	LogParser string `json:"log_parser"`
	Version   string `json:"version"`
}

// Digest returns "sha256:<hex>" over t encoded with SHA empty, whatever SHA holds.
func (t Task) Digest() (string, error) {
	t.SHA = ""
	b, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("task %s: %w", t.ID, err)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Seal validates t and sets SHA to its digest.
func (t *Task) Seal() error {
	if err := t.Validate(); err != nil {
		return err
	}
	sha, err := t.Digest()
	if err != nil {
		return err
	}
	t.SHA = sha
	return nil
}

// Validate reports every field t lacks for its Kind, or nil. It does not check SHA.
func (t Task) Validate() error {
	var errs []error
	need := func(ok bool, field string) {
		if !ok {
			errs = append(errs, fmt.Errorf("%s is empty", field))
		}
	}
	need(t.Suite != "", "suite")
	need(t.ID != "", "task_id")
	need(t.Created != "", "task_created")
	need(t.Repo != "", "repo")
	need(t.License != "", "license")
	need(t.BaseCommit != "", "base_commit")
	need(t.Prompt != "", "prompt")
	switch t.Tag {
	case TagCapability, tagRegression:
	default:
		errs = append(errs, fmt.Errorf("tag %q is neither %s nor %s", t.Tag, TagCapability, tagRegression))
	}
	switch t.Kind {
	case TaskBugfix:
		need(t.Patch != "", "patch")
		need(t.TestPatch != "", "test_patch")
		need(len(t.FailToPass) > 0, "fail_to_pass")
		// A SWE-bench row may have no PASS_TO_PASS; the dataset grades it so.
		need(len(t.PassToPass) > 0 || len(t.Packages) > 0 || t.SWEBench != nil, "pass_to_pass and packages")
	case TaskInterrogation:
		need(t.Answer != nil && len(t.Answer.Items) > 0, "answer.items")
		need(t.Answer != nil && t.Answer.Oracle != "", "answer.oracle")
	case TaskRegeneration:
		need(t.Patch != "", "patch")
		need(len(t.Generated) > 0, "generated")
	default:
		errs = append(errs, fmt.Errorf("kind %q is not one of %s, %s, %s", t.Kind, TaskBugfix, TaskInterrogation, TaskRegeneration))
	}
	if t.SWEBench != nil {
		need(t.SWEBench.Image != "", "swebench.image")
		need(t.SWEBench.EvalScript != "", "swebench.eval_script")
		need(t.SWEBench.LogParser != "", "swebench.log_parser")
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("task %q: %w", t.ID, err)
	}
	return nil
}

// LoadTasks reads a task file. It refuses the whole file when a line does not decode, fails
// [Task.Validate], carries a SHA that is not its digest, or repeats an ID, and when the file
// ends in a line no newline closes; a suite with a bad task is not a smaller suite.
func LoadTasks(path string) ([]Task, error) {
	read, err := file.ReadLines(path)
	if err != nil {
		return nil, err
	}
	if read.Partial {
		return nil, fmt.Errorf("%s ends in a line no newline closes", path)
	}
	if len(read.Lines) == 0 {
		return nil, fmt.Errorf("%s holds no tasks", path)
	}
	seen := make(map[string]int, len(read.Lines))
	tasks := make([]Task, 0, len(read.Lines))
	for _, line := range read.Lines {
		var t Task
		if err := json.Unmarshal(line.Text, &t); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line.N, err)
		}
		if err := t.Validate(); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line.N, err)
		}
		sha, err := t.Digest()
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line.N, err)
		}
		if t.SHA != sha {
			return nil, fmt.Errorf("%s:%d: task %q carries task_sha %q but digests to %q", path, line.N, t.ID, t.SHA, sha)
		}
		if first, ok := seen[t.ID]; ok {
			return nil, fmt.Errorf("%s:%d: task %q repeats line %d", path, line.N, t.ID, first)
		}
		seen[t.ID] = line.N
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// WriteTasks seals every task and replaces the file at path with them, one per line in ID
// order. Nothing is written when any task fails to seal.
func WriteTasks(path string, tasks []Task) error {
	sorted := slices.Clone(tasks)
	slices.SortFunc(sorted, func(a, b Task) int { return cmp.Compare(a.ID, b.ID) })
	lines := make([][]byte, 0, len(sorted))
	for i := range sorted {
		if err := sorted[i].Seal(); err != nil {
			return err
		}
		if i > 0 && sorted[i].ID == sorted[i-1].ID {
			return fmt.Errorf("task %q appears twice", sorted[i].ID)
		}
		b, err := json.Marshal(sorted[i])
		if err != nil {
			return err
		}
		lines = append(lines, b)
	}
	return file.RewriteLines(path, lines, 0o644)
}

// CutClone makes dest a repository holding BaseCommit and its ancestors and nothing else:
// one branch, main, checked out at BaseCommit, no remote, no tags, no reflog. A trial seeded
// from it cannot reach the fix through git log or a ref. source is any repository git can
// fetch from that holds BaseCommit; dest must not exist or be empty.
func (t Task) CutClone(ctx context.Context, source, dest string) error {
	if t.BaseCommit == "" {
		return fmt.Errorf("task %q has no base_commit to cut a clone at", t.ID)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	steps := [][]string{
		{"init", "--quiet", "--initial-branch=main"},
		{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--update-head-ok", source, t.BaseCommit + ":refs/heads/main"},
		{"reset", "--quiet", "--hard", "main"},
		{"reflog", "expire", "--expire=now", "--all"},
	}
	for _, args := range steps {
		if err := git(ctx, dest, args...); err != nil {
			return fmt.Errorf("task %q: cut a clone at %s: %w", t.ID, t.BaseCommit, err)
		}
	}
	return nil
}

func git(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
