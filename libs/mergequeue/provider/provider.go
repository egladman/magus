// Package provider runs a merge-queue [mergequeue.Provider] written in Buzz, on an
// embedded gopherbuzz VM.
//
// A provider script exports these functions, each taking one record and returning one:
//
//	describe({base, remote})                                   > {stack_merge, linear_stacks, methods}
//	list_changes({base, remote})                               > {changes: [change], landed: [landed]}
//	approval_at(change + {commit})                             > {approved, head, reason, base, method, approved_at}
//	post_status(change + {commit, context, state, description}) > bool
//	retarget(change + {base})                                  > bool
//	merge_change(change + {commit, message, through})          > {merged, reason}
//	kick_back(change + {commit, code, report, paths, with, candidate}) > bool
//	run_artifacts({run})                                       > {completed, headers, artifacts: [{name, url}]}
//
// Every op but run_artifacts is required; run_artifacts is required of a provider apply
// follows a validation run through. A change record carries the fields of
// [mergequeue.Change]; a landed record those of [mergequeue.Landed]; a returned
// record's other keys are ignored. The reads run in planning and apply; the writes run
// only in apply, so a script should read its write credential under its own name,
// letting a job that does not hold it fail rather than write.
//
// The records a script receives hold strings, bools, and lists of strings (paths, with).
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
	"slices"
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
	opDescribe     = "describe"
	opListChanges  = "list_changes"
	opApprovalAt   = "approval_at"
	opPostStatus   = "post_status"
	opRetarget     = "retarget"
	opMergeChange  = "merge_change"
	opKickBack     = "kick_back"
	opRunArtifacts = "run_artifacts"
)

// Every op but run_artifacts is required: branch protection requires the queue's status once it is
// wired, so a provider that can list changes but not merge them would hold every change
// forever.
var ops = []string{opDescribe, opListChanges, opApprovalAt, opPostStatus, opRetarget, opMergeChange, opKickBack}

// Script is a [mergequeue.Provider] backed by a Buzz script, and a
// [mergequeue.RunReader] when it exports run_artifacts. Calls are serialized: one VM
// session answers them all.
type Script struct {
	name string
	mu   sync.Mutex
	sess *buzz.Session
	fns  map[string]vm.Value
}

var (
	_ mergequeue.Provider  = (*Script)(nil)
	_ mergequeue.RunReader = (*Script)(nil)
)

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

// New runs source and checks it exports every required op. name labels its errors. The
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
	for _, op := range slices.Concat(ops, []string{opRunArtifacts}) {
		fn, ok := exports[op]
		switch {
		case ok && fn.IsFun():
			p.fns[op] = fn
		case op != opRunArtifacts:
			missing = append(missing, op)
		}
	}
	if len(missing) > 0 {
		_ = sess.Close()
		return nil, fmt.Errorf("provider %q does not export %s", name, strings.Join(missing, ", "))
	}
	return p, nil
}

// ReadsRuns reports whether the script exports run_artifacts, which following a
// validation run needs.
func (p *Script) ReadsRuns() bool {
	_, ok := p.fns[opRunArtifacts]
	return ok
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
	fn, ok := p.fns[op]
	if !ok {
		return nil, fmt.Errorf("provider %q does not export %s", p.name, op)
	}
	arg, err := toValue(params)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.where(op), err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	v, err := p.sess.CallValue(ctx, fn, []vm.Value{arg})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.where(op), err)
	}
	return fromValue(v), nil
}

// callRecord invokes an op answering one record.
func (p *Script) callRecord(ctx context.Context, op string, params map[string]any) (map[string]any, error) {
	data, err := p.call(ctx, op, params)
	if err != nil {
		return nil, err
	}
	m, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s returned %T, want a record", p.where(op), data)
	}
	return m, nil
}

func (p *Script) where(op string) string { return fmt.Sprintf("provider %q: %s", p.name, op) }

func changeParams(c mergequeue.Change) map[string]any {
	return map[string]any{
		"id": c.ID, "repo": c.Repo, "head": c.Head, "ref": c.Ref, "branch": c.Branch,
		"base": c.Base, "title": c.Title, "author": c.Author, "fork": c.Fork,
		"method": string(c.Method), "parent": c.Parent,
	}
}

// Describe calls describe and checks the capabilities it reports.
func (p *Script) Describe(ctx context.Context, q mergequeue.ListQuery) (mergequeue.Capabilities, error) {
	m, err := p.callRecord(ctx, opDescribe, map[string]any{"base": q.Base, "remote": q.Remote})
	if err != nil {
		return mergequeue.Capabilities{}, err
	}
	where := p.where(opDescribe)
	var c mergequeue.Capabilities
	sm, err := str(m, "stack_merge", where)
	if err != nil {
		return mergequeue.Capabilities{}, err
	}
	c.StackMerge = mergequeue.StackMerge(sm)
	if c.LinearStacks, err = boolean(m, "linear_stacks", where); err != nil {
		return mergequeue.Capabilities{}, err
	}
	methods, err := strs(m, "methods", where)
	if err != nil {
		return mergequeue.Capabilities{}, err
	}
	for _, s := range methods {
		c.Methods = append(c.Methods, mergequeue.MergeMethod(s))
	}
	return c, nil
}

// ListChanges calls list_changes and checks every record it returns.
func (p *Script) ListChanges(ctx context.Context, q mergequeue.ListQuery) (mergequeue.Changes, error) {
	m, err := p.callRecord(ctx, opListChanges, map[string]any{"base": q.Base, "remote": q.Remote})
	if err != nil {
		return mergequeue.Changes{}, err
	}
	where := p.where(opListChanges)
	out := mergequeue.Changes{Schema: mergequeue.SchemaChanges, Base: q.Base, Remote: q.Remote}
	changes, err := records(m, "changes", where)
	if err != nil {
		return mergequeue.Changes{}, err
	}
	for i, row := range changes {
		c, err := decodeChange(row, fmt.Sprintf("%s: changes[%d]", where, i))
		if err != nil {
			return mergequeue.Changes{}, err
		}
		out.Changes = append(out.Changes, c)
	}
	landed, err := records(m, "landed", where)
	if err != nil {
		return mergequeue.Changes{}, err
	}
	for i, row := range landed {
		l, err := decodeLanded(row, fmt.Sprintf("%s: landed[%d]", where, i))
		if err != nil {
			return mergequeue.Changes{}, err
		}
		out.Landed = append(out.Landed, l)
	}
	return out, nil
}

// decodeChange refuses a record [mergequeue.Change.Check] refuses: the queue would build
// and post against nothing, or hand the version control an option.
func decodeChange(m map[string]any, where string) (mergequeue.Change, error) {
	var c mergequeue.Change
	var method string
	for _, f := range []struct {
		key string
		dst *string
	}{
		{"id", &c.ID}, {"repo", &c.Repo}, {"head", &c.Head}, {"ref", &c.Ref}, {"branch", &c.Branch},
		{"base", &c.Base}, {"title", &c.Title}, {"author", &c.Author}, {"method", &method}, {"parent", &c.Parent},
	} {
		v, err := str(m, f.key, where)
		if err != nil {
			return mergequeue.Change{}, err
		}
		*f.dst = v
	}
	c.Method = mergequeue.MergeMethod(method)
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

func decodeLanded(m map[string]any, where string) (mergequeue.Landed, error) {
	var l mergequeue.Landed
	var method string
	for _, f := range []struct {
		key string
		dst *string
	}{{"id", &l.ID}, {"head", &l.Head}, {"commit", &l.Commit}, {"method", &method}} {
		v, err := str(m, f.key, where)
		if err != nil {
			return mergequeue.Landed{}, err
		}
		*f.dst = v
	}
	l.Method = mergequeue.MergeMethod(method)
	return l, nil
}

// ApprovalAt calls approval_at. A record without a head is an error.
func (p *Script) ApprovalAt(ctx context.Context, c mergequeue.Change, commit string) (mergequeue.Approval, error) {
	params := changeParams(c)
	params["commit"] = commit
	m, err := p.callRecord(ctx, opApprovalAt, params)
	if err != nil {
		return mergequeue.Approval{}, err
	}
	where := p.where(opApprovalAt)
	var a mergequeue.Approval
	if a.Approved, err = boolean(m, "approved", where); err != nil {
		return mergequeue.Approval{}, err
	}
	var method string
	for _, f := range []struct {
		key string
		dst *string
	}{{"head", &a.Head}, {"reason", &a.Reason}, {"base", &a.Base}, {"method", &method}, {"approved_at", &a.ApprovedAt}} {
		if *f.dst, err = str(m, f.key, where); err != nil {
			return mergequeue.Approval{}, err
		}
	}
	a.Method = mergequeue.MergeMethod(method)
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

// Retarget calls retarget.
func (p *Script) Retarget(ctx context.Context, c mergequeue.Change, base string) error {
	params := changeParams(c)
	params["base"] = base
	return p.acknowledged(ctx, opRetarget, params)
}

// MergeChange calls merge_change.
func (p *Script) MergeChange(ctx context.Context, c mergequeue.Change, req mergequeue.MergeRequest) error {
	params := changeParams(c)
	params["commit"] = req.Commit
	params["message"] = req.Message
	params["through"] = req.Through
	m, err := p.callRecord(ctx, opMergeChange, params)
	if err != nil {
		return err
	}
	where := p.where(opMergeChange)
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
func (p *Script) KickBack(ctx context.Context, c mergequeue.Change, commit string, k mergequeue.Kick) error {
	params := changeParams(c)
	params["commit"] = commit
	params["code"] = string(k.Code)
	params["report"] = k.Report
	params["paths"] = k.Paths
	params["with"] = k.With
	params["candidate"] = k.Candidate
	return p.acknowledged(ctx, opKickBack, params)
}

// RunArtifacts calls run_artifacts.
func (p *Script) RunArtifacts(ctx context.Context, run string) (mergequeue.RunArtifacts, error) {
	m, err := p.callRecord(ctx, opRunArtifacts, map[string]any{"run": run})
	if err != nil {
		return mergequeue.RunArtifacts{}, err
	}
	where := p.where(opRunArtifacts)
	var out mergequeue.RunArtifacts
	if out.Completed, err = boolean(m, "completed", where); err != nil {
		return mergequeue.RunArtifacts{}, err
	}
	switch h := m["headers"].(type) {
	case nil:
	case map[string]any:
		out.Headers = make(map[string]string, len(h))
		for k, v := range h {
			s, ok := v.(string)
			if !ok {
				return mergequeue.RunArtifacts{}, fmt.Errorf("%s: header %q is %T, want str", where, k, v)
			}
			out.Headers[k] = s
		}
	default:
		return mergequeue.RunArtifacts{}, fmt.Errorf("%s: field \"headers\" is %T, want a record", where, h)
	}
	rows, err := records(m, "artifacts", where)
	if err != nil {
		return mergequeue.RunArtifacts{}, err
	}
	for i, row := range rows {
		at := fmt.Sprintf("%s: artifacts[%d]", where, i)
		var a mergequeue.Artifact
		if a.Name, err = str(row, "name", at); err != nil {
			return mergequeue.RunArtifacts{}, err
		}
		if a.URL, err = str(row, "url", at); err != nil {
			return mergequeue.RunArtifacts{}, err
		}
		out.Artifacts = append(out.Artifacts, a)
	}
	return out, nil
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

func strs(m map[string]any, key, where string) ([]string, error) {
	v, present := m[key]
	if !present || v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: field %q is %T, want [str]", where, key, v)
	}
	out := make([]string, len(items))
	for i, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %s[%d] is %T, want str", where, key, i, it)
		}
		out[i] = s
	}
	return out, nil
}

func records(m map[string]any, key, where string) ([]map[string]any, error) {
	v, present := m[key]
	if !present || v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: field %q is %T, want a list", where, key, v)
	}
	out := make([]map[string]any, len(items))
	for i, it := range items {
		r, ok := it.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: %s[%d] is %T, want a record", where, key, i, it)
		}
		out[i] = r
	}
	return out, nil
}

// toValue converts the records the bridge builds, whose values are strings, bools and
// lists of strings, into Buzz values.
func toValue(params map[string]any) (vm.Value, error) {
	m := vm.NewMap()
	for k, v := range params {
		switch x := v.(type) {
		case string:
			m.MapSet(k, vm.StrValue(x))
		case bool:
			m.MapSet(k, vm.BoolValue(x))
		case []string:
			items := make([]vm.Value, len(x))
			for i, s := range x {
				items[i] = vm.StrValue(s)
			}
			m.MapSet(k, vm.ListValue(items))
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
