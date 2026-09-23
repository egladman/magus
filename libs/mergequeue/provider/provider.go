// Package provider runs a merge-queue [mergequeue.Provider] written in Buzz, on an
// embedded gopherbuzz VM.
//
// A provider script exports five functions, each taking one record and returning one:
//
//	list_changes({base, remote})                                > [change]
//	approval_at(change + {commit})                              > {approved, head, reason}
//	post_status(change + {commit, context, state, description}) > bool
//	merge_change(change + {commit, message})                    > {merged, reason}
//	kick_back(change + {commit, report})                        > bool
//
// A change record carries the fields of [mergequeue.Change]; a returned record's other
// keys are ignored. The two reads run in planning and apply; the three writes run
// only in apply, so a script should read its write credential under its own name,
// letting a job that does not hold it fail rather than write.
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

// builtin maps each provider compiled into this package to its source.
var builtin = map[string]string{"github": githubSource}

// Contract op names.
const (
	opListChanges = "list_changes"
	opApprovalAt  = "approval_at"
	opPostStatus  = "post_status"
	opMergeChange = "merge_change"
	opKickBack    = "kick_back"
)

// Every op is required: branch protection requires the queue's status once it is
// wired, so a provider that can list changes but not merge them would hold every
// change forever.
var ops = []string{opListChanges, opApprovalAt, opPostStatus, opMergeChange, opKickBack}

// Script is a [mergequeue.Provider] backed by a Buzz script. Calls are serialized: one
// VM session answers them all.
type Script struct {
	name string
	mu   sync.Mutex
	sess *buzz.Session
	fns  map[string]vm.Value
}

var _ mergequeue.Provider = (*Script)(nil)

// IsBuiltin reports whether [Open] reads spec as a built-in provider's name rather than
// a script's path, which is what a caller resolving relative paths needs to know.
func IsBuiltin(spec string) bool {
	_, ok := builtin[spec]
	return ok
}

// Open opens a built-in provider by name ("github") or a script by path. The caller
// owns Close.
func Open(ctx context.Context, spec string) (*Script, error) {
	if src, ok := builtin[spec]; ok {
		return New(ctx, spec, src)
	}
	src, err := os.ReadFile(spec)
	if err != nil {
		return nil, fmt.Errorf("provider %q: not built in, and %w", spec, err)
	}
	return New(ctx, strings.TrimSuffix(filepath.Base(spec), ".buzz"), string(src))
}

// New runs source and checks it exports every contract op. name labels its errors. The
// caller owns Close.
func New(ctx context.Context, name, source string) (*Script, error) {
	sess, err := newSession(ctx)
	if err != nil {
		return nil, err
	}
	if err := sess.Exec(ctx, source); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("provider %q: %w", name, err)
	}
	exports := sess.Exports()
	p := &Script{name: name, sess: sess, fns: map[string]vm.Value{}}
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

// newSession is a session with the modules a provider script may import.
func newSession(ctx context.Context) (*buzz.Session, error) {
	sess := buzz.NewSession(ctx)
	buzzstd.RegisterWithOutput(sess, os.Stderr)
	if err := sess.Provide(buzz.ModuleEnv{Ctx: ctx, Out: os.Stderr}, hostModule); err != nil {
		_ = sess.Close()
		return nil, err
	}
	return sess, nil
}

// Close releases the VM session.
func (p *Script) Close() error { return p.sess.Close() }

func (p *Script) call(ctx context.Context, op string, params map[string]any) (any, error) {
	arg, err := toValue(params)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.where(op), err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	v, err := p.sess.CallValue(ctx, p.fns[op], []vm.Value{arg})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.where(op), err)
	}
	return fromValue(v), nil
}

func (p *Script) where(op string) string { return fmt.Sprintf("provider %q: %s", p.name, op) }

func changeParams(c mergequeue.Change) map[string]any {
	return map[string]any{
		"id": c.ID, "repo": c.Repo, "head": c.Head, "ref": c.Ref, "branch": c.Branch,
		"base": c.Base, "title": c.Title, "author": c.Author, "fork": c.Fork,
	}
}

// ListChanges calls list_changes and checks every change it returns.
func (p *Script) ListChanges(ctx context.Context, q mergequeue.ListQuery) ([]mergequeue.Change, error) {
	data, err := p.call(ctx, opListChanges, map[string]any{"base": q.Base, "remote": q.Remote})
	if err != nil {
		return nil, err
	}
	rows, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("%s returned %T, want a list", p.where(opListChanges), data)
	}
	out := make([]mergequeue.Change, 0, len(rows))
	for i, row := range rows {
		c, err := decodeChange(row, fmt.Sprintf("%s[%d]", p.where(opListChanges), i))
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// decodeChange refuses a record [mergequeue.Change.Check] refuses: the queue would stage
// and post against nothing, or hand git an option.
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
	if err := c.Check(); err != nil {
		return mergequeue.Change{}, fmt.Errorf("%s: %w", where, err)
	}
	return c, nil
}

// ApprovalAt calls approval_at. A record without a head is an error.
func (p *Script) ApprovalAt(ctx context.Context, c mergequeue.Change, commit string) (mergequeue.Approval, error) {
	params := changeParams(c)
	params["commit"] = commit
	data, err := p.call(ctx, opApprovalAt, params)
	if err != nil {
		return mergequeue.Approval{}, err
	}
	where := p.where(opApprovalAt)
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
	if a.Reason, err = str(m, "reason", where); err != nil {
		return mergequeue.Approval{}, err
	}
	moved := c
	moved.Head = a.Head
	if err := moved.Check(); err != nil {
		return mergequeue.Approval{}, fmt.Errorf("%s: head: %w", where, err)
	}
	return a, nil
}

// PostStatus calls post_status.
func (p *Script) PostStatus(ctx context.Context, c mergequeue.Change, commit string, s mergequeue.CommitStatus) error {
	params := changeParams(c)
	params["commit"] = commit
	params["context"] = s.Context
	params["state"] = string(s.State)
	params["description"] = s.Description
	return p.acknowledged(ctx, opPostStatus, params)
}

// MergeChange calls merge_change.
func (p *Script) MergeChange(ctx context.Context, c mergequeue.Change, commit, message string) error {
	params := changeParams(c)
	params["commit"] = commit
	params["message"] = message
	data, err := p.call(ctx, opMergeChange, params)
	if err != nil {
		return err
	}
	where := p.where(opMergeChange)
	m, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("%s returned %T, want a record", where, data)
	}
	merged, err := boolean(m, "merged", where)
	if err != nil {
		return err
	}
	if !merged {
		reason, err := str(m, "reason", where)
		if err != nil {
			return err
		}
		if reason == "" {
			reason = "no reason given"
		}
		return fmt.Errorf("%s: not merged: %s", where, reason)
	}
	return nil
}

// KickBack calls kick_back.
func (p *Script) KickBack(ctx context.Context, c mergequeue.Change, commit, report string) error {
	params := changeParams(c)
	params["commit"] = commit
	params["report"] = report
	return p.acknowledged(ctx, opKickBack, params)
}

// acknowledged invokes an op answering a bool, reading anything but true as a refusal:
// a caller told a status posted when it did not would merge around its own gate.
func (p *Script) acknowledged(ctx context.Context, op string, params map[string]any) error {
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

// toValue converts the records the bridge builds, whose values are strings and bools,
// into Buzz values.
func toValue(params map[string]any) (vm.Value, error) {
	m := vm.NewMap()
	for k, v := range params {
		switch x := v.(type) {
		case string:
			m.MapSet(k, vm.StrValue(x))
		case bool:
			m.MapSet(k, vm.BoolValue(x))
		default:
			return vm.Null, fmt.Errorf("field %q is %T, which the bridge does not pass", k, v)
		}
	}
	return m, nil
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
