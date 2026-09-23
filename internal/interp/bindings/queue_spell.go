package bindings

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/egladman/magus/internal/queue"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// queueProviderName names the spell a magusfile selected with magus\queue.provider(<spell>).
// It lives here for the reason review_provider.go gives: running the spell needs the VM.
var (
	queueProviderMu   sync.RWMutex
	queueProviderName string
)

// SetQueueProvider records the spell a magusfile selected as its merge-queue provider.
func SetQueueProvider(name string) {
	queueProviderMu.Lock()
	defer queueProviderMu.Unlock()
	queueProviderName = name
}

// QueueProvider reports the selected spell, empty when a magusfile wired none.
func QueueProvider() string {
	queueProviderMu.RLock()
	defer queueProviderMu.RUnlock()
	return queueProviderName
}

var errNoQueueProvider = errors.New(
	"no merge-queue provider wired; the root magusfile selects one with magus\\queue.provider(<spell>)")

// OpenQueueProvider adapts the selected spell to [queue.Provider]. It errors when none is
// wired or the selected name is not a registered spell: running a queue against nothing
// is misconfiguration, not an empty queue.
func OpenQueueProvider() (queue.Provider, error) {
	name := QueueProvider()
	if name == "" {
		return nil, errNoQueueProvider
	}
	drv, ok := project.DefaultSpellRegistry().Lookup(name)
	if !ok {
		return nil, fmt.Errorf("merge-queue provider %q is not a registered spell", name)
	}
	return &spellQueueProvider{drv: drv}, nil
}

// spellQueueProvider bridges the merge-queue contract (spells/queue.go) into Go, the way
// spellRemoteBackend bridges the cache contract. It has no host knowledge: it passes
// records across and decodes what comes back, refusing any answer of the wrong shape.
type spellQueueProvider struct {
	drv spells.Driver
}

func (p *spellQueueProvider) Name() string { return p.drv.Name() }

// invoke calls one contract op. A nil answer means the spell does not export the op,
// which is an error for every op here: see spells/queue.go for why none is optional.
func (p *spellQueueProvider) invoke(ctx context.Context, op string, params map[string]any) (any, error) {
	resp, err := p.drv.Invoke(ctx, spells.InvokeRequest{Target: op, Params: params})
	if err != nil {
		return nil, fmt.Errorf("merge-queue provider %q: %s: %w", p.drv.Name(), op, err)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("merge-queue provider %q does not implement %s", p.drv.Name(), op)
	}
	return resp.Data, nil
}

func (p *spellQueueProvider) where(op string) string {
	return fmt.Sprintf("merge-queue provider %q: %s", p.drv.Name(), op)
}

func changeParams(c queue.Change) map[string]any {
	return map[string]any{
		"id": c.ID, "repo": c.Repo, "head": c.Head, "ref": c.Ref,
		"branch": c.Branch, "base": c.Base, "title": c.Title, "author": c.Author,
	}
}

func (p *spellQueueProvider) List(ctx context.Context, q queue.ListQuery) ([]queue.Change, error) {
	data, err := p.invoke(ctx, spells.ListQueueContract, map[string]any{"base": q.Base, "remote": q.Remote})
	if err != nil {
		return nil, err
	}
	where := p.where(spells.ListQueueContract)
	rows, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("%s returned %T, want a list", where, data)
	}
	out := make([]queue.Change, 0, len(rows))
	for i, row := range rows {
		c, err := decodeChange(row, fmt.Sprintf("%s[%d]", where, i))
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// decodeChange refuses a record without an id or head: the engine would stage and post
// against nothing.
func decodeChange(row any, where string) (queue.Change, error) {
	m, ok := row.(map[string]any)
	if !ok {
		return queue.Change{}, fmt.Errorf("%s is %T, want a record", where, row)
	}
	var c queue.Change
	for _, f := range []struct {
		key string
		dst *string
	}{
		{"id", &c.ID}, {"repo", &c.Repo}, {"head", &c.Head}, {"ref", &c.Ref},
		{"branch", &c.Branch}, {"base", &c.Base}, {"title", &c.Title}, {"author", &c.Author},
	} {
		v, err := strField(m, f.key, where)
		if err != nil {
			return queue.Change{}, err
		}
		*f.dst = v
	}
	if c.ID == "" || c.Head == "" {
		return queue.Change{}, fmt.Errorf("%s: a change needs both id and head", where)
	}
	return c, nil
}

func (p *spellQueueProvider) ApprovalAt(ctx context.Context, c queue.Change, sha string) (queue.Approval, error) {
	params := changeParams(c)
	params["sha"] = sha
	data, err := p.invoke(ctx, spells.ApprovalAtContract, params)
	if err != nil {
		return queue.Approval{}, err
	}
	where := p.where(spells.ApprovalAtContract)
	m, ok := data.(map[string]any)
	if !ok {
		return queue.Approval{}, fmt.Errorf("%s returned %T, want a record", where, data)
	}
	var a queue.Approval
	if a.Approved, err = boolField(m, "approved", where); err != nil {
		return queue.Approval{}, err
	}
	if a.Head, err = strField(m, "head", where); err != nil {
		return queue.Approval{}, err
	}
	if a.Required, err = intField(m, "required", where); err != nil {
		return queue.Approval{}, err
	}
	if a.Approvals, err = intField(m, "approvals", where); err != nil {
		return queue.Approval{}, err
	}
	if a.Reason, err = strField(m, "reason", where); err != nil {
		return queue.Approval{}, err
	}
	return a, nil
}

func (p *spellQueueProvider) PostStatus(ctx context.Context, c queue.Change, sha string, s queue.Status) error {
	params := changeParams(c)
	params["sha"] = sha
	params["context"] = queue.StatusContext
	params["state"] = string(s.State)
	params["description"] = s.Description
	return p.acknowledged(ctx, spells.PostStatusContract, params)
}

func (p *spellQueueProvider) Merge(ctx context.Context, c queue.Change, sha string) error {
	params := changeParams(c)
	params["sha"] = sha
	data, err := p.invoke(ctx, spells.MergeChangeContract, params)
	if err != nil {
		return err
	}
	where := p.where(spells.MergeChangeContract)
	m, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("%s returned %T, want a record", where, data)
	}
	merged, err := boolField(m, "merged", where)
	if err != nil {
		return err
	}
	if !merged {
		reason, _ := strField(m, "reason", where)
		if reason == "" {
			reason = "no reason given"
		}
		return fmt.Errorf("%s: not merged: %s", where, reason)
	}
	return nil
}

func (p *spellQueueProvider) KickBack(ctx context.Context, c queue.Change, sha, report string) error {
	params := changeParams(c)
	params["sha"] = sha
	params["body"] = report
	return p.acknowledged(ctx, spells.KickBackContract, params)
}

// acknowledged invokes an op answering a bool, reading anything but true as a refusal:
// a caller told a status posted when it did not would merge around its own gate.
func (p *spellQueueProvider) acknowledged(ctx context.Context, op string, params map[string]any) error {
	data, err := p.invoke(ctx, op, params)
	if err != nil {
		return err
	}
	ok, isBool := data.(bool)
	if !isBool {
		return fmt.Errorf("%s returned %T, want bool", p.where(op), data)
	}
	if !ok {
		return fmt.Errorf("%s: the host refused", p.where(op))
	}
	return nil
}
