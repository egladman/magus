package review

import (
	"github.com/egladman/magus/types/diff"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func unreadFixture() diff.Diff {
	ref := func(path string, i int) diff.HunkRef { return diff.HunkRef{Path: path, Index: i} }
	step := func(n int, refs ...diff.HunkRef) diff.Step {
		s := diff.Step{Number: n}
		for _, r := range refs {
			s.Hunks = append(s.Hunks, diff.StepHunk{Ref: r})
		}
		return s
	}
	return diff.Diff{
		Base: "main",
		Files: []diff.File{
			{Path: "core.go", Hunks: []diff.Hunk{
				{Index: 0, Digest: "core0", NewStart: 3, NewCount: 1},
				{Index: 1, Digest: "core1", NewStart: 9, NewCount: 2},
			}},
			{Path: "other.go", Hunks: []diff.Hunk{{Index: 0, Digest: "other0", NewStart: 20, NewCount: 1}}},
			{Path: "logo.png"},
		},
		Order: &diff.Order{
			Groups: []diff.Group{
				{Kind: diff.GroupConnected, Label: "F", HunkCount: 2, Steps: []diff.Step{step(1, ref("core.go", 0)), step(2, ref("other.go", 0))}},
				{Kind: diff.GroupConnected, Label: "G", HunkCount: 1, Steps: []diff.Step{step(3, ref("core.go", 1))}},
			},
			Count: diff.OrderCount{HunkCount: 3, Placed: 3, Complete: true, FilesWithoutHunks: []string{"logo.png"}},
		},
	}
}

// TestFilterUnreadKeepsOnlyTheHunksNoMarkCovers. A file keeps its unread hunks and goes when it
// has none; the order keeps its steps' original numbers, since each Why names a step by number.
func TestFilterUnreadKeepsOnlyTheHunksNoMarkCovers(t *testing.T) {
	got := FilterUnread(unreadFixture(), []string{"core0", "elsewhere"}, nil)

	assert.Equal(t, diff.Diff{
		Base: "main",
		Files: []diff.File{
			{Path: "core.go", Hunks: []diff.Hunk{{Index: 1, Digest: "core1", NewStart: 9, NewCount: 2}}},
			{Path: "other.go", Hunks: []diff.Hunk{{Index: 0, Digest: "other0", NewStart: 20, NewCount: 1}}},
		},
		Order: &diff.Order{
			Groups: []diff.Group{
				{Kind: diff.GroupConnected, Label: "F", HunkCount: 1, Steps: []diff.Step{
					{Number: 2, Hunks: []diff.StepHunk{{Ref: diff.HunkRef{Path: "other.go", Index: 0}}}},
				}},
				{Kind: diff.GroupConnected, Label: "G", HunkCount: 1, Steps: []diff.Step{
					{Number: 3, Hunks: []diff.StepHunk{{Ref: diff.HunkRef{Path: "core.go", Index: 1}}}},
				}},
			},
			Count: diff.OrderCount{HunkCount: 2, Placed: 2, Complete: true},
		},
		Unread: &diff.Unread{ReadState: diff.ReadStateKnown, Hunks: 3, Unread: 2},
	}, got)
	assert.Equal(t, []string{"core.go:9-10", "other.go:20"}, HunkNames(got))
	assert.Equal(t, "2 of 3 hunks in the range main...HEAD are not marked read", UnreadLine(*got.Unread, "the range main...HEAD"))
}

// TestFilterUnreadOfAFullyReadChangesetIsEmptyAndSaysSo.
func TestFilterUnreadOfAFullyReadChangesetIsEmptyAndSaysSo(t *testing.T) {
	got := FilterUnread(unreadFixture(), []string{"core0", "core1", "other0"}, nil)

	assert.Empty(t, got.Files)
	assert.Empty(t, got.Order.Groups)
	assert.Equal(t, &diff.Unread{ReadState: diff.ReadStateKnown, Hunks: 3}, got.Unread)
	assert.Equal(t, "every hunk of the working tree is marked read (3 hunks)", UnreadLine(*got.Unread, "the working tree"))
}

// TestFilterUnreadCallsNoHunkUnreadWhenTheMarksCannotBeRead. An unreadable store says nothing
// about what is unread, so the filtered report is empty and says the state is unknown.
func TestFilterUnreadCallsNoHunkUnreadWhenTheMarksCannotBeRead(t *testing.T) {
	got := FilterUnread(unreadFixture(), nil, errors.New("read marks: permission denied"))

	assert.Empty(t, got.Files)
	assert.Nil(t, got.Order)
	assert.Equal(t, &diff.Unread{ReadState: diff.ReadStateUnknown, Reason: "read marks: permission denied", Hunks: 3}, got.Unread)
	assert.Equal(t, "read state unknown for the working tree; no hunk is called unread",
		UnreadLine(*got.Unread, "the working tree"))
}
