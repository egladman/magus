package queue

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue/types"
)

func TestPartitionSeparatesDisjointSetsAndKeepsQueueOrder(t *testing.T) {
	got := partition([]types.Change{change("1", "a"), change("2", "b"), change("3", "a", "c")})
	assert.Equal(t, [][]string{{"1", "3"}, {"2"}}, ids(got))
}

func TestPartitionJoinsGroupsThroughAChangeThatReachesBoth(t *testing.T) {
	got := partition([]types.Change{change("1", "a"), change("2", "b"), change("3", "b", "a")})
	assert.Equal(t, [][]string{{"1", "2", "3"}}, ids(got))
}

func TestPartitionNeverAssumesIndependenceItCannotProve(t *testing.T) {
	unknown := change("2")
	unbounded := change("4", "d")
	unbounded.UnboundedBy = "edits the declarations"
	assert.Equal(t, [][]string{{"1", "2", "3"}}, ids(partition([]types.Change{change("1", "a"), unknown, change("3", "c")})))
	assert.Equal(t, [][]string{{"1", "4"}}, ids(partition([]types.Change{change("1", "a"), unbounded})))
}

func TestPartitionOfAChangeAffectingNothingStandsAlone(t *testing.T) {
	empty := types.Change{ID: "2", Head: head("2"), Affected: []string{}}
	assert.Equal(t, [][]string{{"1"}, {"2"}}, ids(partition([]types.Change{change("1", "a"), empty})))
}

// A stacked change merges after the one beneath it, so they share a partition even
// when their keys do not.
func TestPartitionKeepsAStackTogether(t *testing.T) {
	child := change("2", "b")
	child.Below = "1"
	assert.Equal(t, [][]string{{"1", "2"}, {"3"}}, ids(partition([]types.Change{change("1", "a"), child, change("3", "c")})))
}

func TestPartitionOfNothingIsNothing(t *testing.T) {
	assert.Nil(t, partition(nil))
}

// FuzzPartition holds partitioning to what merging in parallel rests on: every change
// in exactly one group, queue order inside a group, groups ordered by their first change,
// a stacked change with the change beneath it, no unit shared across groups, and one
// group whenever a change's reach is unproven.
func FuzzPartition(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{5, 1, 2, 0, 2, 4, 0, 3, 8, 1, 1, 0, 6, 0, 2})
	f.Add([]byte{3, 3, 1, 2, 0, 0, 1, 7, 0, 5})
	f.Fuzz(partitionHolds)
}

// partitionHolds is FuzzPartition's property over one input.
func partitionHolds(t *testing.T, data []byte) {
	d := &draw{data: data}
	n := d.n(8)
	changes := make([]types.Change, n)
	proof := true
	for i := range changes {
		c := change(fmt.Sprint(i))
		switch d.n(8) {
		case 0:
			c.Affected = nil
		case 1:
			c.Affected, c.UnboundedBy = []string{"u0"}, "edits the declarations"
		default:
			c.Affected = []string{}
			for u := range 4 {
				if d.n(3) == 0 {
					c.Affected = append(c.Affected, fmt.Sprint("u", u))
				}
			}
		}
		if i > 0 && d.n(3) == 0 {
			c.Below = fmt.Sprint(d.n(i))
		}
		proof = proof && proven(c)
		changes[i] = c
	}
	groups := partition(changes)

	group := map[string]int{}
	last := -1
	for gi, g := range groups {
		require.NotEmpty(t, g)
		first, _ := strconv.Atoi(g[0].ID)
		require.Greater(t, first, last, "groups are ordered by their first change")
		last = first
		prev := -1
		for _, c := range g {
			_, twice := group[c.ID]
			require.False(t, twice, "#%s is in two groups", c.ID)
			group[c.ID] = gi
			pos, _ := strconv.Atoi(c.ID)
			require.Greater(t, pos, prev, "queue order inside a group")
			prev = pos
		}
	}
	require.Len(t, group, n)
	if !proof {
		require.LessOrEqual(t, len(groups), 1, "independence nobody proved is never assumed")
	}
	for _, c := range changes {
		if c.Below != "" {
			require.Equal(t, group[c.Below], group[c.ID], "#%s is stacked on #%s", c.ID, c.Below)
		}
		for _, o := range changes {
			if group[o.ID] != group[c.ID] {
				for _, u := range c.Affected {
					require.NotContains(t, o.Affected, u, "#%s and #%s share %s across groups", c.ID, o.ID, u)
				}
			}
		}
	}
}

// BenchmarkPartition sizes planning at monorepo scale: n open changes over a
// 10k-project graph, each reaching 20 projects.
func BenchmarkPartition(b *testing.B) {
	for _, n := range []int{100, 1000} {
		changes := make([]types.Change, n)
		for i := range changes {
			units := make([]string, 20)
			for j := range units {
				units[j] = fmt.Sprintf("p%d", (i*37+j*101)%10000)
			}
			changes[i] = change(fmt.Sprint(i), units...)
		}
		b.Run(fmt.Sprintf("changes=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				partition(changes)
			}
		})
	}
}
