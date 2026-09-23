// Package provider runs a merge-queue [mergequeue.Provider] written in Buzz, on an
// embedded gopherbuzz VM.
//
// A provider script exports five functions, each taking one record and returning one:
//
//	list_queue({base, remote})                         > [change]
//	approval_at(change + {sha})                        > {approved, head, required, approvals, reason}
//	post_status(change + {sha, context, state, description}) > bool
//	merge_change(change + {sha, message})              > {merged, reason}
//	kick_back(change + {sha, body})                    > bool
//
// A change record carries the fields of [mergequeue.Change]. The two reads run in
// planning and landing; the three writes run only in landing, so a script should read
// its write credential under its own name, letting a job that does not hold it fail
// rather than write.
//
// Scripts see Buzz's standard library (std, os, serialize, ...) and one host module,
// "mergequeue", whose request(method, url, body, headers) makes an HTTP request and
// returns {status, body}.
package provider

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/vm"

	"github.com/egladman/magus/libs/mergequeue"
)

//go:embed github.buzz
var githubSource string

// Builtin maps each provider compiled into this package to its source.
var Builtin = map[string]string{"github": githubSource}

// Contract op names.
const (
	OpList       = "list_queue"
	OpApprovalAt = "approval_at"
	OpPostStatus = "post_status"
	OpMerge      = "merge_change"
	OpKickBack   = "kick_back"
)

// Every op is required: branch protection requires the queue's status once it is
// wired, so a provider that can list changes but not merge them would hold every
// change forever.
var ops = []string{OpList, OpApprovalAt, OpPostStatus, OpMerge, OpKickBack}

// Buzz is a [mergequeue.Provider] backed by a Buzz script. Calls are serialized: one
// VM session answers them all.
type Buzz struct {
	name string
	mu   sync.Mutex
	sess *buzz.Session
	fns  map[string]vm.Value
}

var _ mergequeue.Provider = (*Buzz)(nil)

// Load opens a built-in provider by name ("github") or a script by path.
func Load(ctx context.Context, spec string) (*Buzz, error) {
	if src, ok := Builtin[spec]; ok {
		return Open(ctx, spec, src)
	}
	src, err := os.ReadFile(spec)
	if err != nil {
		return nil, fmt.Errorf("provider %q: not built in, and %w", spec, err)
	}
	return Open(ctx, strings.TrimSuffix(filepath.Base(spec), ".buzz"), string(src))
}

// Open runs source and checks it exports every contract op. The caller owns Close.
func Open(ctx context.Context, name, source string) (*Buzz, error) {
	sess, err := NewSession(ctx)
	if err != nil {
		return nil, err
	}
	if err := sess.Exec(ctx, source); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("provider %q: %w", name, err)
	}
	exports := sess.Exports()
	p := &Buzz{name: name, sess: sess, fns: map[string]vm.Value{}}
	var missing []string
	for _, op := range ops {
		fn, ok := exports[op]
		if !ok || !fn.IsFun() {
			missing = append(missing, op)
			continue
		}
		p.fns[op] = fn
	}
	if len(missing) > 0 {
		_ = sess.Close()
		return nil, fmt.Errorf("provider %q does not export %s", name, strings.Join(missing, ", "))
	}
	return p, nil
}

// NewSession is a session with the modules a provider script may import. Exposed so a
// provider's own `test` blocks run against the same surface.
func NewSession(ctx context.Context) (*buzz.Session, error) {
	sess := buzz.NewSession(ctx)
	buzzstd.RegisterWithOutput(sess, os.Stderr)
	if err := sess.Provide(buzz.ModuleEnv{Ctx: ctx, Out: os.Stderr}, hostModule); err != nil {
		_ = sess.Close()
		return nil, err
	}
	return sess, nil
}

// Close releases the VM session.
func (p *Buzz) Close() error { return p.sess.Close() }

func (p *Buzz) Name() string { return p.name }

func (p *Buzz) call(ctx context.Context, op string, params map[string]any) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, err := p.sess.CallValue(ctx, p.fns[op], []vm.Value{toValue(params)})
	if err != nil {
		return nil, fmt.Errorf("provider %q: %s: %w", p.name, op, err)
	}
	return fromValue(v), nil
}

func (p *Buzz) where(op string) string { return fmt.Sprintf("provider %q: %s", p.name, op) }

func changeParams(c mergequeue.Change) map[string]any {
	return map[string]any{
		"id": c.ID, "repo": c.Repo, "head": c.Head, "ref": c.Ref, "branch": c.Branch,
		"base": c.Base, "title": c.Title, "author": c.Author, "fork": c.Fork,
	}
}

func (p *Buzz) List(ctx context.Context, q mergequeue.ListQuery) ([]mergequeue.Change, error) {
	data, err := p.call(ctx, OpList, map[string]any{"base": q.Base, "remote": q.Remote})
	if err != nil {
		return nil, err
	}
	rows, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("%s returned %T, want a list", p.where(OpList), data)
	}
	out := make([]mergequeue.Change, 0, len(rows))
	for i, row := range rows {
		c, err := decodeChange(row, fmt.Sprintf("%s[%d]", p.where(OpList), i))
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// decodeChange refuses a record without an id or head: the queue would stage and post
// against nothing.
func decodeChange(row any, where string) (mergequeue.Change, error) {
	m, ok := row.(map[string]any)
	if !ok {
		return mergequeue.Change{}, fmt.Errorf("%s is %T, want a record", where, row)
	}
	var c mergequeue.Change
	for _, f := range []struct {
		key string
		dst *string
	}{
		{"id", &c.ID}, {"repo", &c.Repo}, {"head", &c.Head}, {"ref", &c.Ref},
		{"branch", &c.Branch}, {"base", &c.Base}, {"title", &c.Title}, {"author", &c.Author},
	} {
		v, err := str(m, f.key, where)
		if err != nil {
			return mergequeue.Change{}, err
		}
		*f.dst = v
	}
	fork, err := boolean(m, "fork", where)
	if err != nil {
		return mergequeue.Change{}, err
	}
	c.Fork = fork
	if c.ID == "" || c.Head == "" {
		return mergequeue.Change{}, fmt.Errorf("%s: a change needs both id and head", where)
	}
	return c, nil
}

func (p *Buzz) ApprovalAt(ctx context.Context, c mergequeue.Change, sha string) (mergequeue.Approval, error) {
	params := changeParams(c)
	params["sha"] = sha
	data, err := p.call(ctx, OpApprovalAt, params)
	if err != nil {
		return mergequeue.Approval{}, err
	}
	where := p.where(OpApprovalAt)
	m, ok := data.(map[string]any)
	if !ok {
		return mergequeue.Approval{}, fmt.Errorf("%s returned %T, want a record", where, data)
	}
	var a mergequeue.Approval
	if a.Approved, err = boolean(m, "approved", where); err != nil {
		return mergequeue.Approval{}, err
	}
	if a.Head, err = str(m, "head", where); err != nil {
		return mergequeue.Approval{}, err
	}
	if a.Required, err = integer(m, "required", where); err != nil {
		return mergequeue.Approval{}, err
	}
	if a.Approvals, err = integer(m, "approvals", where); err != nil {
		return mergequeue.Approval{}, err
	}
	if a.Reason, err = str(m, "reason", where); err != nil {
		return mergequeue.Approval{}, err
	}
	return a, nil
}

func (p *Buzz) PostStatus(ctx context.Context, c mergequeue.Change, sha string, s mergequeue.Status) error {
	params := changeParams(c)
	params["sha"] = sha
	params["context"] = s.Context
	params["state"] = string(s.State)
	params["description"] = s.Description
	return p.acknowledged(ctx, OpPostStatus, params)
}

func (p *Buzz) Merge(ctx context.Context, c mergequeue.Change, sha, message string) error {
	params := changeParams(c)
	params["sha"] = sha
	params["message"] = message
	data, err := p.call(ctx, OpMerge, params)
	if err != nil {
		return err
	}
	where := p.where(OpMerge)
	m, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("%s returned %T, want a record", where, data)
	}
	merged, err := boolean(m, "merged", where)
	if err != nil {
		return err
	}
	if !merged {
		reason, _ := str(m, "reason", where)
		if reason == "" {
			reason = "no reason given"
		}
		return fmt.Errorf("%s: not merged: %s", where, reason)
	}
	return nil
}

func (p *Buzz) KickBack(ctx context.Context, c mergequeue.Change, sha, report string) error {
	params := changeParams(c)
	params["sha"] = sha
	params["body"] = report
	return p.acknowledged(ctx, OpKickBack, params)
}

// acknowledged invokes an op answering a bool, reading anything but true as a refusal:
// a caller told a status posted when it did not would merge around its own gate.
func (p *Buzz) acknowledged(ctx context.Context, op string, params map[string]any) error {
	data, err := p.call(ctx, op, params)
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

func str(m map[string]any, key, where string) (string, error) {
	v, present := m[key]
	if !present || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: field %q is %T, want str", where, key, v)
	}
	return s, nil
}

func boolean(m map[string]any, key, where string) (bool, error) {
	v, present := m[key]
	if !present || v == nil {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s: field %q is %T, want bool", where, key, v)
	}
	return b, nil
}

func integer(m map[string]any, key, where string) (int, error) {
	v, present := m[key]
	if !present || v == nil {
		return 0, nil
	}
	n, ok := v.(int64)
	if !ok {
		return 0, fmt.Errorf("%s: field %q is %T, want int", where, key, v)
	}
	return int(n), nil
}

// toValue converts the plain records the bridge builds into Buzz values.
func toValue(v any) vm.Value {
	switch x := v.(type) {
	case nil:
		return vm.Null
	case bool:
		return vm.BoolValue(x)
	case int:
		return vm.IntValue(int64(x))
	case int64:
		return vm.IntValue(x)
	case string:
		return vm.StrValue(x)
	case []any:
		items := make([]vm.Value, len(x))
		for i, it := range x {
			items[i] = toValue(it)
		}
		return vm.ListValue(items)
	case map[string]string:
		m := vm.NewMap()
		for k, val := range x {
			m.MapSet(k, vm.StrValue(val))
		}
		return m
	case map[string]any:
		m := vm.NewMap()
		for k, val := range x {
			m.MapSet(k, toValue(val))
		}
		return m
	}
	return vm.StrValue(fmt.Sprint(v))
}

// fromValue converts a Buzz value a script returned into plain Go values.
func fromValue(v vm.Value) any {
	if ev, ok := v.EnumValue(); ok {
		v = ev
	}
	switch {
	case v.IsBool():
		return v.AsBool()
	case v.IsInt():
		return v.AsInt()
	case v.IsFloat():
		return v.AsFloat()
	case v.IsStr():
		return v.AsString()
	case v.IsList():
		items := v.ListItems()
		out := make([]any, len(items))
		for i, it := range items {
			out[i] = fromValue(it)
		}
		return out
	case v.IsMap():
		out := map[string]any{}
		for _, k := range v.MapKeys() {
			if mv, ok := v.MapGet(k); ok {
				out[k] = fromValue(mv)
			}
		}
		return out
	case v.IsObject():
		mv, ok := v.MapView()
		if !ok {
			return nil
		}
		return fromValue(mv)
	}
	return nil
}
