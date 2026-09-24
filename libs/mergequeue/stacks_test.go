package mergequeue

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue/types"
)

// set is the commits named.
func set(ids ...string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func TestStackDetection(t *testing.T) {
	parent, child := change("1", "a"), change("2", "a")
	for name, tc := range map[string]struct {
		in        stackInput
		wantBelow string
		wantBase  string
		wantCode  types.Code
		wantWhy   string
	}{
		"a change carrying another's head is stacked on it": {
			in:        stackInput{changes: []types.Change{parent, child}, own: []map[string]bool{set(parent.Head), set(parent.Head, child.Head)}, tops: []string{parent.Head, child.Head}},
			wantBelow: "1", wantBase: parent.Head,
		},
		// A merge of the base into the change beneath, after this one was built on it, is
		// peeled off: this one carries its top.
		"a change carrying another's top is stacked on it": {
			in:        stackInput{changes: []types.Change{parent, child}, own: []map[string]bool{set(parent.Head, "top1"), set("top1", child.Head)}, tops: []string{"top1", child.Head}},
			wantBelow: "1", wantBase: "top1",
		},
		// A change merged through an update commit is reported at that commit, which the
		// change stacked on it does not carry.
		"a change on a merged one is stacked on the newest commit of it it carries": {
			in: stackInput{changes: []types.Change{child}, own: []map[string]bool{set("m.2", "m.1", child.Head)}, tops: []string{child.Head},
				merged:    []types.MergedChange{{ID: "9", Head: "m.update", Method: types.MethodSquash}},
				mergedOwn: [][]string{{"m.update", "m.2", "m.1"}}},
			wantBase: "m.2",
		},
		"a change carrying an unqueued change's head waits": {
			in: stackInput{changes: []types.Change{child}, own: []map[string]bool{set("u", child.Head)}, tops: []string{child.Head},
				unqueued: []types.UnqueuedChange{{ID: "7", Head: "u"}}},
			wantCode: types.CodeWaitUnqueuedBelow, wantWhy: "carries the commits of #7, which is open but not queued",
		},
		// It was built on #7 before a merge of the base went on top of #7.
		"a change carrying an unqueued change's top waits": {
			in: stackInput{changes: []types.Change{child}, own: []map[string]bool{set("u.top", child.Head)}, tops: []string{child.Head},
				unqueued: []types.UnqueuedChange{{ID: "7", Head: "u.merge"}}, unqueuedTops: []string{"u.top"}},
			wantCode: types.CodeWaitUnqueuedBelow, wantWhy: "carries the commits of #7, which is open but not queued",
		},
		"a declared parent that is not queued waits": {
			in:       stackInput{changes: []types.Change{withParent(child, "8")}, own: []map[string]bool{set(child.Head)}, tops: []string{child.Head}},
			wantCode: types.CodeWaitBelow, wantWhy: "stacked on #8, which is not queued",
		},
		"a declared parent it is not built on waits for a restack": {
			in:       stackInput{changes: []types.Change{parent, withParent(child, "1")}, own: []map[string]bool{set(parent.Head), set(child.Head)}, tops: []string{parent.Head, child.Head}},
			wantCode: types.CodeWaitRestack, wantWhy: "not built on #1's head; restack it onto that head",
		},
		"a stack that mixes merge methods is refused": {
			in:       stackInput{changes: []types.Change{parent, withMethod(child, types.MethodMerge)}, own: []map[string]bool{set(parent.Head), set(parent.Head, child.Head)}, tops: []string{parent.Head, child.Head}},
			wantCode: types.CodeKickRefused, wantWhy: "the stack mixes squash (#1) and merge (#2), and a stack merges with one merge method",
		},
		"a stacked change cannot rebase": {
			in:       stackInput{changes: []types.Change{withMethod(parent, types.MethodRebase), withMethod(child, types.MethodRebase)}, own: []map[string]bool{set(parent.Head), set(parent.Head, child.Head)}, tops: []string{parent.Head, child.Head}},
			wantCode: types.CodeKickRefused, wantWhy: "a stacked change cannot merge with the rebase method yet",
		},
		"a change built on two changes stacked on neither is refused": {
			in: stackInput{changes: []types.Change{parent, change("3"), child}, own: []map[string]bool{set(parent.Head), set(head("3")), set(parent.Head, head("3"), child.Head)},
				tops: []string{parent.Head, head("3"), child.Head}},
			wantCode: types.CodeKickRefused, wantWhy: "built on #1 and #3, which are not stacked on each other",
		},
	} {
		t.Run(name, func(t *testing.T) {
			i := len(tc.in.changes) - 1
			v := tc.in.stack(i)
			if tc.wantCode == "" {
				require.Nil(t, v)
				assert.Equal(t, tc.wantBelow, tc.in.changes[i].Below)
				assert.Equal(t, tc.wantBase, tc.in.changes[i].StackBase)
				return
			}
			require.NotNil(t, v)
			assert.Equal(t, tc.wantCode, v.Code)
			assert.Contains(t, v.Reason, tc.wantWhy)
			require.NoError(t, v.Check())
		})
	}
}

func withParent(c types.Change, parent string) types.Change { c.Parent = parent; return c }

func withMethod(c types.Change, m types.MergeMethod) types.Change { c.Method = m; return c }

func TestAStackDeeperThanTheQueueMergesIsRefused(t *testing.T) {
	var in stackInput
	carried := set()
	for i := range maxStackDepth + 2 {
		c := change(fmt.Sprint(i))
		carried[c.Head] = true
		in.changes = append(in.changes, c)
		in.own = append(in.own, maps.Clone(carried))
		in.tops = append(in.tops, c.Head)
	}
	v := in.stack(len(in.changes) - 1)
	require.NotNil(t, v)
	assert.Equal(t, "stacked on 17 unmerged changes, and the queue merges stacks up to 16 deep", v.Reason)
}

// Two changes at one top each read as stacked on the other, and neither can merge first.
func TestTwoChangesStackedOnEachOtherAreRefused(t *testing.T) {
	a, b := change("1"), change("2")
	a.Below, b.Below = "2", "1"
	verdicts := make([]*types.Verdict, 3)
	refuseCycles([]types.Change{a, b, change("3")}, verdicts)
	assert.Nil(t, verdicts[2])
	require.NotNil(t, verdicts[0])
	require.NotNil(t, verdicts[1])
	assert.Equal(t, "stacked in a cycle through #2", verdicts[0].Reason)
	assert.Equal(t, types.CodeKickRefused, verdicts[1].Code)
}

// What waits on a change planning settled is never blamed for it, and what is stacked on
// a change already on the base is stacked on the base.
func TestArrangeHoldsWhatIsStackedOnASettledChangeWithoutBlame(t *testing.T) {
	kicked, waits, merged := change("1", "a"), change("2", "b"), change("3", "c")
	onKicked := stacked("4", kicked, "a")
	onOnKicked := stacked("5", onKicked, "a")
	onWaits, onMerged := stacked("6", waits, "b"), stacked("7", merged, "c")
	changes := []types.Change{onOnKicked, kicked, waits, merged, onKicked, onWaits, onMerged}
	verdicts := []*types.Verdict{nil, refused(kicked, "a change from a fork"),
		decided(waits, types.DecisionWait, types.CodeWaitNotApproved, "not approved", ""),
		decided(merged, types.DecisionMerged, "", "its head is already on main", ""), nil, nil, nil}

	settled, partitions := arrange(changes, verdicts)
	codes := map[string]types.Code{}
	for _, v := range settled {
		codes[v.Change.ID] = v.Code
		require.NoError(t, v.Check())
	}
	assert.Equal(t, map[string]types.Code{"1": types.CodeKickRefused, "2": types.CodeWaitNotApproved, "3": "",
		"4": types.CodeWaitBelowKicked, "5": types.CodeWaitBelowKicked, "6": types.CodeWaitBelow}, codes)
	assert.Equal(t, [][]string{{"7"}}, ids(partitions))
	assert.Empty(t, partitions[0][0].Below, "stacked on the base once the change beneath is on it")
}

func TestStackOrderMovesAChildListedBeforeItsParentAfterIt(t *testing.T) {
	parent := change("1")
	child := stacked("2", parent)
	grandchild := stacked("3", child)
	got := stackOrder([]types.Change{grandchild, change("4"), child, parent})
	assert.Equal(t, [][]string{{"1", "2", "3", "4"}}, ids([][]types.Change{got}))
}

// stackWorld is a set of changes branched from each other, as FuzzStacks draws them, and
// the truth planning has to find.
type stackWorld struct {
	in       stackInput
	parentOf map[string]string          // the change each was branched from, "" off the base
	topOf    map[string]string          // each listed or merged change's top
	ownOf    map[string]map[string]bool // the commits each open change carries
	merged   map[string]bool
	dup      bool // two listed changes share a head
}

var fuzzMethods = []types.MergeMethod{types.MethodMerge, types.MethodSquash, types.MethodRebase}

// drawStacks decodes one world. Every choice is documented at its draw, in the order the
// seeds in testdata/fuzz/FuzzStacks spell them.
func drawStacks(d *draw) stackWorld {
	w := stackWorld{parentOf: map[string]string{}, topOf: map[string]string{}, ownOf: map[string]map[string]bool{}, merged: map[string]bool{}}
	type node struct {
		id      string
		method  types.MergeMethod
		carried map[string]bool // what a change branched from its top carries of it
		atHead  map[string]bool // what a change branched from its head carries of it
		merged  bool
		queued  bool
	}
	var nodes []node
	nMerged := d.n(3) // merged changes
	for j := range nMerged {
		id := fmt.Sprint("m", j)
		method := fuzzMethods[d.n(3)] // its merge method
		k := 1 + d.n(3)               // its own commits
		viaUpdate := d.n(2) == 1      // it merged through an update commit, which it is reported at
		var own []string
		for x := k; x >= 1; x-- {
			own = append(own, oid(id, fmt.Sprint(x)))
		}
		reported := own[0]
		if viaUpdate {
			reported = oid(id, "update")
			own = append([]string{reported}, own...)
		}
		n := node{id: id, method: method, merged: true, carried: set(), atHead: set()}
		if method != types.MethodMerge {
			// A merge put its commits on the base; a squash or a rebase left them off it.
			n.carried, n.atHead = set(own[len(own)-k:]...), set(own[len(own)-k:]...)
			w.in.mergedOwn = append(w.in.mergedOwn, own)
		} else {
			w.in.mergedOwn = append(w.in.mergedOwn, nil)
		}
		w.in.merged = append(w.in.merged, types.MergedChange{ID: id, Head: reported, Commit: oid(id, "on base"), Method: method})
		w.merged[id] = true
		nodes = append(nodes, n)
	}
	nOpen := 1 + d.n(6) // open changes
	var listed []types.Change
	var listedOwn []map[string]bool
	var listedTops []string
	for i := range nOpen {
		id := fmt.Sprint(i + 1)
		from := d.n(1 + len(nodes)) // 0 is the base, else the change it is branched from
		k := 1 + d.n(3)             // its own commits
		fromHead := d.n(2) == 1     // branched from the head of a change beneath with a merge of the base on top, not its top
		mergeOfBase := d.n(3) == 0  // a merge of the base on top of its own commits
		methodChoice := d.n(5)      // 0-2 inherits the method of what it is branched from, 3 is merge, 4 is rebase
		hint := d.n(3)              // the provider's declared parent: 0 none, 1 the true one, 2 a wrong one
		queued := d.n(5) != 0       // it carries merge intent
		fork := d.n(9) == 0         // it comes from a fork
		carries := set()
		method := types.MethodSquash
		if from > 0 {
			p := nodes[from-1]
			w.parentOf[id] = p.id
			method = p.method
			carries = maps.Clone(p.carried)
			if fromHead {
				carries = maps.Clone(p.atHead)
			}
		}
		switch methodChoice {
		case 3:
			method = types.MethodMerge
		case 4:
			method = types.MethodRebase
		}
		for x := 1; x <= k; x++ {
			carries[oid(id, fmt.Sprint(x))] = true
		}
		top := oid(id, fmt.Sprint(k))
		atHead := maps.Clone(carries)
		headID := top
		if mergeOfBase {
			headID = oid(id, "merge of base")
			atHead[headID] = true
		}
		n := node{id: id, method: method, carried: carries, atHead: atHead, queued: queued}
		nodes = append(nodes, n)
		w.ownOf[id] = atHead
		if !queued {
			w.in.unqueued = append(w.in.unqueued, types.UnqueuedChange{ID: id, Head: headID})
			w.in.unqueuedTops = append(w.in.unqueuedTops, top)
			continue
		}
		c := types.Change{ID: id, Head: headID, Base: "main", Method: method, Fork: fork}
		switch {
		case hint == 1 && from > 0:
			c.Parent = nodes[from-1].id
		case hint == 2:
			c.Parent = "99"
		}
		w.topOf[id] = top
		listed = append(listed, c)
		listedOwn = append(listedOwn, atHead)
		listedTops = append(listedTops, top)
	}
	if len(listed) > 1 && d.n(6) == 0 { // two listed changes at one head
		listed[len(listed)-1].Head = listed[0].Head
		w.dup = true
	}
	for i := len(listed) - 1; i > 0; i-- { // the queue order
		j := d.n(i + 1)
		listed[i], listed[j] = listed[j], listed[i]
		listedOwn[i], listedOwn[j] = listedOwn[j], listedOwn[i]
		listedTops[i], listedTops[j] = listedTops[j], listedTops[i]
	}
	w.in.changes, w.in.own, w.in.tops = listed, listedOwn, listedTops
	return w
}

// FuzzStacks holds planning's stack decisions, composed as [Planner.Run] composes them,
// to the queue's stack invariants:
//
//	I13 stack order: an admitted change carrying a listed change's top is admitted after
//	    it in the same partition, unless that change is already on the base; one carrying
//	    an unqueued change's head or top is never admitted; and its stack base is the top of the
//	    change it was branched from, or the newest commit of a merged one it carries.
//	I16 cascade without blame: what is stacked on a change planning settled waits, and
//	    waits on a kicked change as WAIT_BELOW_KICKED; only a change's own decision kicks.
//	No cycles: no change is admitted on a chain that comes back to it.
//	No duplicate heads: two listed changes at one head never reach planning.
//
// Admission, which reads the provider and the build tool, is drawn too: each change
// planning did not settle on its stack is kicked, held, found on the base, or admitted
// with a drawn affected set.
func FuzzStacks(f *testing.F) {
	f.Add([]byte{})
	f.Fuzz(stacksHold)
}

// stacksHold is FuzzStacks's property over one input.
func stacksHold(t *testing.T, data []byte) {
	d := &draw{data: data}
	w := drawStacks(d)
	doc := types.Changes{Base: "main", Changes: w.in.changes, Merged: w.in.merged, Unqueued: w.in.unqueued}
	if w.dup {
		require.ErrorContains(t, doc.Check(), "share head", "no duplicate heads")
		return
	}
	require.NoError(t, doc.Check())

	changes := w.in.changes
	verdicts := make([]*types.Verdict, len(changes))
	for i, c := range changes {
		if c.Fork {
			verdicts[i] = decided(c, types.DecisionKick, types.CodeKickRefused, "a change from a fork", forkReport)
		}
	}
	w.in.detect(verdicts)
	own := map[string]*types.Verdict{}
	for i, c := range changes {
		if verdicts[i] != nil {
			own[c.ID] = verdicts[i]
			continue
		}
		// Only a change off the base, or off a merged one, can be found on the base: one
		// on the base carries no listed change's commits.
		onBase := w.parentOf[c.ID] == "" || w.merged[w.parentOf[c.ID]]
		switch choice := d.n(8); {
		case choice == 0:
			verdicts[i] = decided(c, types.DecisionKick, types.CodeKickConflict, "conflicts", "report")
		case choice == 1:
			verdicts[i] = decided(c, types.DecisionWait, types.CodeWaitNotApproved, "not approved", "")
		case choice == 2 && onBase:
			verdicts[i] = decided(c, types.DecisionMerged, "", "its head is already on main", "")
		default:
			changes[i].Affected = []string{}
			for u := range 3 {
				if d.n(2) == 1 {
					changes[i].Affected = append(changes[i].Affected, fmt.Sprint("u", u))
				}
			}
		}
		own[c.ID] = verdicts[i]
	}
	settled, partitions := arrange(changes, verdicts)

	plan := types.Plan{Base: "main", BaseCommit: base, Depth: 1, Partitions: partitions, Verdicts: settled, Merged: w.in.merged, Unqueued: w.in.unqueued}
	require.NoError(t, plan.Check(), "stack order, no duplicates, and every code of its decision's class")

	where := map[string][2]int{}
	for gi, g := range partitions {
		for pos, c := range g {
			where[c.ID] = [2]int{gi, pos}
		}
	}
	decision := map[string]types.Verdict{}
	for _, v := range settled {
		decision[v.Change.ID] = v
	}
	for _, v := range settled {
		if own[v.Change.ID] != nil {
			continue
		}
		assert.Equal(t, types.DecisionWait, v.Decision, "I16: #%s is held for what is beneath it, never blamed", v.Change.ID)
		below := decision[v.Change.Below]
		if below.Decision == types.DecisionKick || below.Code == types.CodeWaitBelowKicked {
			assert.Equal(t, types.CodeWaitBelowKicked, v.Code, "I16: #%s is stacked on the kicked #%s", v.Change.ID, v.Change.Below)
		} else {
			assert.Equal(t, types.CodeWaitBelow, v.Code, "I16: #%s", v.Change.ID)
		}
	}
	for _, c := range slices.Concat(partitions...) {
		mine := w.ownOf[c.ID]
		for id, top := range w.topOf {
			if id == c.ID || !mine[top] {
				continue
			}
			if pos, ok := where[id]; ok {
				assert.Equal(t, pos[0], where[c.ID][0], "I13: #%s carries #%s but is in another partition", c.ID, id)
				assert.Less(t, pos[1], where[c.ID][1], "I13: #%s carries #%s but merges first", c.ID, id)
				continue
			}
			assert.Equal(t, types.DecisionMerged, decision[id].Decision, "I13: #%s carries #%s, which is %s, and was admitted", c.ID, id, decision[id].Code)
		}
		for k, u := range w.in.unqueued {
			assert.False(t, mine[u.Head] || mine[w.in.unqueuedTops[k]], "I13: #%s carries the unqueued #%s and was admitted", c.ID, u.ID)
		}
		switch p := w.parentOf[c.ID]; {
		case p == "":
		case w.merged[p]:
			m := w.in.merged[slices.IndexFunc(w.in.merged, func(m types.MergedChange) bool { return m.ID == p })]
			if m.Method != types.MethodMerge {
				assert.Equal(t, oidOfNewest(m.ID, w), c.StackBase, "I13: #%s is stacked on the newest commit of #%s it carries", c.ID, p)
			}
		case w.topOf[p] != "":
			assert.Equal(t, w.topOf[p], c.StackBase, "I13: #%s is stacked on #%s's top", c.ID, p)
		}
	}
	admitted := map[string]types.Change{}
	for _, c := range slices.Concat(partitions...) {
		admitted[c.ID] = c
	}
	for _, c := range admitted {
		steps := 0
		for id := c.Below; id != ""; id = admitted[id].Below {
			require.NotEqual(t, c.ID, id, "no cycles: #%s is admitted on a chain through itself", c.ID)
			steps++
			require.LessOrEqual(t, steps, len(admitted), "no cycles")
		}
	}
}

// oidOfNewest is the newest own commit of merged change id, which the change branched
// from it carries: not the update commit it may be reported at.
func oidOfNewest(id string, w stackWorld) string {
	for j, m := range w.in.merged {
		if m.ID == id {
			own := w.in.mergedOwn[j]
			if own[0] == oid(id, "update") {
				return own[1]
			}
			return own[0]
		}
	}
	return ""
}
