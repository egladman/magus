package queue

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func entry(id string, proven bool, projects ...string) Entry {
	return Entry{Change: Change{ID: id}, Closure: Closure{Projects: projects, Proven: proven}}
}

func ids(groups []Group) [][]string {
	var out [][]string
	for _, g := range groups {
		var row []string
		for _, e := range g.Entries {
			row = append(row, e.Change.ID)
		}
		out = append(out, row)
	}
	return out
}

func TestPlanSeparatesDisjointClosuresAndKeepsQueueOrder(t *testing.T) {
	got := Partition([]Entry{
		entry("1", true, "a"),
		entry("2", true, "b"),
		entry("3", true, "a", "c"),
	})
	assert.Equal(t, [][]string{{"1", "3"}, {"2"}}, ids(got))
}

func TestPlanJoinsGroupsThroughAChangeThatReachesBoth(t *testing.T) {
	got := Partition([]Entry{
		entry("1", true, "a"),
		entry("2", true, "b"),
		entry("3", true, "b", "a"),
	})
	assert.Equal(t, [][]string{{"1", "2", "3"}}, ids(got))
}

func TestPlanNeverAssumesIndependenceItCannotProve(t *testing.T) {
	got := Partition([]Entry{
		entry("1", true, "a"),
		entry("2", false),
		entry("3", true, "c"),
	})
	assert.Equal(t, [][]string{{"1", "2", "3"}}, ids(got))
}

func TestPlanOfNothingIsNothing(t *testing.T) {
	assert.Nil(t, Partition(nil))
}

// BenchmarkPlan sizes planning at monorepo scale: n open changes over a 10k-project
// graph, each reaching 20 projects.
func BenchmarkPartition(b *testing.B) {
	for _, n := range []int{100, 1000} {
		entries := make([]Entry, n)
		for i := range entries {
			projects := make([]string, 20)
			for j := range projects {
				projects[j] = fmt.Sprintf("p%d", (i*37+j*101)%10000)
			}
			entries[i] = entry(fmt.Sprint(i), true, projects...)
		}
		b.Run(fmt.Sprintf("changes=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Partition(entries)
			}
		})
	}
}
