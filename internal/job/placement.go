package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	qtypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// Place is where a row's work stands, read from the commit the row recorded.
type Place string

const (
	// PlaceMain: a base holds the head's content.
	PlaceMain Place = "main"
	// PlaceProposed: an open change's head holds it.
	PlaceProposed Place = "proposed"
	// PlacePushed: a remote-tracking ref reaches it and no open change holds it.
	PlacePushed Place = "pushed"
	// PlaceLocal: only this repository holds it.
	PlaceLocal Place = "local"
	// PlaceUnread: the read could not answer. It is never read as kept.
	PlaceUnread Place = "unread"
)

// ChangeListMaxAge is how old a change list may be and still place a row on
// an open change. A change closed unmerged since the read would keep its rows
// proposed, so past this age no row is.
const ChangeListMaxAge = time.Hour

// OpenChanges is the change list `magus queue ls` left. A placement reads it
// and never the network.
type OpenChanges struct {
	// Fetch is the read the changes came from, nil when the queue was never read.
	Fetch   *InflightFetch
	Changes qtypes.Changes
}

// Placed is where one row's work stands.
type Placed struct {
	Job   string `json:"job"`
	Head  string `json:"head,omitempty"`
	Place Place  `json:"place"`
	// Change is the open change whose head holds Head, for PlaceProposed.
	Change string `json:"change,omitempty"`
	// Branch is a local branch standing at Head, "" when no name holds it.
	Branch string `json:"branch,omitempty"`
	// Commits is how many of Head's commits the nearer base lacks.
	Commits int `json:"commits,omitempty"`
	// Why and Fix say, for PlaceUnread, what did not read and the command that
	// makes it answer.
	Why string `json:"why,omitempty"`
	Fix string `json:"fix,omitempty"`
}

// Waiting reports work no base holds and no open change carries.
func (p Placed) Waiting() bool { return p.Place == PlacePushed || p.Place == PlaceLocal }

// Name is what a person reads the work by: its branch, else its short head.
func (p Placed) Name() string {
	if p.Branch != "" {
		return p.Branch
	}
	return p.Head[:min(len(p.Head), 12)]
}

// Placements places each over row of root's own tree ([ownTree]) a holder
// wrote in, in store order. It leaves out a row a live integration names, one
// that landed, and one a person set aside. The error is a read that answers
// for no row; note names what the change list or the remote left unread.
func Placements(ctx context.Context, root string, rows []job.Job, open OpenChanges, now time.Time) ([]Placed, string, error) {
	work := placedRows(rows, root)
	if len(work) == 0 {
		return nil, "", nil
	}
	driver, why := resolveDriver(ctx, root)
	if driver == nil {
		return nil, "", errors.New(why)
	}
	return placeWith(ctx, driver, root, work, open, now)
}

// placedRows is the rows whose work [Placements] reads.
func placedRows(rows []job.Job, root string) []job.Job {
	carried := map[string]bool{}
	for _, r := range rows {
		if r.State.Live() {
			for _, id := range r.Integrates {
				carried[id] = true
			}
		}
	}
	var out []job.Job
	for _, r := range rows {
		finished := r.State.Terminal() || r.State == job.StateExited
		wrote := r.Registered != 0 && !r.ReadOnly && r.Delivery != job.DeliveryFindings && !r.ServerRun()
		if finished && wrote && !r.Landed && r.LeftUnmerged == "" && !carried[r.ID] && ownTree(rows, root, r) {
			out = append(out, r)
		}
	}
	return out
}

// placeWith places work against every integration base. A head already read
// against a base or a change head is answered from missingCommitsFile.
func placeWith(ctx context.Context, driver types.VCSDriver, root string, work []job.Job, open OpenChanges, now time.Time) ([]Placed, string, error) {
	bases, note, err := integrationBases(ctx, driver, root)
	if err != nil {
		return nil, "", err
	}
	tips, err := driver.BranchHeads(ctx, root)
	if err != nil {
		return nil, "", fmt.Errorf("the branch heads: %w", err)
	}
	upstreams, err := branchUpstreams(ctx, driver, root)
	if err != nil {
		return nil, "", fmt.Errorf("the branch upstreams: %w", err)
	}
	changes, changeNote := usableChanges(ctx, driver, root, open, now)
	r := placeRead{driver: driver, root: root, bases: bases, tips: tips, upstreams: upstreams, changes: changes,
		answers: readMissingCache(root), mu: &sync.Mutex{}}
	out := make([]Placed, len(work))
	var g errgroup.Group
	g.SetLimit(branchReadWorkers)
	for i, row := range work {
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			out[i] = r.place(ctx, row)
			return nil
		})
	}
	// A row skipped because the deadline passed would read as placed.
	waitErr := g.Wait()
	if err := r.answers.write(append(slices.Clone(bases), changeHeads(changes)...)...); err != nil {
		slog.WarnContext(ctx, "missing-commit answers not stored", attr.Component("magus"), attr.Error(err))
	}
	if waitErr != nil {
		return nil, "", fmt.Errorf("the finished rows: %w", waitErr)
	}
	if err := recordPlacedMain(root, out); err != nil {
		slog.WarnContext(ctx, "heads on a base not stored, so retention keeps their rows", attr.Component("magus"), attr.Error(err))
	}
	return out, joinNotes(note, changeNote), nil
}

// usableChanges is the open changes a row may be placed on, and why the list
// answers for fewer. A row no list places reads pushed or local and counts
// alone: an over-report, never an under-report.
func usableChanges(ctx context.Context, driver types.VCSDriver, root string, open OpenChanges, now time.Time) ([]qtypes.Change, string) {
	if open.Fetch == nil {
		return nil, fmt.Sprintf("no change list was read, so no row is placed on an open change; run `%s`",
			queueLs("<provider>", "<base>"))
	}
	if age := now.Sub(time.Unix(open.Fetch.At, 0)); age > ChangeListMaxAge {
		return nil, fmt.Sprintf("the change list is %s old, past %s, so no row is placed on an open change; run `%s`",
			age.Round(time.Minute), ChangeListMaxAge, queueLs(open.Fetch.Provider, open.Fetch.Base))
	}
	all := slices.Clone(open.Changes.Changes)
	for _, u := range open.Changes.Unqueued {
		all = append(all, qtypes.Change{ID: u.ID, Head: u.Head, Branch: u.Branch, Base: u.Base, Title: u.Title})
	}
	var usable []qtypes.Change
	var unfetched []string
	for _, c := range all {
		if c.Head == "" {
			continue
		}
		if _, err := driver.FindCommit(ctx, root, c.Head); err != nil {
			unfetched = append(unfetched, fmt.Sprintf("#%s at %s", c.ID, c.Head[:min(len(c.Head), 12)]))
			continue
		}
		usable = append(usable, c)
	}
	if len(unfetched) > 0 {
		return usable, fmt.Sprintf("%d open change head(s) are not in this repository, so they hold no row: %s; run `git fetch`",
			len(unfetched), strings.Join(unfetched, ", "))
	}
	return usable, ""
}

// queueLs is the typed command that reads the change list. internal/hint
// declares no path for it, and that package is not this one's to edit.
func queueLs(provider, base string) string {
	return fmt.Sprintf("%s queue ls --provider %s --base %s", hint.BinaryName(), provider, base)
}

func changeHeads(changes []qtypes.Change) []string {
	heads := make([]string, len(changes))
	for i, c := range changes {
		heads[i] = c.Head
	}
	return heads
}

func joinNotes(notes ...string) string {
	return strings.Join(slices.DeleteFunc(notes, func(s string) bool { return s == "" }), "; ")
}

// placeRead is one [placeWith] pass. Rows place concurrently; mu guards the
// answers cache, the one thing they write.
type placeRead struct {
	driver    types.VCSDriver
	root      string
	bases     []string
	tips      map[string]string
	upstreams map[string]string
	changes   []qtypes.Change
	answers   *missingCache
	mu        *sync.Mutex
}

func (r placeRead) place(ctx context.Context, row job.Job) Placed {
	p := Placed{Job: row.ID, Head: row.Head}
	if row.Head == "" {
		why := row.HeadUnread
		if why == "" {
			why = "the row was stored before rows recorded a head"
		}
		return r.unread(p, "no head is recorded: "+why)
	}
	if _, err := r.driver.FindCommit(ctx, r.root, row.Head); err != nil {
		return r.unread(p, fmt.Sprintf("its head %s is not in this repository: %v", p.Name(), err))
	}
	p.Branch = r.branchAt(row)
	missing, err := r.lacking(ctx, row.Head)
	if err != nil {
		return r.unread(p, fmt.Sprintf("whether a base holds %s: %v", p.Name(), err))
	}
	if missing == 0 || r.upstreamLanded(ctx, p.Branch, row.Head) {
		p.Place = PlaceMain
		return p
	}
	p.Commits = missing
	for _, c := range r.changes {
		if held, err := r.holds(ctx, c.Head, row.Head); err == nil && held {
			p.Place, p.Change = PlaceProposed, c.ID
			return p
		}
	}
	p.Place = PlaceLocal
	if unpublished, err := r.driver.UnpublishedRevisions(ctx, r.root, row.Head); err == nil && len(unpublished) == 0 {
		p.Place = PlacePushed
	}
	return p
}

// unread is p as PlaceUnread for why. Only a person sets work aside, so that
// is the fix when no read can answer.
func (r placeRead) unread(p Placed, why string) Placed {
	p.Place, p.Why = PlaceUnread, why
	p.Fix = fmt.Sprintf("restore the commit, or have a person set it aside: `%s` with {\"api_version\":%q,\"id\":%q,\"left_unmerged\":\"<why>\"}",
		hint.JobApply.With("--stdin"), job.APIVersion, p.Job)
	return p
}

// branchAt is the local branch standing at row's head: the one named for the
// row first, else the first by name.
func (r placeRead) branchAt(row job.Job) string {
	if r.tips[row.ID] == row.Head {
		return row.ID
	}
	var names []string
	for name, tip := range r.tips {
		if tip == row.Head {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// lacking is the fewest of head's commits a base lacks, 0 when one holds them.
func (r placeRead) lacking(ctx context.Context, head string) (int, error) {
	fewest := -1
	for _, base := range r.bases {
		missing, err := r.missing(ctx, base, head)
		if err != nil {
			return 0, err
		}
		if fewest < 0 || missing < fewest {
			fewest = missing
		}
		if missing == 0 {
			break
		}
	}
	return max(fewest, 0), nil
}

func (r placeRead) holds(ctx context.Context, at, head string) (bool, error) {
	missing, err := r.missing(ctx, at, head)
	return missing == 0, err
}

func (r placeRead) missing(ctx context.Context, at, head string) (int, error) {
	r.mu.Lock()
	missing, known := r.answers.get(at, head)
	r.mu.Unlock()
	if known {
		return missing, nil
	}
	_, missing, err := reached(ctx, r.driver, r.root, at, head)
	if err != nil {
		return 0, err
	}
	r.mu.Lock()
	r.answers.put(at, head, missing)
	r.mu.Unlock()
	return missing, nil
}

// upstreamLanded reports a branch at head behind its own pushed copy, which a
// base holds: the forge merged what was pushed after the row ended.
func (r placeRead) upstreamLanded(ctx context.Context, branch, head string) bool {
	pushed, tracked := r.upstreams[branch]
	if branch == "" || !tracked || pushed == head {
		return false
	}
	if behind, err := r.driver.IsAncestor(ctx, r.root, head, pushed); err != nil || !behind {
		return false
	}
	missing, err := r.lacking(ctx, pushed)
	return err == nil && missing == 0
}
