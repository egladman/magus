package review

import (
	"errors"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func unreadFixture() types.Diff {
	ref := func(path string, i int) types.DiffHunkRef { return types.DiffHunkRef{Path: path, Index: i} }
	step := func(n int, refs ...types.DiffHunkRef) types.DiffStep {
		s := types.DiffStep{Number: n}
		for _, r := range refs {
			s.Hunks = append(s.Hunks, types.DiffStepHunk{Ref: r})
		}
		return s
	}
	return types.Diff{
		Base: "main",
		Files: []types.DiffFile{
			{Path: "core.go", Hunks: []types.DiffHunk{
				{Index: 0, Digest: "core0", NewStart: 3, NewCount: 1},
				{Index: 1, Digest: "core1", NewStart: 9, NewCount: 2},
			}},
			{Path: "other.go", Hunks: []types.DiffHunk{{Index: 0, Digest: "other0", NewStart: 20, NewCount: 1}}},
			{Path: "logo.png"},
		},
		Order: &types.DiffOrder{
			Groups: []types.DiffGroup{
				{Kind: types.DiffGroupConnected, Label: "F", HunkCount: 2, Steps: []types.DiffStep{step(1, ref("core.go", 0)), step(2, ref("other.go", 0))}},
				{Kind: types.DiffGroupConnected, Label: "G", HunkCount: 1, Steps: []types.DiffStep{step(3, ref("core.go", 1))}},
			},
			Count: types.DiffOrderCount{HunkCount: 3, Placed: 3, Complete: true, FilesWithoutHunks: []string{"logo.png"}},
		},
	}
}

// TestFilterUnreadKeepsOnlyTheHunksNoMarkCovers. A file keeps its unread hunks and goes when it
// has none; the order keeps its steps' original numbers, since each Why names a step by number.
func TestFilterUnreadKeepsOnlyTheHunksNoMarkCovers(t *testing.T) {
	got := FilterUnread(unreadFixture(), []string{"core0", "elsewhere"}, nil)

	assert.Equal(t, types.Diff{
		Base: "main",
		Files: []types.DiffFile{
			{Path: "core.go", Hunks: []types.DiffHunk{{Index: 1, Digest: "core1", NewStart: 9, NewCount: 2}}},
			{Path: "other.go", Hunks: []types.DiffHunk{{Index: 0, Digest: "other0", NewStart: 20, NewCount: 1}}},
		},
		Order: &types.DiffOrder{
			Groups: []types.DiffGroup{
				{Kind: types.DiffGroupConnected, Label: "F", HunkCount: 1, Steps: []types.DiffStep{
					{Number: 2, Hunks: []types.DiffStepHunk{{Ref: types.DiffHunkRef{Path: "other.go", Index: 0}}}},
				}},
				{Kind: types.DiffGroupConnected, Label: "G", HunkCount: 1, Steps: []types.DiffStep{
					{Number: 3, Hunks: []types.DiffStepHunk{{Ref: types.DiffHunkRef{Path: "core.go", Index: 1}}}},
				}},
			},
			Count: types.DiffOrderCount{HunkCount: 2, Placed: 2, Complete: true},
		},
		Unread: &types.DiffUnread{ReadState: types.DiffReadStateKnown, Hunks: 3, Unread: 2},
	}, got)
	assert.Equal(t, []string{"core.go:9-10", "other.go:20"}, HunkNames(got))
	assert.Equal(t, "2 of 3 hunks in the range main...HEAD are not marked read", UnreadLine(*got.Unread, "the range main...HEAD"))
}

// TestFilterUnreadOfAFullyReadChangesetIsEmptyAndSaysSo.
func TestFilterUnreadOfAFullyReadChangesetIsEmptyAndSaysSo(t *testing.T) {
	got := FilterUnread(unreadFixture(), []string{"core0", "core1", "other0"}, nil)

	assert.Empty(t, got.Files)
	assert.Empty(t, got.Order.Groups)
	assert.Equal(t, &types.DiffUnread{ReadState: types.DiffReadStateKnown, Hunks: 3}, got.Unread)
	assert.Equal(t, "every hunk of the working tree is marked read (3 hunks)", UnreadLine(*got.Unread, "the working tree"))
}

// TestFilterUnreadCallsNoHunkUnreadWhenTheMarksCannotBeRead. An unreadable store says nothing
// about what is unread, so the filtered report is empty and says the state is unknown.
func TestFilterUnreadCallsNoHunkUnreadWhenTheMarksCannotBeRead(t *testing.T) {
	got := FilterUnread(unreadFixture(), nil, errors.New("read marks: permission denied"))

	assert.Empty(t, got.Files)
	assert.Nil(t, got.Order)
	assert.Equal(t, &types.DiffUnread{ReadState: types.DiffReadStateUnknown, Reason: "read marks: permission denied", Hunks: 3}, got.Unread)
	assert.Equal(t, "read state unknown for the working tree: the read marks could not be read (read marks: permission denied); no hunk is called unread",
		UnreadLine(*got.Unread, "the working tree"))
}
