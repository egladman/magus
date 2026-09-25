// cross-cutting: this file holds the test doubles every step's tests share.

package mergequeue

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/egladman/magus/libs/mergequeue/types"
	"github.com/egladman/magus/libs/mergequeue/types/gen/mocks"
	magustypes "github.com/egladman/magus/types"
	magusmocks "github.com/egladman/magus/types/gen/mocks"
)

// base is the plan's base commit.
var base = strings.Repeat("b", 40)

// clone is the clone every step under test works in.
var clone = Clone{Root: "/clone", Remote: "origin"}

// head is a commit id for change id.
func head(id string) string { return fmt.Sprintf("%040s", hex.EncodeToString([]byte(id))) }

// oid is a commit id derived from parts, for commits a step makes.
func oid(parts ...string) string {
	sum := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// candidateOf is the candidate [doubles.builds] makes of head onto onto.
func candidateOf(onto, head string) string { return oid("candidate", onto, head) }

// change builds a change on main affecting units.
func change(id string, units ...string) types.Change {
	return types.Change{ID: id, Head: head(id), Base: "main", Method: types.MethodSquash, Affected: units}
}

// stacked is change id stacked on below, whose head is its stack base.
func stacked(id string, below types.Change, units ...string) types.Change {
	c := change(id, units...)
	c.Below, c.Parent, c.StackBase = below.ID, below.ID, below.Head
	return c
}

func waiting(id string) types.Verdict {
	return types.Verdict{Change: change(id), Decision: types.DecisionWait, Code: types.CodeWaitBehind, Reason: "r"}
}

func green(id string) types.Verdict {
	return types.Verdict{BaseCommit: base, Change: change(id, "a"), Decision: types.DecisionMerge, Onto: base, CandidateCommit: head("c" + id),
		Method: types.MethodSquash, Depth: 1}
}

// ids names the changes of each partition.
func ids(groups [][]types.Change) [][]string {
	out := make([][]string, len(groups))
	for i, g := range groups {
		for _, c := range g {
			out[i] = append(out[i], c.ID)
		}
	}
	return out
}

// idsOf names the changes vs decide, in order.
func idsOf(vs []types.Verdict) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Change.ID)
	}
	return out
}

// draw reads a fuzz input as a sequence of small choices; past its end every choice is
// the first, so every input decodes and a short one is a small case.
type draw struct {
	data []byte
	i    int
}

// n is a choice in [0, k).
func (d *draw) n(k int) int {
	if k <= 1 || d.i >= len(d.data) {
		return 0
	}
	v := int(d.data[d.i]) % k
	d.i++
	return v
}

// doubles are the mocks one step under test talks to.
type doubles struct {
	vcs      *magusmocks.MockVCSDriver
	provider *mocks.MockProvider
	facts    *mocks.MockBuildFacts
	gate     *mocks.MockGate
	src      *mocks.MockVerdictSource
}

func newDoubles(t *testing.T) doubles {
	return doubles{
		vcs:      magusmocks.NewMockVCSDriver(t),
		provider: mocks.NewMockProvider(t),
		facts:    mocks.NewMockBuildFacts(t),
		gate:     mocks.NewMockGate(t),
		src:      mocks.NewMockVerdictSource(t),
	}
}

// tip answers a fetch of the base branch with commit.
func (d doubles) tip(commit string) {
	d.vcs.EXPECT().FetchRef(mock.Anything, clone.Root, clone.Remote, "refs/heads/main").Return(commit, nil)
}

// bot is the committer the provider names, and author wrote every change's head.
var (
	bot    = magustypes.Person{Name: "bot", Email: "bot@example.com"}
	author = magustypes.Person{Name: "author", Email: "author@example.com"}
)

// Planning asks Describe for the base alone; an Applier also asks for the setup of its
// status, on the default credential.
var (
	planQuery  = types.ListQuery{Base: "main"}
	applyQuery = types.ListQuery{Base: "main", StatusContext: DefaultStatusContext}
)

// caps describes a provider merging one change per call with methods, committing as bot,
// to a planner or an applier.
func (d doubles) caps(methods ...types.MergeMethod) {
	if len(methods) == 0 {
		methods = []types.MergeMethod{types.MethodSquash}
	}
	d.provider.EXPECT().Describe(mock.Anything, mock.MatchedBy(func(q types.ListQuery) bool { return q == planQuery || q == applyQuery })).
		Return(types.Capabilities{StackMerge: types.StackMergeSequential, Methods: methods, Committer: bot}, nil)
}

// plain says commit has one parent, so a review of it covers it, and author wrote it.
func (d doubles) plain(commit string) {
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, commit).Return(magustypes.Commit{ID: commit, Parents: []string{base}, Author: author}, nil)
}

// noCheckouts is the cleanup a validator or an applier runs, finding nothing left.
func (d doubles) noCheckouts() {
	d.vcs.EXPECT().Checkouts(mock.Anything, clone.Root).Return(nil, nil)
}

// building is how the version control answers the calls building a candidate makes, for
// any change onto any commit: the candidate of head onto onto is candidateOf(onto, head),
// and it touches touched.
type building struct {
	touched   []string
	conflicts map[string][]magustypes.Conflict // by the head merged
	fail      map[string]error                 // starting the merge, by the head merged
}

// makeCheckout creates the checkout's directory, as the version control would; the
// queue writes into it through an os.Root.
func makeCheckout(_ context.Context, _, dir, _ string) error { return os.MkdirAll(dir, 0o755) }

// builds answers what b says, and records each checkout's commit and head.
func (d doubles) builds(b building) *checkouts {
	co := &checkouts{onto: map[string]string{}, head: map[string]string{}}
	d.vcs.EXPECT().CreateCheckout(mock.Anything, clone.Root, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, _, dir, rev string) error {
			co.mu.Lock()
			defer co.mu.Unlock()
			co.onto[dir] = rev
			return makeCheckout(ctx, "", dir, rev)
		}).Maybe()
	d.vcs.EXPECT().StartMerge(mock.Anything, mock.Anything, mock.Anything, candidateIdentity).
		RunAndReturn(func(_ context.Context, dir, rev string, _ magustypes.Person) error {
			co.mu.Lock()
			defer co.mu.Unlock()
			co.head[dir] = rev
			return b.fail[rev]
		}).Maybe()
	d.vcs.EXPECT().Conflicts(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, dir string) ([]magustypes.Conflict, error) {
			co.mu.Lock()
			defer co.mu.Unlock()
			return b.conflicts[co.head[dir]], nil
		}).Maybe()
	d.vcs.EXPECT().AbortMerge(mock.Anything, mock.Anything).Return(nil).Maybe()
	d.vcs.EXPECT().Commit(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, dir string, _ magustypes.CheckoutCommit) (string, error) {
			co.mu.Lock()
			defer co.mu.Unlock()
			return candidateOf(co.onto[dir], co.head[dir]), nil
		}).Maybe()
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, mock.Anything, mock.Anything).Return(b.touched, nil).Maybe()
	d.vcs.EXPECT().RemoveCheckout(mock.Anything, clone.Root, mock.Anything).
		RunAndReturn(func(_ context.Context, _, dir string) error {
			co.mu.Lock()
			defer co.mu.Unlock()
			co.removed = append(co.removed, dir)
			return nil
		}).Maybe()
	return co
}

// trail records, in order, the marks a step shows as "<id> <mark>" ("none" for
// [types.MarkNone]), plus " in <repo>" for a change naming its repository, beside
// whatever else a test adds to it.
type trail struct {
	mu  sync.Mutex
	got []string
}

func (tr *trail) add(entry string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.got = append(tr.got, entry)
}

func (tr *trail) entries() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return slices.Clone(tr.got)
}

// marks records every mark the provider is asked to show, failing the entries fail
// names. Set before [applierFor], it answers every mark ahead of that catch-all.
func (d doubles) marks(fail map[string]error) *trail {
	tr := &trail{}
	d.provider.EXPECT().Mark(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, c types.Change, m types.Mark) error {
			entry := c.ID + " " + string(m)
			if m == types.MarkNone {
				entry = c.ID + " none"
			}
			if c.Repo != "" {
				entry += " in " + c.Repo
			}
			tr.add(entry)
			return fail[entry]
		}).Maybe()
	return tr
}

// flags records every flag the provider is asked to show as "<id> <flag> on" or "<id>
// <flag> off". Set before [applierFor], it answers every flag ahead of that catch-all.
func (d doubles) flags() *trail {
	tr := &trail{}
	d.provider.EXPECT().Flag(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, c types.Change, f types.Flag, on bool) error {
			state := "off"
			if on {
				state = "on"
			}
			tr.add(c.ID + " " + string(f) + " " + state)
			return nil
		}).Maybe()
	return tr
}

// checkouts are the checkouts building candidates made.
type checkouts struct {
	mu      sync.Mutex
	onto    map[string]string // dir -> the commit checked out
	head    map[string]string // dir -> the head merged into it
	removed []string
}
