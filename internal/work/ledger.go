package work

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// readBudget is what one survey spends on the version control. A stop
// hook has seven seconds, and an item past this waits for the next one.
const readBudget = 3 * time.Second

// itemBudget stops one wide item spending a whole survey.
const itemBudget = time.Second

// heldReporter is the optional capability to list the stashes and
// preserved captures a repository holds. A backend without it leaves both
// kinds unsurveyed, and the survey says so in [report.Unknown].
type heldReporter interface {
	HeldRevisions(ctx context.Context, dir string) ([]heldRevision, error)
}

// ledger is the work ledger of one repository: an append-only JSONL file
// whose last line per key is the item. Nothing is ever deleted from it; an
// item that no longer exists gets a Dropped line. It computes and records,
// and never touches the work itself.
type ledger struct {
	repo  string
	items file.JSONL[workItem]
	facts file.JSONL[fact]
	now   func() time.Time
}

// open is the ledger of the repository whose primary checkout is repo,
// under $XDG_STATE_HOME/magus/work. It reads nothing until asked.
func open(repo string) (*ledger, error) {
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
	items, err := file.NewJSONL(codec.JSONLConfig[workItem]{
		Log:  slog.With(attr.Component("magus")),
		Path: filepath.Join(dir, "work.jsonl"),
		Perm: 0o644,
		Key:  workItem.Key,
		Live: func(w workItem) bool { return !w.Dropped },
	})
	if err != nil {
		return nil, fmt.Errorf("work ledger: %w", err)
	}
	facts, err := file.NewJSONL(codec.JSONLConfig[fact]{
		Log:  slog.With(attr.Component("magus")),
		Path: filepath.Join(dir, "facts.jsonl"),
		Perm: 0o644,
		Key:  fact.key,
	})
	if err != nil {
		return nil, fmt.Errorf("work ledger: %w", err)
	}
	return &ledger{repo: repo, items: items, facts: facts, now: time.Now}, nil
}

// Items is what the ledger says now, without asking the version control.
func (l *ledger) Items() ([]workItem, error) {
	return l.items.Load()
}

// End records that a person ended the item at key, for reason. It holds
// while the item's head and uncommitted count stay what they were.
func (l *ledger) End(ctx context.Context, key, reason string) error {
	if reason == "" {
		return fmt.Errorf("work ledger: ending %s needs a reason", key)
	}
	found := false
	err := l.items.Update(ctx, func(items []workItem) []workItem {
		i := slices.IndexFunc(items, func(w workItem) bool { return w.Key() == key })
		if i < 0 {
			return nil
		}
		found = true
		ended := items[i]
		ended.State, ended.Reason, ended.Why = stateEnded, reason, "ended by a person"
		ended.At = l.now().Unix()
		return []workItem{ended}
	})
	if err != nil {
		return fmt.Errorf("work ledger: %w", err)
	}
	if !found {
		return fmt.Errorf("work ledger: no item %s in %s", key, l.items.Path())
	}
	return nil
}

// surveyTerms is what a survey judges items against.
type surveyTerms struct {
	// Base is the revision an item is absorbed into. Required.
	Base string
	// Jobs are the rows that can track an item; a terminal row tracks none.
	Jobs []job.Job
	// LocalOnly is work.local_only. A bad glob fails the survey.
	LocalOnly []string
}

// report is the ledger after a survey, and what the survey left unread.
type report struct {
	Items []workItem
	// Unknown names what the survey could not read and why.
	Unknown []string
}

// Survey reads every registered checkout (gone ones too), every branch
// head holding unpublished revisions, and held revisions when driver
// reports them. It appends a line for each item whose state moved, and a
// Dropped line for each that no longer exists. A second survey over an
// unchanged repository appends nothing.
func (l *ledger) Survey(ctx context.Context, driver types.VCSDriver, in surveyTerms) (report, error) {
	if in.Base == "" {
		return report{}, errors.New("work survey: no base revision to judge absorbed against")
	}
	for _, glob := range in.LocalOnly {
		if _, err := path.Match(glob, ""); err != nil {
			return report{}, fmt.Errorf("work survey: work.local_only %q: %w", glob, err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, readBudget)
	defer cancel()
	base, err := driver.FindCommit(ctx, l.repo, in.Base)
	if err != nil {
		return report{}, fmt.Errorf("work survey: base %s: %w", in.Base, err)
	}
	baseTree, err := driver.TreeID(ctx, l.repo, base.ID)
	if err != nil {
		return report{}, fmt.Errorf("work survey: base %s: %w", in.Base, err)
	}
	checkouts, err := driver.RegisteredCheckouts(ctx, l.repo)
	if err != nil {
		return report{}, fmt.Errorf("work survey: %w", err)
	}
	stored, err := l.items.Load()
	if err != nil {
		return report{}, fmt.Errorf("work survey: %w", err)
	}
	known, err := l.facts.Load()
	if err != nil {
		return report{}, fmt.Errorf("work survey: %w", err)
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
		return report{}, err
	}
	items, err := l.items.Load()
	if err != nil {
		return report{}, fmt.Errorf("work survey: %w", err)
	}
	return report{Items: items, Unknown: r.unknown()}, nil
}

// fact is what the version control said of one head against one base.
type fact struct {
	Head     string `json:"head"`
	Base     string `json:"base"`
	Commits  int    `json:"commits"`
	Absorbed bool   `json:"absorbed,omitempty"`
	At       int64  `json:"at"`
}

func (f fact) key() string { return fmt.Sprintf("%s..%s", f.Head, f.Base) }

type survey struct {
	ledger         *ledger
	driver         types.VCSDriver
	in             surveyTerms
	base, baseTree string
	stored         map[string]workItem
	facts          map[string]fact
	asked          []fact
	found          []workItem
	// kept are stored keys a cut read leaves as they were.
	kept map[string]bool
	// unread is why a checkout with no stored line went unread.
	unread  map[string]string
	trackAt map[string]string
	cut     int
	notes   []string
	now     int64
}

func indexItems(items []workItem) map[string]workItem {
	out := make(map[string]workItem, len(items))
	for _, w := range items {
		out[w.Key()] = w
	}
	return out
}

func (r *survey) readCheckouts(ctx context.Context, checkouts []types.RegisteredCheckout) {
	for _, c := range checkouts {
		if why := r.holdingJob(c.Root); why != "" && c.Head != "" {
			r.trackAt[c.Head] = why
		}
		item := workItem{Kind: kindCheckout, Name: c.Root, Head: c.Head}
		if r.readItem(ctx, &item) {
			r.found = append(r.found, item)
		}
	}
}

// readItem fills item's facts within [itemBudget]. It reports false when
// the read was cut, which leaves any stored line for the item as it was.
func (r *survey) readItem(ctx context.Context, item *workItem) bool {
	ctx, cancel := context.WithTimeout(ctx, itemBudget)
	defer cancel()
	if item.Kind == kindCheckout {
		dirty, gone, err := r.dirty(ctx, item.Name)
		if err != nil {
			r.cutItem(*item, err)
			return false
		}
		item.Dirty, item.Gone = dirty, gone
	}
	if item.Head == "" {
		return true
	}
	f, err := r.fact(ctx, item.Head)
	if err != nil {
		r.cutItem(*item, err)
		return false
	}
	item.Base, item.Commits = r.base, f.Commits
	return true
}

// cutItem keeps a stored line as it was. A checkout with none is still
// listed, as unaccounted: an unread checkout is not a clear one.
func (r *survey) cutItem(item workItem, err error) {
	r.cut++
	if _, ok := r.stored[item.Key()]; ok {
		r.kept[item.Key()] = true
		return
	}
	if item.Kind == kindCheckout {
		r.unread[item.Key()] = fmt.Sprintf("not read within %s: %v", itemBudget, err)
		r.found = append(r.found, item)
	}
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
		r.keepKind(kindBranch)
		return
	}
	names := make([]string, 0, len(heads))
	for name := range heads {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		item := workItem{Kind: kindBranch, Name: name, Head: heads[name]}
		if r.readItem(ctx, &item) && item.Commits > 0 {
			r.found = append(r.found, item)
		}
	}
}

func (r *survey) readHeld(ctx context.Context) {
	held, ok := r.driver.(heldReporter)
	if !ok {
		r.notes = append(r.notes, fmt.Sprintf("%s lists no stashes or preserved captures, so neither was surveyed", r.driver.Name()))
		r.keepKind(kindStash, kindPreserved)
		return
	}
	revs, err := held.HeldRevisions(ctx, r.ledger.repo)
	if err != nil {
		r.notes = append(r.notes, fmt.Sprintf("stashes and preserved captures were not read: %v", err))
		r.keepKind(kindStash, kindPreserved)
		return
	}
	for _, h := range revs {
		item := workItem{Kind: h.Kind, Name: h.Name, Head: h.Head}
		if r.readItem(ctx, &item) {
			r.found = append(r.found, item)
		}
	}
}

// keepKind leaves every stored item of kinds as it was: a kind nobody read
// is not a kind that is gone.
func (r *survey) keepKind(kinds ...itemKind) {
	for key, w := range r.stored {
		if slices.Contains(kinds, w.Kind) {
			r.kept[key] = true
		}
	}
}

func (r *survey) holdingJob(root string) string {
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
func (r *survey) decide(item workItem) workItem {
	held := item.Kind == kindStash || item.Kind == kindPreserved
	f := r.facts[fact{Head: item.Head, Base: r.base}.key()]
	tracked, glob := r.tracked(item), r.localOnly(item)
	switch {
	case r.unread[item.Key()] != "":
		item.State, item.Why = stateUnaccounted, r.unread[item.Key()]
	case !held && item.Dirty == 0 && item.Commits == 0:
		item.State, item.Why = stateClear, "no uncommitted file and no unpublished revision"
	case item.Dirty == 0 && f.Absorbed:
		item.State, item.Why = stateAbsorbed, fmt.Sprintf("merging it into %s leaves the tree as it is", r.in.Base)
	case tracked != "":
		item.State, item.Why = stateTracked, tracked
	case glob != "":
		item.State, item.Why = stateLocalOnly, "matches work.local_only "+glob
	case r.ended(item):
		prior := r.stored[item.Key()]
		item.State, item.Why, item.Reason = stateEnded, prior.Why, prior.Reason
	default:
		item.State, item.Why = stateUnaccounted, unaccountedWhy(item)
	}
	return item
}

func unaccountedWhy(item workItem) string {
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

func (r *survey) tracked(item workItem) string {
	switch item.Kind {
	case kindCheckout:
		return r.holdingJob(item.Name)
	case kindBranch:
		return r.trackAt[item.Head]
	case kindStash, kindPreserved:
	}
	return ""
}

// localOnly is the glob item matches: a checkout by its directory name,
// anything else by its name. Survey has validated every glob.
func (r *survey) localOnly(item workItem) string {
	name := item.Name
	if item.Kind == kindCheckout {
		name = filepath.Base(item.Name)
	}
	for _, glob := range r.in.LocalOnly {
		if ok, _ := path.Match(glob, name); ok {
			return glob
		}
	}
	return ""
}

func (r *survey) ended(item workItem) bool {
	prior, ok := r.stored[item.Key()]
	return ok && prior.State == stateEnded && prior.Head == item.Head && prior.Dirty == item.Dirty
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
	err := r.ledger.items.Update(ctx, func(current []workItem) []workItem {
		latest := indexItems(current)
		var add []workItem
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

func same(a, b workItem) bool {
	a.At, b.At = 0, 0
	return a == b
}

// itemKind is what holds a piece of work a person could lose.
type itemKind string

const (
	// kindCheckout is a registered checkout: its files and its head.
	kindCheckout itemKind = "checkout"
	// kindBranch is a local branch holding unpublished revisions.
	kindBranch itemKind = "branch"
	// kindStash is a stash entry.
	kindStash itemKind = "stash"
	// kindPreserved is a capture Preserve anchored.
	kindPreserved itemKind = "preserved"
)

// itemState is what accounts for a piece of work, if anything does.
type itemState string

const (
	// stateUnaccounted is work nothing accounts for.
	stateUnaccounted itemState = "unaccounted"
	// stateTracked is work a live job row holds.
	stateTracked itemState = "tracked"
	// stateLocalOnly matches a work.local_only glob: never published.
	stateLocalOnly itemState = "local-only"
	// stateAbsorbed merges into the base without changing its tree.
	stateAbsorbed itemState = "absorbed"
	// stateEnded is work a person ended, with a reason, at its head.
	stateEnded itemState = "ended"
	// stateClear holds nothing to lose: no uncommitted file and no
	// unpublished revision.
	stateClear itemState = "clear"
)

// workItem is one line of the work ledger: a checkout, branch, stash or
// preserved capture of one repository, and what accounts for it. The last
// line per [workItem.Key] is the item.
type workItem struct {
	Kind itemKind
	// Name is the checkout root, branch, stash ref or preserved handle.
	Name string
	// Repo is the primary checkout's root, so a reader of every ledger
	// can tell whose an item is.
	Repo  string
	State itemState
	// Head is the revision the item holds; Base is what it was judged
	// absorbed against.
	Head, Base string
	// Dirty counts a checkout's uncommitted files.
	Dirty int
	// Commits counts revisions from Head that no remote or Base reaches.
	Commits int
	// Gone marks a registered checkout whose directory is gone.
	Gone bool
	// Why is what decided State: a job, a glob, an unanswered read.
	Why string
	// Reason is what the person said when they ended the item.
	Reason string
	// At is unix seconds when this line was written.
	At int64
	// Dropped ends the key: the item is gone.
	Dropped bool
}

// Key is "<kind>:<name>".
func (w workItem) Key() string {
	return fmt.Sprintf("%s:%s", w.Kind, w.Name)
}

type workItemWire struct {
	Kind    itemKind  `json:"kind"`
	Name    string    `json:"name"`
	Repo    string    `json:"repo"`
	State   itemState `json:"state"`
	Head    string    `json:"head,omitempty"`
	Base    string    `json:"base,omitempty"`
	Dirty   int       `json:"dirty,omitempty"`
	Commits int       `json:"commits,omitempty"`
	Gone    bool      `json:"gone,omitempty"`
	Why     string    `json:"why,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	At      int64     `json:"at"`
	Dropped bool      `json:"dropped,omitempty"`
}

// MarshalJSON refuses an unknown kind or state, so the ledger never
// holds a line its reader would refuse.
func (w workItem) MarshalJSON() ([]byte, error) {
	if err := w.valid(); err != nil {
		return nil, err
	}
	return codec.Marshal(workItemWire(w))
}

// UnmarshalJSON refuses an unknown kind or state rather than guess one.
func (w *workItem) UnmarshalJSON(b []byte) error {
	var wire workItemWire
	if err := codec.Unmarshal(b, &wire); err != nil {
		return fmt.Errorf("decode work item: %w", err)
	}
	item := workItem(wire)
	if err := item.valid(); err != nil {
		return err
	}
	*w = item
	return nil
}

func (w workItem) valid() error {
	kinds := []itemKind{kindCheckout, kindBranch, kindStash, kindPreserved}
	if !slices.Contains(kinds, w.Kind) {
		return fmt.Errorf("work item %q: kind %q is not one of %v", w.Name, w.Kind, kinds)
	}
	states := []itemState{stateUnaccounted, stateTracked, stateLocalOnly, stateAbsorbed, stateEnded, stateClear}
	if !slices.Contains(states, w.State) {
		return fmt.Errorf("work item %s: state %q is not one of %v", w.Key(), w.State, states)
	}
	return nil
}

// heldRevision is a revision a repository holds apart from its branches.
type heldRevision struct {
	// Kind is kindStash or kindPreserved.
	Kind itemKind
	// Name is the backend's own name: a stash ref, a preserved handle.
	Name string
	Head string
}
