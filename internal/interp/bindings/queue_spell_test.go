package bindings

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// withQueueSpell registers a fake provider answering per op and selects it, recording
// every request it received.
func withQueueSpell(t *testing.T, answer func(op string, params map[string]any) (any, error)) (queue.Provider, *[]spells.InvokeRequest) {
	t.Helper()
	fakeSpellSeq++
	name := fmt.Sprintf("fake-queue-%s-%d", t.Name(), fakeSpellSeq)
	var seen []spells.InvokeRequest
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name,
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			seen = append(seen, req)
			return answer(req.Target, req.Params)
		})))
	prev := QueueProvider()
	SetQueueProvider(name)
	t.Cleanup(func() { SetQueueProvider(prev) })
	p, err := OpenQueueProvider()
	require.NoError(t, err)
	return p, &seen
}

var change = queue.Change{ID: "7", Repo: "acme/acme", Head: "abc", Base: "main"}

func TestOpenQueueProviderRefusesWhenNoneIsWired(t *testing.T) {
	prev := QueueProvider()
	SetQueueProvider("")
	t.Cleanup(func() { SetQueueProvider(prev) })
	_, err := OpenQueueProvider()
	require.ErrorContains(t, err, `magus\queue.provider`)
}

func TestQueueListDecodesEveryField(t *testing.T) {
	p, seen := withQueueSpell(t, func(string, map[string]any) (any, error) {
		return []any{map[string]any{
			"id": "7", "repo": "acme/acme", "head": "abc", "ref": "refs/pull/7/head",
			"branch": "feat", "base": "main", "title": "add x", "author": "priya",
		}}, nil
	})
	got, err := p.List(context.Background(), queue.ListQuery{Base: "main", Remote: "git@github.com:acme/acme.git"})
	require.NoError(t, err)
	assert.Equal(t, []queue.Change{{
		ID: "7", Repo: "acme/acme", Head: "abc", Ref: "refs/pull/7/head",
		Branch: "feat", Base: "main", Title: "add x", Author: "priya",
	}}, got)
	assert.Equal(t, map[string]any{"base": "main", "remote": "git@github.com:acme/acme.git"}, (*seen)[0].Params)
}

func TestQueueListRefusesAChangeWithoutAHead(t *testing.T) {
	p, _ := withQueueSpell(t, func(string, map[string]any) (any, error) {
		return []any{map[string]any{"id": "7"}}, nil
	})
	_, err := p.List(context.Background(), queue.ListQuery{Base: "main"})
	require.ErrorContains(t, err, "needs both id and head")
}

func TestQueueApprovalDecodesAtTheCommitAsked(t *testing.T) {
	p, seen := withQueueSpell(t, func(string, map[string]any) (any, error) {
		return map[string]any{"approved": true, "head": "abc", "required": float64(1), "approvals": float64(2)}, nil
	})
	got, err := p.ApprovalAt(context.Background(), change, "abc")
	require.NoError(t, err)
	assert.Equal(t, queue.Approval{Approved: true, Head: "abc", Required: 1, Approvals: 2}, got)
	assert.Equal(t, "abc", (*seen)[0].Params["sha"])
	assert.Equal(t, "7", (*seen)[0].Params["id"])
}

func TestQueueApprovalOfTheWrongTypeNamesTheField(t *testing.T) {
	p, _ := withQueueSpell(t, func(string, map[string]any) (any, error) {
		return map[string]any{"approved": "yes"}, nil
	})
	_, err := p.ApprovalAt(context.Background(), change, "abc")
	require.ErrorContains(t, err, `field "approved" is string, want bool`)
}

func TestQueueStatusPostsTheQueueContext(t *testing.T) {
	p, seen := withQueueSpell(t, func(string, map[string]any) (any, error) { return true, nil })
	require.NoError(t, p.PostStatus(context.Background(), change, "abc", queue.Status{State: queue.StateSuccess, Description: "ok"}))
	assert.Equal(t, spells.PostStatusContract, (*seen)[0].Target)
	assert.Equal(t, "magus/queue", (*seen)[0].Params["context"])
	assert.Equal(t, "success", (*seen)[0].Params["state"])
}

func TestQueueRefusalIsAnError(t *testing.T) {
	p, _ := withQueueSpell(t, func(op string, _ map[string]any) (any, error) {
		if op == spells.MergeChangeContract {
			return map[string]any{"merged": false, "reason": "head moved"}, nil
		}
		return false, nil
	})
	require.ErrorContains(t, p.Merge(context.Background(), change, "abc"), "head moved")
	require.ErrorContains(t, p.KickBack(context.Background(), change, "abc", "report"), "the host refused")
}

func TestQueueOpTheSpellLacksIsAnError(t *testing.T) {
	p, _ := withQueueSpell(t, func(string, map[string]any) (any, error) { return nil, nil })
	require.ErrorContains(t, p.KickBack(context.Background(), change, "abc", "report"), "does not implement kick_back")
}
