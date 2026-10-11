package changeset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func ids(cs []types.ReviewComment) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

// shape reads a grouping as head -> reply ids, in the order the threads come back.
func shape(threads []Thread) [][]string {
	var out [][]string
	for _, t := range threads {
		out = append(out, ids(t.Comments()))
	}
	return out
}

// A comment is a reply only when its Root names a top-level comment in the list. Every other
// shape heads a thread of its own, so no input can index a missing slot or attach a comment to
// something that is not a thread head.
func TestGroupThreadsOnlyCountsARootThatIsATopLevelComment(t *testing.T) {
	cases := map[string]struct {
		in   []types.ReviewComment
		want [][]string
	}{
		"empty": {in: nil, want: nil},
		"replies follow their head in list order": {
			in: []types.ReviewComment{
				{ID: "a"}, {ID: "b"}, {ID: "a2", Root: "a"}, {ID: "b2", Root: "b"}, {ID: "a3", Root: "a"},
			},
			want: [][]string{{"a", "a2", "a3"}, {"b", "b2"}},
		},
		"a reply that precedes its head still follows it": {
			in:   []types.ReviewComment{{ID: "r", Root: "h"}, {ID: "h"}},
			want: [][]string{{"h", "r"}},
		},
		"a reply whose head the host trimmed heads its own thread": {
			in:   []types.ReviewComment{{ID: "r1", Root: "gone"}, {ID: "r2", Root: "gone"}},
			want: [][]string{{"r1"}, {"r2"}},
		},
		"a comment rooted at itself heads its own thread": {
			in:   []types.ReviewComment{{ID: "x", Root: "x"}},
			want: [][]string{{"x"}},
		},
		"comments rooted at each other each head their own": {
			in:   []types.ReviewComment{{ID: "a", Root: "b"}, {ID: "b", Root: "a"}},
			want: [][]string{{"a"}, {"b"}},
		},
		"a reply to a reply heads its own thread": {
			in:   []types.ReviewComment{{ID: "h"}, {ID: "r", Root: "h"}, {ID: "rr", Root: "r"}},
			want: [][]string{{"h", "r"}, {"rr"}},
		},
		"a self-rooted comment does not capture a reply to it": {
			in:   []types.ReviewComment{{ID: "x", Root: "x"}, {ID: "y", Root: "x"}},
			want: [][]string{{"x"}, {"y"}},
		},
		"a top-level duplicate id leaves its replies with the first": {
			in:   []types.ReviewComment{{ID: "h"}, {ID: "h"}, {ID: "r", Root: "h"}},
			want: [][]string{{"h", "r"}, {"h"}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, shape(GroupThreads(tc.in)))
		})
	}
}

func TestFindThreadAnswersAnyCommentWithItsThread(t *testing.T) {
	in := []types.ReviewComment{
		{ID: "t1", Author: "ana"}, {ID: "t2"}, {ID: "t1b", Root: "t1"}, {ID: "orphan", Root: "gone"},
	}

	fromHead, err := FindThread(in, "t1")
	require.NoError(t, err)
	fromReply, err := FindThread(in, " t1b ")
	require.NoError(t, err)
	assert.Equal(t, fromHead, fromReply)
	assert.Equal(t, "t1", fromReply.ID())
	assert.Equal(t, "ana", fromReply.Head.Author)

	orphan, err := FindThread(in, "orphan")
	require.NoError(t, err)
	assert.Equal(t, "orphan", orphan.ID(), "a comment whose head is gone is its own thread")
}

func TestFindThreadRefusesWhatNamesNoThread(t *testing.T) {
	in := []types.ReviewComment{{ID: "t1"}, {ID: "r", Root: "gone"}}
	for name, id := range map[string]string{"empty": " ", "unknown": "zzz", "a head the host trimmed": "gone"} {
		t.Run(name, func(t *testing.T) {
			_, err := FindThread(in, id)
			require.ErrorIs(t, err, ErrNoThread)
		})
	}
}
