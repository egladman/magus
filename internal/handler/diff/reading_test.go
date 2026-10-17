package diff

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// githubReview is a workspace whose branch lives on github.com, which is what the reading
// command is built for.
type githubReview struct{ cleanBranchReview }

func (githubReview) ReviewOrigin(context.Context) types.ReviewOrigin {
	return types.ReviewOrigin{Branch: "feat/x", Remote: "git@github.com:acme/acme.git"}
}

// withReviewTarget registers a provider that answers the review lookup with target.
func withReviewTarget(t *testing.T, target map[string]any) {
	t.Helper()
	name := register.Spell(t, "reading-review",
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			if req.Target == spells.FindReviewContract {
				return target, nil
			}
			return map[string]any{}, nil
		}))
	prev := bindings.ReviewProvider()
	bindings.SetReviewProvider(name)
	t.Cleanup(func() { bindings.SetReviewProvider(prev) })
}

func readingHandler(t *testing.T, ws reviewSource) (*ReviewHandler, *changeset.Store) {
	t.Helper()
	store := changeset.NewStore(t.TempDir())
	return NewReviewHandler(ReviewOptions{Sessions: store, Workspace: ws, Root: t.TempDir()}, nil), store
}

// The op marks the review locally and answers with the command the person may run. The mark
// needs no attached session: a reader can say they are reading before any hunk is fetched.
func TestReadingMarksTheReviewAndPrintsTheForgeCommand(t *testing.T) {
	withReviewTarget(t, map[string]any{"id": "482", "repo": "acme/acme", "viewer": "eli"})
	h, store := readingHandler(t, githubReview{})

	w := post(t, h, `{"op":"reading","on":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got readingResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	mark := loadReading(t, store)
	assert.Equal(t, changeset.ReadingMark{Review: "482", Repo: "acme/acme", Since: got.Since}, mark)
	assert.Equal(t, readingResponse{
		Reading: true,
		Since:   mark.Since,
		Command: "gh pr comment 482 --repo acme/acme --body 'eli is reading this now'",
	}, got)
	assert.NotZero(t, got.Since)
}

func TestReadingOffClearsTheMark(t *testing.T) {
	withReviewTarget(t, map[string]any{"id": "482", "repo": "acme/acme"})
	h, store := readingHandler(t, githubReview{})
	require.Equal(t, http.StatusOK, post(t, h, `{"op":"reading","on":true}`).Code)

	w := post(t, h, `{"op":"reading","on":false}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"reading":false}`, w.Body.String())
	assert.False(t, loadReading(t, store).Active())
}

// loadReading is the store's mark, failing the test when it cannot be read.
func loadReading(t *testing.T, store *changeset.Store) changeset.ReadingMark {
	t.Helper()
	m, err := store.LoadReading()
	require.NoError(t, err)
	return m
}

// Only GitHub's shape is known, so any other forge gets the mark and no command rather than a
// guessed one.
func TestReadingOnAnotherForgeMarksWithoutACommand(t *testing.T) {
	withReviewTarget(t, map[string]any{"id": "482", "repo": "acme/acme"})
	h, store := readingHandler(t, cleanBranchReview{})

	w := post(t, h, `{"op":"reading","on":true}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got readingResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.True(t, got.Reading)
	assert.Empty(t, got.Command)
	assert.True(t, loadReading(t, store).Active())
}

// A review that has landed has nothing left to hold, and recording the mark would make the next
// job tick report a merge under a reader who started after it.
func TestReadingAMergedReviewIsRefused(t *testing.T) {
	withReviewTarget(t, map[string]any{"id": "482", "repo": "acme/acme", "state": "merged"})
	h, store := readingHandler(t, githubReview{})

	w := post(t, h, `{"op":"reading","on":true}`)

	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.False(t, loadReading(t, store).Active())
}

func TestReadingWithNoReviewIsAnError(t *testing.T) {
	withReviewTarget(t, map[string]any{"id": "", "reason": "no pull request for this branch"})
	h, _ := readingHandler(t, githubReview{})

	w := post(t, h, `{"op":"reading","on":true}`)

	assert.Equal(t, http.StatusBadGateway, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "no pull request for this branch")
}

// A server with nowhere to keep the mark must say so rather than answer as if it had recorded one.
func TestReadingWithNoStateDirIsAnError(t *testing.T) {
	withReviewTarget(t, map[string]any{"id": "482", "repo": "acme/acme"})
	h := NewReviewHandler(ReviewOptions{Sessions: changeset.NewStore(""), Workspace: githubReview{}, Root: t.TempDir()}, nil)

	w := post(t, h, `{"op":"reading","on":true}`)

	assert.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
}
