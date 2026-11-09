package work

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// readBudget is what one survey spends on the version control. A stop
// hook has seven seconds, and an item past this waits for the next one.
const readBudget = 3 * time.Second

// itemBudget stops one wide item spending a whole survey.
const itemBudget = time.Second

// HeldReporter is the optional capability to list the stashes and
// preserved captures a repository holds. A backend without it leaves both
// kinds unsurveyed, and the survey says so in [Report.Unknown].
type HeldReporter interface {
	HeldRevisions(ctx context.Context, dir string) ([]types.HeldRevision, error)
}

// Ledger is the work ledger of one repository: an append-only JSONL file
// whose last line per key is the item. Nothing is ever deleted from it; an
// item that no longer exists gets a Dropped line. It computes and records,
// and never touches the work itself.
type Ledger struct {
	repo  string
	items file.JSONL[types.WorkItem]
	facts file.JSONL[fact]
	now   func() time.Time
}

// Open is the ledger of the repository whose primary checkout is repo,
// under $XDG_STATE_HOME/magus/work. It reads nothing until asked.
func Open(repo string) (*Ledger, error) {
	base, err := xdg.StateDir()
	if err != nil {
		return nil, fmt.Errorf("work ledger: %w", err)
	}
	dir, err := vcs.StateDir(base, "work", repo)
	if err != nil {
		return nil, fmt.Errorf("work ledger: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("work ledger: %w", err)
	}
	return &Ledger{
		repo: repo,
		items: file.JSONL[types.WorkItem]{
			Path: filepath.Join(dir, "work.jsonl"),
			Key:  types.WorkItem.Key,
			Live: func(w types.WorkItem) bool { return !w.Dropped },
		},
		facts: file.JSONL[fact]{
			Path: filepath.Join(dir, "facts.jsonl"),
			Key:  fact.key,
		},
		now: time.Now,
	}, nil
}

// Items is what the ledger says now, without asking the version control.
func (l *Ledger) Items() ([]types.WorkItem, error) {
	return l.items.Load()
}

// End records that a person ended the item at key, for reason. It holds
// while the item's head and uncommitted count stay what they were.
func (l *Ledger) End(ctx context.Context, key, reason string) error {
	if reason == "" {
		return fmt.Errorf("work ledger: ending %s needs a reason", key)
	}
	found := false
	err := l.items.Update(ctx, func(items []types.WorkItem) []types.WorkItem {
		i := slices.IndexFunc(items, func(w types.WorkItem) bool { return w.Key() == key })
		if i < 0 {
			return nil
		}
		found = true
		ended := items[i]
		ended.State, ended.Reason, ended.Why = types.WorkEnded, reason, "ended by a person"
		ended.At = l.now().Unix()
		return []types.WorkItem{ended}
	})
	if err != nil {
		return fmt.Errorf("work ledger: %w", err)
	}
	if !found {
		return fmt.Errorf("work ledger: no item %s in %s", key, l.items.Path)
	}
	return nil
}

// Survey is what a survey judges items against.
type Survey struct {
	// Base is the revision an item is absorbed into. Required.
	Base string
	// Jobs are the rows that can track an item; a terminal row tracks none.
	Jobs []types.Job
	// LocalOnly is work.local_only. A bad glob fails the survey.
	LocalOnly []string
}

// Report is the ledger after a survey, and what the survey left unread.
type Report struct {
	Items []types.WorkItem
	// Unknown names what the survey could not read and why.
	Unknown []string
}

// Survey reads every registered checkout (gone ones too), every branch
// head holding unpublished revisions, and held revisions when driver
// reports them. It appends a line for each item whose state moved, and a
// Dropped line for each that no longer exists. A second survey over an
// unchanged repository appends nothing.
func (l *Ledger) Survey(ctx context.Context, driver types.VCSDriver, in Survey) (Report, error) {
	if in.Base == "" {
		return Report{}, errors.New("work survey: no base revision to judge absorbed against")
	}
	for _, glob := range in.LocalOnly {
		if _, err := path.Match(glob, ""); err != nil {
			return Report{}, fmt.Errorf("work survey: work.local_only %q: %w", glob, err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, readBudget)
	defer cancel()
	base, err := driver.FindCommit(ctx, l.repo, in.Base)
	if err != nil {
		return Report{}, fmt.Errorf("work survey: base %s: %w", in.Base, err)
	}
	baseTree, err := driver.TreeID(ctx, l.repo, base.ID)
	if err != nil {
		return Report{}, fmt.Errorf("work survey: base %s: %w", in.Base, err)
	}
	checkouts, err := driver.RegisteredCheckouts(ctx, l.repo)
	if err != nil {
		return Report{}, fmt.Errorf("work survey: %w", err)
	}
	stored, err := l.items.Load()
	if err != nil {
		return Report{}, fmt.Errorf("work survey: %w", err)
	}
	known, err := l.facts.Load()
	if err != nil {
		return Report{}, fmt.Errorf("work survey: %w", err)
	}
	r := &survey{
		ledger: l, driver: driver, in: in, base: base.ID, baseTree: baseTree,
		stored: indexItems(stored), facts: map[string]fact{}, kept: map[string]bool{},
		unread: map[string]string{}, trackAt: map[string]string{}, now: l.now().Unix(),
	}
	for _, f := range known {
		r.facts[f.key()] = f
	}
	r.readCheckouts(ctx, checkouts)
	r.readBranches(ctx)
	r.readHeld(ctx)
	if err := r.write(ctx); err != nil {
		return Report{}, err
	}
	items, err := l.items.Load()
	if err != nil {
		return Report{}, fmt.Errorf("work survey: %w", err)
	}
	return Report{Items: items, Unknown: r.unknown()}, nil
}

// fact is what the version control said of one head against one base.
type fact struct {
	Head     string `json:"head"`
	Base     string `json:"base"`
	Commits  int    `json:"commits"`
	Absorbed bool   `json:"absorbed,omitempty"`
	At       int64  `json:"at"`
}

func (f fact) key() string { return f.Head + ".." + f.Base }

type survey struct {
	ledger         *Ledger
	driver         types.VCSDriver
	in             Survey
	base, baseTree string
	stored         map[string]types.WorkItem
	facts          map[string]fact
	asked          []fact
	found          []types.WorkItem
	// kept are stored keys a cut read leaves as they were.
	kept map[string]bool
	// unread is why a checkout with no stored line went unread.
	unread  map[string]string
	trackAt map[string]string
	cut     int
	notes   []string
	now     int64
}

func indexItems(items []types.WorkItem) map[string]types.WorkItem {
	out := make(map[string]types.WorkItem, len(items))
	for _, w := range items {
		out[w.Key()] = w
	}
	return out
}

func (r *survey) readCheckouts(ctx context.Context, checkouts []types.RegisteredCheckout) {
	for _, c := range checkouts {
		if why := r.jobFor(c.Root); why != "" && c.Head != "" {
			r.trackAt[c.Head] = why
		}
		item := types.WorkItem{Kind: types.WorkCheckout, Name: c.Root, Head: c.Head}
		if r.readItem(ctx, &item) {
			r.found = append(r.found, item)
		}
	}
}

// readItem fills item's facts within [itemBudget]. It reports false when
// the read was cut, which leaves any stored line for the item as it was.
func (r *survey) readItem(ctx context.Context, item *types.WorkItem) bool {
	ctx, cancel := context.WithTimeout(ctx, itemBudget)
	defer cancel()
	if item.Kind == types.WorkCheckout {
		dirty, gone, err := r.dirty(ctx, item.Name)
		if err != nil {
			return r.cutItem(*item, err)
		}
		item.Dirty, item.Gone = dirty, gone
	}
	if item.Head == "" {
		return true
	}
	f, err := r.fact(ctx, item.Head)
	if err != nil {
		return r.cutItem(*item, err)
	}
	item.Base, item.Commits = r.base, f.Commits
	return true
}

// cutItem keeps a stored line as it was. A checkout with none is still
// listed, as unaccounted: an unread checkout is not a clear one.
func (r *survey) cutItem(item types.WorkItem, err error) bool {
	r.cut++
	if _, ok := r.stored[item.Key()]; ok {
		r.kept[item.Key()] = true
		return false
	}
	if item.Kind == types.WorkCheckout {
		r.unread[item.Key()] = fmt.Sprintf("not read within %s: %v", itemBudget, err)
		r.found = append(r.found, item)
	}
	return false
}

func (r *survey) dirty(ctx context.Context, root string) (int, bool, error) {
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return 0, true, nil
	}
	files, err := r.driver.DirtyFiles(ctx, root, nil)
	if err != nil {
		return 0, false, err
	}
	return len(files), false, nil
}

// fact is head against the base, from the stored facts when a survey has
// already asked about this pair.
func (r *survey) fact(ctx context.Context, head string) (fact, error) {
	key := fact{Head: head, Base: r.base}.key()
	if f, ok := r.facts[key]; ok {
		return f, nil
	}
	revs, err := r.driver.UnpublishedRevisions(ctx, r.ledger.repo, head, r.base)
	if err != nil {
		return fact{}, err
	}
	f := fact{Head: head, Base: r.base, Commits: len(revs), At: r.now}
	if f.Commits > 0 {
		merge := types.TreeMerge{Ours: r.base, Theirs: head}
		merged, err := r.driver.MergeTrees(ctx, r.ledger.repo, merge)
		if err != nil {
			return fact{}, err
		}
		f.Absorbed = len(merged.Conflicts) == 0 && merged.Tree == r.baseTree
	}
	r.facts[key] = f
	r.asked = append(r.asked, f)
	return f, nil
}

func (r *survey) readBranches(ctx context.Context) {
	heads, err := r.driver.BranchHeads(ctx, r.ledger.repo)
	if err != nil {
		r.notes = append(r.notes, fmt.Sprintf("branches were not read: %v", err))
		r.keepKind(types.WorkBranch)
		return
	}
	names := make([]string, 0, len(heads))
	for name := range heads {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		item := types.WorkItem{Kind: types.WorkBranch, Name: name, Head: heads[name]}
		if r.readItem(ctx, &item) && item.Commits > 0 {
			r.found = append(r.found, item)
		}
	}
}

func (r *survey) readHeld(ctx context.Context) {
	held, ok := r.driver.(HeldReporter)
	if !ok {
		r.notes = append(r.notes, fmt.Sprintf("%s lists no stashes or preserved captures, so neither was surveyed", r.driver.Name()))
		r.keepKind(types.WorkStash, types.WorkPreserved)
		return
	}
	revs, err := held.HeldRevisions(ctx, r.ledger.repo)
	if err != nil {
		r.notes = append(r.notes, fmt.Sprintf("stashes and preserved captures were not read: %v", err))
		r.keepKind(types.WorkStash, types.WorkPreserved)
		return
	}
	for _, h := range revs {
		item := types.WorkItem{Kind: h.Kind, Name: h.Name, Head: h.Head}
		if r.readItem(ctx, &item) {
			r.found = append(r.found, item)
		}
	}
}

// keepKind leaves every stored item of kinds as it was: a kind nobody read
// is not a kind that is gone.
func (r *survey) keepKind(kinds ...types.WorkKind) {
	for key, w := range r.stored {
		if slices.Contains(kinds, w.Kind) {
			r.kept[key] = true
		}
	}
}

func (r *survey) jobFor(root string) string {
	for _, row := range r.in.Jobs {
		if row.State.Terminal() {
			continue
		}
		if row.CheckoutRoot == root || (row.Worktree != nil && row.Worktree.Root == root) {
			return fmt.Sprintf("job %s (%s)", row.ID, row.State)
		}
	}
	return ""
}

// decide gives item its state. The order of the cases is the precedence.
func (r *survey) decide(item types.WorkItem) types.WorkItem {
	held := item.Kind == types.WorkStash || item.Kind == types.WorkPreserved
	f := r.facts[fact{Head: item.Head, Base: r.base}.key()]
	tracked, glob := r.tracked(item), r.localOnly(item)
	switch {
	case r.unread[item.Key()] != "":
		item.State, item.Why = types.WorkUnaccounted, r.unread[item.Key()]
	case !held && item.Dirty == 0 && item.Commits == 0:
		item.State, item.Why = types.WorkClear, "no uncommitted file and no unpublished revision"
	case item.Dirty == 0 && f.Absorbed:
		item.State, item.Why = types.WorkAbsorbed, "merging it into "+r.in.Base+" leaves the tree as it is"
	case tracked != "":
		item.State, item.Why = types.WorkTracked, tracked
	case glob != "":
		item.State, item.Why = types.WorkLocalOnly, "matches work.local_only "+glob
	case r.ended(item):
		prior := r.stored[item.Key()]
		item.State, item.Why, item.Reason = types.WorkEnded, prior.Why, prior.Reason
	default:
		item.State, item.Why = types.WorkUnaccounted, unaccountedWhy(item)
	}
	return item
}

func unaccountedWhy(item types.WorkItem) string {
	switch {
	case item.Gone && item.Commits > 0:
		return fmt.Sprintf("its directory is gone and %d revision(s) exist only here", item.Commits)
	case item.Dirty > 0 && item.Commits > 0:
		return fmt.Sprintf("%d uncommitted file(s) and %d unpublished revision(s), and no job or glob holds them", item.Dirty, item.Commits)
	case item.Dirty > 0:
		return fmt.Sprintf("%d uncommitted file(s), and no job or glob holds them", item.Dirty)
	case item.Commits > 0:
		return fmt.Sprintf("%d unpublished revision(s), and no job or glob holds them", item.Commits)
	}
	return "it holds a revision no branch reaches, and no job or glob holds it"
}

func (r *survey) tracked(item types.WorkItem) string {
	switch item.Kind {
	case types.WorkCheckout:
		return r.jobFor(item.Name)
	case types.WorkBranch:
		return r.trackAt[item.Head]
	case types.WorkStash, types.WorkPreserved:
	}
	return ""
}

// localOnly is the glob item matches: a checkout by its directory name,
// anything else by its name. Survey has validated every glob.
func (r *survey) localOnly(item types.WorkItem) string {
	name := item.Name
	if item.Kind == types.WorkCheckout {
		name = filepath.Base(item.Name)
	}
	for _, glob := range r.in.LocalOnly {
		if ok, _ := path.Match(glob, name); ok {
			return glob
		}
	}
	return ""
}

func (r *survey) ended(item types.WorkItem) bool {
	prior, ok := r.stored[item.Key()]
	return ok && prior.State == types.WorkEnded && prior.Head == item.Head && prior.Dirty == item.Dirty
}

func (r *survey) unknown() []string {
	out := slices.Clone(r.notes)
	if r.cut > 0 {
		out = append(out, fmt.Sprintf("%d item(s) were not read within %s each or %s in all; the next survey reads them again", r.cut, itemBudget, readBudget))
	}
	return out
}

// write appends a line per item whose state moved, and a Dropped line per
// stored item the survey no longer found.
func (r *survey) write(ctx context.Context) error {
	if len(r.asked) > 0 {
		asked := r.asked
		err := r.ledger.facts.Update(ctx, func([]fact) []fact { return asked })
		if err != nil {
			return fmt.Errorf("work survey: %w", err)
		}
	}
	err := r.ledger.items.Update(ctx, func(current []types.WorkItem) []types.WorkItem {
		latest := indexItems(current)
		var add []types.WorkItem
		seen := map[string]bool{}
		for _, item := range r.found {
			item = r.decide(item)
			item.Repo = r.ledger.repo
			seen[item.Key()] = true
			if prior, ok := latest[item.Key()]; ok && same(prior, item) {
				continue
			}
			item.At = r.now
			add = append(add, item)
		}
		for _, prior := range current {
			if seen[prior.Key()] || r.kept[prior.Key()] {
				continue
			}
			prior.Dropped, prior.At = true, r.now
			add = append(add, prior)
		}
		return add
	})
	if err != nil {
		return fmt.Errorf("work survey: %w", err)
	}
	return nil
}

func same(a, b types.WorkItem) bool {
	a.At, b.At = 0, 0
	return a == b
}
