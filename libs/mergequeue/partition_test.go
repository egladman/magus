package mergequeue

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func ids(groups [][]Change) [][]string {
	var out [][]string
	for _, g := range groups {
		var row []string
		for _, c := range g {
			row = append(row, c.ID)
		}
		out = append(out, row)
	}
	return out
}

func TestPartitionSeparatesDisjointSetsAndKeepsQueueOrder(t *testing.T) {
	got := Partition([]Change{change("1", "a"), change("2", "b"), change("3", "a", "c")})
	assert.Equal(t, [][]string{{"1", "3"}, {"2"}}, ids(got))
}

func TestPartitionJoinsGroupsThroughAChangeThatReachesBoth(t *testing.T) {
	got := Partition([]Change{change("1", "a"), change("2", "b"), change("3", "b", "a")})
	assert.Equal(t, [][]string{{"1", "2", "3"}}, ids(got))
}

func TestPartitionNeverAssumesIndependenceItCannotProve(t *testing.T) {
	unknown := change("2")
	unbounded := change("4", "d")
	unbounded.UnboundedBy = "edits the declarations"
	assert.Equal(t, [][]string{{"1", "2", "3"}}, ids(Partition([]Change{change("1", "a"), unknown, change("3", "c")})))
	assert.Equal(t, [][]string{{"1", "4"}}, ids(Partition([]Change{change("1", "a"), unbounded})))
}

func TestPartitionOfAChangeAffectingNothingStandsAlone(t *testing.T) {
	empty := Change{ID: "2", Head: head("2"), Affected: []string{}}
	assert.Equal(t, [][]string{{"1"}, {"2"}}, ids(Partition([]Change{change("1", "a"), empty})))
}

func TestPartitionOfNothingIsNothing(t *testing.T) {
	assert.Nil(t, Partition(nil))
}

// BenchmarkPartition sizes planning at monorepo scale: n open changes over a
// 10k-project graph, each reaching 20 projects.
func BenchmarkPartition(b *testing.B) {
	for _, n := range []int{100, 1000} {
		changes := make([]Change, n)
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
				Partition(changes)
			}
		})
	}
}
