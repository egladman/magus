package pulls

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/queue"
	pullsv1 "github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1"
	"github.com/egladman/magus/types"
)

// backlogMinChecks is how many checks must wait past the runner window
// before the board blames capacity instead of code.
const backlogMinChecks = 3

// boardInput is everything buildBoard reads, the clock included, so a
// board is a function of its input.
type boardInput struct {
	List   types.PullList
	Now    time.Time
	Config config.Pulls
	Filter authorFilter
}

// analyseBoard analyses the pull list into the board the console renders.
// Sync stays unset: the poller owns when the list was taken.
func analyseBoard(in boardInput) *pullsv1.Board {
	base := cmp.Or(in.List.Base.Name, "main")
	rows := visiblePulls(in)
	pulls := make([]*pullsv1.Pull, 0, len(rows))
	for _, p := range rows {
		pulls = append(pulls, pullRow(p, base, in))
	}
	overlaps(pulls, rows)
	patterns := findPatterns(rows, in)

	return &pullsv1.Board{
		Repo:       in.List.Repo,
		Filter:     in.Filter.proto(in.List.Viewer),
		Main:       mainState(in.List.Base, in.Now),
		Pulls:      pulls,
		Patterns:   patterns,
		MergeOrder: mergeOrder(pulls, patterns),
	}
}

func visiblePulls(in boardInput) []types.ForgePull {
	authors := in.Filter.Effective(in.List.Viewer)
	out := make([]types.ForgePull, 0, len(in.List.Pulls))
	for _, p := range in.List.Pulls {
		if authors == nil || slices.ContainsFunc(authors, func(a string) bool {
			return strings.EqualFold(a, p.Author)
		}) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b types.ForgePull) int {
		return cmp.Compare(a.Number, b.Number)
	})
	return out
}

// rollup is a commit's checks with the merge-queue context set aside.
type rollup struct {
	state     pullsv1.CiState
	passed    int
	failed    int
	pending   int
	hasQueued bool
	queuedFor time.Duration
	failures  []types.ForgeCheck
	queue     *pullsv1.QueueStatus
}

// rollupChecks keeps the queue context out of the counts. It says where
// a pull sits in the queue, not whether CI passed.
func rollupChecks(checks []types.ForgeCheck, now time.Time) rollup {
	var r rollup
	var running int
	var earliest time.Time
	for _, c := range checks {
		if c.Name == queue.DefaultStatusContext {
			r.queue = &pullsv1.QueueStatus{State: c.State, Description: c.Description}
			continue
		}
		switch c.State {
		case "passed", "skipped":
			r.passed++
		case "failed":
			r.failed++
			r.failures = append(r.failures, c)
		case "running":
			r.pending++
			running++
		case "queued":
			r.pending++
			r.hasQueued = true
			if q := parseTime(c.QueuedAt); !q.IsZero() && (earliest.IsZero() || q.Before(earliest)) {
				earliest = q
			}
		}
	}
	if !earliest.IsZero() {
		r.queuedFor = max(now.Sub(earliest), 0)
	}
	switch {
	case r.failed > 0:
		r.state = pullsv1.CiState_CI_STATE_RED
	case running > 0:
		r.state = pullsv1.CiState_CI_STATE_RUNNING
	case r.hasQueued:
		r.state = pullsv1.CiState_CI_STATE_QUEUED
	case r.passed > 0:
		r.state = pullsv1.CiState_CI_STATE_GREEN
	default:
		r.state = pullsv1.CiState_CI_STATE_NONE
	}
	return r
}

func firstLine(c types.ForgeCheck) string {
	if len(c.Lines) > 0 {
		return c.Lines[0]
	}
	return c.Description
}

func mainState(b types.BaseBranch, now time.Time) *pullsv1.BranchState {
	r := rollupChecks(b.Checks, now)
	s := &pullsv1.BranchState{
		Name:    cmp.Or(b.Name, "main"),
		Sha:     b.SHA,
		Url:     b.URL,
		Ci:      r.state,
		Passed:  int32(r.passed),
		Failed:  int32(r.failed),
		Pending: int32(r.pending),
	}
	if len(r.failures) > 0 {
		s.FirstFailure = failedJob(r.failures[0])
	}
	return s
}

func pullRow(p types.ForgePull, base string, in boardInput) *pullsv1.Pull {
	r := rollupChecks(p.Checks, in.Now)
	row := &pullsv1.Pull{
		Number:    int32(p.Number),
		Title:     p.Title,
		Author:    p.Author,
		Url:       p.URL,
		Branch:    p.Branch,
		HeadSha:   p.HeadSHA,
		BehindBy:  int32(p.BehindBy),
		Mergeable: mergeableOf(p.Mergeable),
		Ci:        r.state,
		Passed:    int32(r.passed),
		Failed:    int32(r.failed),
		Pending:   int32(r.pending),
		Review:    reviewOf(p.Review),
		Queue:     r.queue,
		Draft:     p.Draft,
	}
	if r.hasQueued {
		row.QueuedFor = durationOf(r.queuedFor)
	}
	for _, c := range r.failures {
		row.FailedJobs = append(row.FailedJobs, failedJob(c))
	}
	if len(row.FailedJobs) > 0 {
		row.FirstFailure = row.FailedJobs[0]
	}
	if pushed := parseTime(p.PushedAt); !pushed.IsZero() {
		row.PushedAt = timestampOf(pushed)
		row.Stale = in.Now.Sub(pushed) > in.Config.StaleAfter
	}
	row.Blocked = blockedReasons(p, r, base, in.Config.RunnerWait)
	return row
}

func blockedReasons(p types.ForgePull, r rollup, base string, runnerWait time.Duration) []string {
	var out []string
	if p.Mergeable == "conflicting" {
		out = append(out, "conflicts with "+base)
	}
	for _, c := range r.failures {
		reason := fmt.Sprintf("`%s` failing", c.Name)
		if line := firstLine(c); line != "" {
			reason += ": " + line
		}
		out = append(out, reason)
	}
	if r.hasQueued && r.queuedFor > runnerWait {
		out = append(out, "waiting on runners for "+formatDuration(r.queuedFor.Truncate(time.Minute)))
	}
	switch p.Review {
	case "changes_requested":
		out = append(out, "changes requested")
	case "review_required":
		out = append(out, "review required")
	}
	if p.Draft {
		out = append(out, "draft")
	}
	if p.BehindBy > 0 && p.Mergeable != "mergeable" && p.Mergeable != "conflicting" {
		out = append(out, plural(p.BehindBy, "commit", "commits")+" behind "+base)
	}
	return out
}

// overlaps fills each row with the other rows that share a changed file.
func overlaps(rows []*pullsv1.Pull, src []types.ForgePull) {
	for i, p := range src {
		mine := make(map[string]bool, len(p.Files))
		for _, f := range p.Files {
			mine[f] = true
		}
		for j, q := range src {
			if i == j {
				continue
			}
			var shared []string
			for _, f := range q.Files {
				if mine[f] {
					shared = append(shared, f)
				}
			}
			if len(shared) > 0 {
				slices.Sort(shared)
				rows[i].Overlaps = append(rows[i].Overlaps, &pullsv1.Overlap{Number: int32(q.Number), Files: shared})
			}
		}
	}
}

func findPatterns(rows []types.ForgePull, in boardInput) []*pullsv1.Pattern {
	out := regressions(rows, in)
	if p := runnerBacklog(rows, in); p != nil {
		out = append(out, p)
	}
	return out
}

type failureKey struct{ check, line string }

// regressions finds checks that fail the same way on several pulls. That
// points at the base branch more than at any one change.
func regressions(rows []types.ForgePull, in boardInput) []*pullsv1.Pattern {
	type group struct {
		pulls    map[int]bool
		earliest time.Time
	}
	groups := map[failureKey]*group{}
	for _, p := range rows {
		for _, c := range p.Checks {
			if c.Name == queue.DefaultStatusContext || c.State != "failed" {
				continue
			}
			k := failureKey{c.Name, firstLine(c)}
			g := groups[k]
			if g == nil {
				g = &group{pulls: map[int]bool{}}
				groups[k] = g
			}
			g.pulls[p.Number] = true
			if s := parseTime(c.StartedAt); !s.IsZero() && (g.earliest.IsZero() || s.Before(g.earliest)) {
				g.earliest = s
			}
		}
	}
	var out []*pullsv1.Pattern
	for k, g := range groups {
		if len(g.pulls) < 2 {
			continue
		}
		pat := &pullsv1.Pattern{
			Kind:      pullsv1.PatternKind_PATTERN_KIND_REGRESSION,
			Check:     k.check,
			FirstLine: k.line,
			OnMain:    mainFails(in.List.Base, k.check, in.Now),
		}
		for n := range g.pulls {
			pat.Pulls = append(pat.Pulls, int32(n))
		}
		slices.Sort(pat.Pulls)
		if c, ok := commitBefore(in.List.Base.Commits, g.earliest); ok {
			pat.SinceSha, pat.SinceSubject, pat.SincePull = c.SHA, c.Subject, int32(c.Pull)
		}
		pat.Message = regressionMessage(pat)
		out = append(out, pat)
	}
	slices.SortFunc(out, func(a, b *pullsv1.Pattern) int {
		return cmp.Or(cmp.Compare(a.Check, b.Check), cmp.Compare(a.FirstLine, b.FirstLine))
	})
	return out
}

func mainFails(b types.BaseBranch, check string, now time.Time) bool {
	for _, c := range rollupChecks(b.Checks, now).failures {
		if c.Name == check {
			return true
		}
	}
	return false
}

// commitBefore returns the newest commit at or before t. Commits arrive
// newest first.
func commitBefore(commits []types.BaseCommit, t time.Time) (types.BaseCommit, bool) {
	if t.IsZero() {
		return types.BaseCommit{}, false
	}
	for _, c := range commits {
		if at := parseTime(c.At); !at.IsZero() && !at.After(t) {
			return c, true
		}
	}
	return types.BaseCommit{}, false
}

func regressionMessage(p *pullsv1.Pattern) string {
	msg := fmt.Sprintf("%s fail on %s", plural(len(p.Pulls), "PR", "PRs"), p.Check)
	if p.OnMain {
		msg += ", and main fails it too"
	}
	if p.SinceSha != "" {
		msg += fmt.Sprintf("; started after %s %s", shortSHA(p.SinceSha), p.SinceSubject)
		if p.SincePull > 0 {
			msg += fmt.Sprintf(" (#%d)", p.SincePull)
		}
	}
	return msg
}

func runnerBacklog(rows []types.ForgePull, in boardInput) *pullsv1.Pattern {
	var n int
	seen := map[int32]bool{}
	var pulls []int32
	for _, p := range rows {
		for _, c := range p.Checks {
			q := parseTime(c.QueuedAt)
			if c.Name == queue.DefaultStatusContext || c.State != "queued" || q.IsZero() {
				continue
			}
			if in.Now.Sub(q) > in.Config.RunnerWait {
				n++
				if !seen[int32(p.Number)] {
					seen[int32(p.Number)] = true
					pulls = append(pulls, int32(p.Number))
				}
			}
		}
	}
	if n < backlogMinChecks {
		return nil
	}
	return &pullsv1.Pattern{
		Kind:    pullsv1.PatternKind_PATTERN_KIND_RUNNER_BACKLOG,
		Pulls:   pulls,
		Message: fmt.Sprintf("%d runs queued over %s: runner backlog, not code", n, formatDuration(in.Config.RunnerWait)),
	}
}

// mergeOrder ranks ready pulls first, then those held only by a
// regression, then the rest by number.
func mergeOrder(pulls []*pullsv1.Pull, patterns []*pullsv1.Pattern) []*pullsv1.MergeStep {
	var ready, mergeable, held, rest []*pullsv1.MergeStep
	for _, p := range pulls {
		ok := p.Ci == pullsv1.CiState_CI_STATE_GREEN &&
			p.Mergeable == pullsv1.Mergeable_MERGEABLE_MERGEABLE &&
			!p.Draft && p.Review != pullsv1.Review_REVIEW_CHANGES_REQUESTED
		step := &pullsv1.MergeStep{Number: p.Number}
		switch {
		case ok && p.Review == pullsv1.Review_REVIEW_APPROVED:
			step.Note = "green, mergeable, approved"
			ready = append(ready, step)
		case ok:
			step.Note = "green, mergeable"
			mergeable = append(mergeable, step)
		default:
			if check, only := onlyRegression(p, patterns); only {
				step.Note = "waits on the main regression in " + check
				held = append(held, step)
				continue
			}
			if len(p.Blocked) > 0 {
				step.Note = p.Blocked[0]
			}
			rest = append(rest, step)
		}
	}
	return slices.Concat(ready, mergeable, held, rest)
}

// onlyRegression reports whether every failure of p belongs to a
// regression pattern covering it, and names the first such check.
func onlyRegression(p *pullsv1.Pull, patterns []*pullsv1.Pattern) (string, bool) {
	if len(p.FailedJobs) == 0 {
		return "", false
	}
	var first string
	for _, j := range p.FailedJobs {
		i := slices.IndexFunc(patterns, func(pt *pullsv1.Pattern) bool {
			return pt.Kind == pullsv1.PatternKind_PATTERN_KIND_REGRESSION &&
				pt.Check == j.Name && slices.Contains(pt.Pulls, p.Number)
		})
		if i < 0 {
			return "", false
		}
		if first == "" {
			first = patterns[i].Check
		}
	}
	return first, true
}
