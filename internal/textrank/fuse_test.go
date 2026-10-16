package textrank

import (
	"math"
	"reflect"
	"testing"
)

func TestFuse(t *testing.T) {
	const k = 60
	tests := []struct {
		name  string
		lists []RankedList
		want  []Fused
	}{
		{
			name: "weighted two lists",
			lists: []RankedList{
				{Name: "bm25", Weight: 1.0, IDs: []string{"a", "b", "c"}},
				{Name: "name", Weight: 0.5, IDs: []string{"c", "a"}},
			},
			want: []Fused{
				{ID: "a", Score: 1.0/(k+1) + 0.5/(k+2), Ranks: map[string]int{"bm25": 1, "name": 2}},
				{ID: "c", Score: 1.0/(k+3) + 0.5/(k+1), Ranks: map[string]int{"bm25": 3, "name": 1}},
				{ID: "b", Score: 1.0 / (k + 2), Ranks: map[string]int{"bm25": 2}},
			},
		},
		{
			name: "weight outweighs rank",
			lists: []RankedList{
				{Name: "heavy", Weight: 2.0, IDs: []string{"x", "y"}},
				{Name: "light", Weight: 0.1, IDs: []string{"y", "x"}},
			},
			want: []Fused{
				{ID: "x", Score: 2.0/(k+1) + 0.1/(k+2), Ranks: map[string]int{"heavy": 1, "light": 2}},
				{ID: "y", Score: 2.0/(k+2) + 0.1/(k+1), Ranks: map[string]int{"heavy": 2, "light": 1}},
			},
		},
		{
			name: "ties sort by id",
			lists: []RankedList{
				{Name: "l1", Weight: 1, IDs: []string{"b", "a"}},
				{Name: "l2", Weight: 1, IDs: []string{"a", "b"}},
			},
			want: []Fused{
				{ID: "a", Score: 1.0/(k+2) + 1.0/(k+1), Ranks: map[string]int{"l1": 2, "l2": 1}},
				{ID: "b", Score: 1.0/(k+1) + 1.0/(k+2), Ranks: map[string]int{"l1": 1, "l2": 2}},
			},
		},
		{
			name: "duplicate in one list keeps its first rank",
			lists: []RankedList{
				{Name: "l", Weight: 1, IDs: []string{"a", "b", "a"}},
			},
			want: []Fused{
				{ID: "a", Score: 1.0 / (k + 1), Ranks: map[string]int{"l": 1}},
				{ID: "b", Score: 1.0 / (k + 2), Ranks: map[string]int{"l": 2}},
			},
		},
		{
			name: "zero weight list still records ranks",
			lists: []RankedList{
				{Name: "on", Weight: 1, IDs: []string{"a"}},
				{Name: "off", Weight: 0, IDs: []string{"a"}},
			},
			want: []Fused{
				{ID: "a", Score: 1.0 / (k + 1), Ranks: map[string]int{"on": 1, "off": 1}},
			},
		},
		{name: "nil lists", lists: nil, want: []Fused{}},
		{
			name:  "empty list",
			lists: []RankedList{{Name: "l", Weight: 1}},
			want:  []Fused{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Fuse(tt.lists, k)
			if got == nil {
				t.Fatal("Fuse returned nil, want a non-nil slice")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Fuse returned %d results, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i].ID != tt.want[i].ID {
					t.Fatalf("result %d is %q, want %q: %+v", i, got[i].ID, tt.want[i].ID, got)
				}
				if math.Abs(got[i].Score-tt.want[i].Score) > 1e-12 {
					t.Errorf("%s score = %v, want %v", got[i].ID, got[i].Score, tt.want[i].Score)
				}
				if !reflect.DeepEqual(got[i].Ranks, tt.want[i].Ranks) {
					t.Errorf("%s ranks = %v, want %v", got[i].ID, got[i].Ranks, tt.want[i].Ranks)
				}
			}
		})
	}
}

func TestFuseK(t *testing.T) {
	got := Fuse([]RankedList{{Name: "l", Weight: 1, IDs: []string{"a"}}}, 0)
	if len(got) != 1 || got[0].Score != 1.0 {
		t.Fatalf("Fuse with k=0 = %+v, want one result scoring 1/(0+1)", got)
	}
}
