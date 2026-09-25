// Package provider runs a merge-queue [types.Provider] written in Buzz, on an
// embedded gopherbuzz VM.
//
// A provider script exports these functions, each taking one record and returning one:
//
//	describe({base, remote_url, status_context, app, setup_steps}) > {stack_merge, linear_stacks, methods, queue_label?, committer?, setup?}
//	list_changes({base, remote_url})               > {changes: [change], merged: [merged], unqueued: [{id, head, repo?, mark?}]}
//	approval_at(change + {commit})                 > {approved, head, base, method, queued, shared_with, reason?, approved_commit?}
//	list_green({base, remote_url, context})        > {changes: [{id, repo, head}]}
//	post_status(change + {commit, context, state, description}) > bool
//	retarget(change + {base})                      > bool
//	merge_change(change + {commit, message, through: [{id, commit}]}) > {merged, by_provider?, reason?}
//	kick_back(change + {commit, code, report, paths, with, candidate_commit}) > bool
//	mark(change + {mark})                          > bool
//	list_artifacts({source})                       > {run, complete, artifacts: [{name, url}], headers?}
//
// Every op but list_artifacts is required; list_artifacts is required of a provider
// apply follows a validation run through. A change record carries the fields of
// [types.Change], a merged record those of [types.MergedChange] and an
// unqueued record those of [types.UnqueuedChange]. describe's setup, asked for with a
// status_context, carries [types.Setup] as status_context, credential {id, name?},
// required_checks [{context, integration?, events?}], settings [{name, value, want}],
// app? {slug, id, client_id?, registration_url?, install_url?, environment?, variable?,
// secret?} and steps [{title, command? or url?}]. list_artifacts' run carries
// [types.RunOrigin] as repo, head_repo, head_branch, event, branch_event and
// definition. Every key the contract lists
// without a "?" is required: a missing one is an error, never a zero value, since a
// missing "fork" or "queued" read as false would admit what the provider meant to
// refuse. Other keys a record carries are ignored. The reads run in planning and apply;
// the writes run only in apply, so a script should read its write credential under its
// own name, letting a job that does not hold it fail rather than write.
//
// The records a script receives hold strings, bools, lists of strings (paths, with) and
// lists of records (through). Scripts see Buzz's standard library (std, os, serialize,
// ...) and one host module, "mergequeue", whose request(method, url, body, headers)
// makes an HTTP request and returns {status, body}.
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

	"github.com/egladman/magus/libs/mergequeue/types"
)

//go:embed github.buzz
var githubSource string

// builtin maps each provider compiled into this package to its source.
var builtin = map[string]string{"github": githubSource}

// Contract op names.
const (
	opDescribe      = "describe"
	opListChanges   = "list_changes"
	opApprovalAt    = "approval_at"
	opListGreen     = "list_green"
	opPostStatus    = "post_status"
	opRetarget      = "retarget"
	opMergeChange   = "merge_change"
	opKickBack      = "kick_back"
	opMark          = "mark"
	opListArtifacts = "list_artifacts"
)

// Every op but list_artifacts is required: branch protection requires the queue's status
// once it is wired, so a provider that can list changes but not merge them would hold
// every change forever.
var ops = []string{opDescribe, opListChanges, opApprovalAt, opListGreen, opPostStatus, opRetarget, opMergeChange, opKickBack, opMark}

// Script is a [types.Provider] backed by a Buzz script, and a
// [types.ArtifactLister] when it exports list_artifacts. Calls are serialized: one
// VM session answers them all.
type Script struct {
	name string
	mu   sync.Mutex
	sess *buzz.Session
	fns  map[string]vm.Value
}

var (
	_ types.Provider       = (*Script)(nil)
	_ types.ArtifactLister = (*Script)(nil)
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
		return newScript(ctx, spec, src)
	}
	src, err := os.ReadFile(spec)
	if err != nil {
		return nil, fmt.Errorf("provider %q: not built in, and %w", spec, err)
	}
	return newScript(ctx, strings.TrimSuffix(filepath.Base(spec), ".buzz"), string(src))
}

// newScript runs source and checks it exports every required op. name labels its
// errors.
func newScript(ctx context.Context, name, source string) (*Script, error) {
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
	for _, op := range slices.Concat(ops, []string{opListArtifacts}) {
		fn, ok := exports[op]
		switch {
		case ok && fn.IsFun():
			p.fns[op] = fn
		case op != opListArtifacts:
			missing = append(missing, op)
		}
	}
	if len(missing) > 0 {
		_ = sess.Close()
		return nil, fmt.Errorf("provider %q does not export %s", name, strings.Join(missing, ", "))
	}
	return p, nil
}

// ListsArtifacts reports whether the script exports list_artifacts, which following a
// validation run needs.
func (p *Script) ListsArtifacts() bool {
	_, ok := p.fns[opListArtifacts]
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
func (p *Script) callRecord(ctx context.Context, op string, params map[string]any) (record, error) {
	data, err := p.call(ctx, op, params)
	if err != nil {
		return record{}, err
	}
	m, ok := data.(map[string]any)
	if !ok {
		return record{}, fmt.Errorf("%s returned %T, want a record", p.where(op), data)
	}
	return record{m: m, where: p.where(op)}, nil
}

func (p *Script) where(op string) string { return fmt.Sprintf("provider %q: %s", p.name, op) }

func changeParams(c types.Change) map[string]any {
	return map[string]any{
		"id": c.ID, "repo": c.Repo, "head": c.Head, "ref": c.Ref, "branch": c.Branch,
		"base": c.Base, "title": c.Title, "fork": c.Fork, "method": string(c.Method), "parent": c.Parent,
	}
}

// Describe calls describe. The queue checks what it reports before relying on it.
func (p *Script) Describe(ctx context.Context, q types.ListQuery) (types.Capabilities, error) {
	r, err := p.callRecord(ctx, opDescribe, map[string]any{
		"base": q.Base, "remote_url": q.RemoteURL, "status_context": q.StatusContext, "app": q.App, "setup_steps": q.SetupSteps,
	})
	if err != nil {
		return types.Capabilities{}, err
	}
	var c types.Capabilities
	var sm string
	var methods []string
	var committer map[string]string
	var setup *record
	if err := r.decode(required("stack_merge", &sm), required("linear_stacks", &c.LinearStacks), required("methods", &methods),
		optional("queue_label", &c.QueueLabel), optional("committer", &committer), optional("setup", &setup)); err != nil {
		return types.Capabilities{}, err
	}
	if setup != nil {
		s, err := decodeSetup(*setup)
		if err != nil {
			return types.Capabilities{}, err
		}
		c.Setup = &s
	}
	if committer != nil {
		c.Committer.Name, c.Committer.Email = committer["name"], committer["email"]
		if c.Committer.Name == "" || c.Committer.Email == "" {
			return types.Capabilities{}, fmt.Errorf("%s: field %q needs a name and an email", r.where, "committer")
		}
	}
	c.StackMerge = types.StackMerge(sm)
	for _, s := range methods {
		c.Methods = append(c.Methods, types.MergeMethod(s))
	}
	return c, nil
}

// decodeSetup reads describe's setup record; every list in it is optional.
func decodeSetup(r record) (types.Setup, error) {
	var s types.Setup
	var credential, app *record
	var checks, settings, steps []record
	if err := r.decode(required("status_context", &s.StatusContext), required("credential", &credential),
		optional("required_checks", &checks), optional("settings", &settings), optional("app", &app), optional("steps", &steps)); err != nil {
		return types.Setup{}, err
	}
	if err := credential.decode(required("id", &s.Credential.ID), optional("name", &s.Credential.Name)); err != nil {
		return types.Setup{}, err
	}
	for _, row := range checks {
		var rc types.RequiredCheck
		if err := row.decode(required("context", &rc.Context), optional("integration", &rc.Integration), optional("events", &rc.Events)); err != nil {
			return types.Setup{}, err
		}
		s.RequiredChecks = append(s.RequiredChecks, rc)
	}
	for _, row := range settings {
		var st types.Setting
		if err := row.decode(required("name", &st.Name), required("value", &st.Value), required("want", &st.Want)); err != nil {
			return types.Setup{}, err
		}
		s.Settings = append(s.Settings, st)
	}
	if app != nil {
		var a types.App
		if err := app.decode(required("slug", &a.Slug), required("id", &a.ID), optional("client_id", &a.ClientID),
			optional("registration_url", &a.RegistrationURL), optional("install_url", &a.InstallURL),
			optional("environment", &a.Environment), optional("variable", &a.Variable), optional("secret", &a.Secret)); err != nil {
			return types.Setup{}, err
		}
		s.App = &a
	}
	for _, row := range steps {
		var st types.SetupStep
		if err := row.decode(required("title", &st.Title), optional("command", &st.Command), optional("url", &st.URL)); err != nil {
			return types.Setup{}, err
		}
		s.Steps = append(s.Steps, st)
	}
	if err := s.Check(); err != nil {
		return types.Setup{}, fmt.Errorf("%s: setup: %w", r.where, err)
	}
	return s, nil
}

// ListChanges calls list_changes and checks every record it returns.
func (p *Script) ListChanges(ctx context.Context, q types.ListQuery) (types.Changes, error) {
	r, err := p.callRecord(ctx, opListChanges, map[string]any{"base": q.Base, "remote_url": q.RemoteURL})
	if err != nil {
		return types.Changes{}, err
	}
	out := types.Changes{Schema: types.SchemaChanges, Base: q.Base, RemoteURL: q.RemoteURL}
	var changes, merged, unqueued []record
	if err := r.decode(required("changes", &changes), required("merged", &merged), required("unqueued", &unqueued)); err != nil {
		return types.Changes{}, err
	}
	for _, row := range changes {
		c, err := decodeChange(row)
		if err != nil {
			return types.Changes{}, err
		}
		out.Changes = append(out.Changes, c)
	}
	for _, row := range merged {
		var m types.MergedChange
		var method string
		if err := row.decode(required("id", &m.ID), required("head", &m.Head), required("commit", &m.Commit), required("method", &method)); err != nil {
			return types.Changes{}, err
		}
		m.Method = types.MergeMethod(method)
		out.Merged = append(out.Merged, m)
	}
	for _, row := range unqueued {
		var u types.UnqueuedChange
		var mark string
		if err := row.decode(required("id", &u.ID), required("head", &u.Head), optional("repo", &u.Repo), optional("mark", &mark)); err != nil {
			return types.Changes{}, err
		}
		u.Mark = types.Mark(mark)
		out.Unqueued = append(out.Unqueued, u)
	}
	// The same checks the document gets when it is read back, here where the script
	// that broke them can be named.
	if err := out.Check(); err != nil {
		return types.Changes{}, fmt.Errorf("%s: %s: %w", r.where, types.SchemaChanges, err)
	}
	return out, nil
}

// decodeChange refuses a record missing a required field, or one [types.Change.Check]
// refuses: the queue would build and post against nothing, or hand the version control
// an option.
func decodeChange(r record) (types.Change, error) {
	var c types.Change
	var method string
	if err := r.decode(
		required("id", &c.ID), required("repo", &c.Repo), required("head", &c.Head), required("base", &c.Base),
		required("method", &method), required("fork", &c.Fork),
		optional("ref", &c.Ref), optional("branch", &c.Branch), optional("title", &c.Title), optional("parent", &c.Parent),
	); err != nil {
		return types.Change{}, err
	}
	c.Method = types.MergeMethod(method)
	if err := c.Check(); err != nil {
		return types.Change{}, fmt.Errorf("%s: %w", r.where, err)
	}
	return c, nil
}

// ApprovalAt calls approval_at. A record without a head is an error.
func (p *Script) ApprovalAt(ctx context.Context, c types.Change, commit string) (types.Approval, error) {
	params := changeParams(c)
	params["commit"] = commit
	r, err := p.callRecord(ctx, opApprovalAt, params)
	if err != nil {
		return types.Approval{}, err
	}
	var a types.Approval
	var method string
	if err := r.decode(
		required("approved", &a.Approved), required("head", &a.Head), required("base", &a.Base), required("method", &method),
		required("queued", &a.Queued), required("shared_with", &a.BranchSharedWith),
		optional("reason", &a.Reason), optional("approved_commit", &a.ApprovedCommit),
	); err != nil {
		return types.Approval{}, err
	}
	a.Method = types.MergeMethod(method)
	moved := c
	moved.Head = a.Head
	if err := moved.Check(); err != nil {
		return types.Approval{}, fmt.Errorf("%s: head: %w", r.where, err)
	}
	return a, nil
}

// ListGreen calls list_green. A record whose id or head git could read as something else
// is an error, since the queue posts a status on that head.
func (p *Script) ListGreen(ctx context.Context, q types.ListQuery, statusContext string) ([]types.GreenChange, error) {
	r, err := p.callRecord(ctx, opListGreen, map[string]any{"base": q.Base, "remote_url": q.RemoteURL, "context": statusContext})
	if err != nil {
		return nil, err
	}
	var rows []record
	if err := r.decode(required("changes", &rows)); err != nil {
		return nil, err
	}
	out := make([]types.GreenChange, 0, len(rows))
	for _, row := range rows {
		var g types.GreenChange
		if err := row.decode(required("id", &g.ID), required("repo", &g.Repo), required("head", &g.Head)); err != nil {
			return nil, err
		}
		if err := types.CheckID(g.ID); err != nil {
			return nil, fmt.Errorf("%s: %w", row.where, err)
		}
		if !types.IsObjectID(g.Head) {
			return nil, fmt.Errorf("%s: head %q is not a full commit id", row.where, g.Head)
		}
		out = append(out, g)
	}
	return out, nil
}

// PostStatus calls post_status.
func (p *Script) PostStatus(ctx context.Context, c types.Change, commit string, s types.CommitStatus) error {
	params := changeParams(c)
	params["commit"] = commit
	params["context"] = s.Context
	params["state"] = string(s.State)
	params["description"] = s.Description
	return p.acknowledged(ctx, opPostStatus, params)
}

// Retarget calls retarget.
func (p *Script) Retarget(ctx context.Context, c types.Change, base string) error {
	params := changeParams(c)
	params["base"] = base
	return p.acknowledged(ctx, opRetarget, params)
}

// MergeChange calls merge_change. A script that omits by_provider says the queue's call
// merged the change.
func (p *Script) MergeChange(ctx context.Context, c types.Change, opts types.MergeOptions) (types.MergeResult, error) {
	params := changeParams(c)
	params["commit"] = opts.Commit
	params["message"] = opts.Message
	through := make([]map[string]string, len(opts.Through))
	for i, pin := range opts.Through {
		through[i] = map[string]string{"id": pin.ID, "commit": pin.Commit}
	}
	params["through"] = through
	r, err := p.callRecord(ctx, opMergeChange, params)
	if err != nil {
		return types.MergeResult{}, err
	}
	var merged bool
	var reason string
	var res types.MergeResult
	if err := r.decode(required("merged", &merged), optional("by_provider", &res.ByProvider), optional("reason", &reason)); err != nil {
		return types.MergeResult{}, err
	}
	if !merged {
		if reason == "" {
			reason = "no reason given"
		}
		return types.MergeResult{}, fmt.Errorf("%s: not merged: %s", r.where, reason)
	}
	return res, nil
}

// KickBack calls kick_back.
func (p *Script) KickBack(ctx context.Context, c types.Change, commit string, k types.Kick) error {
	params := changeParams(c)
	params["commit"] = commit
	params["code"] = string(k.Code)
	params["report"] = k.Report
	params["paths"] = k.Paths
	params["with"] = k.With
	params["candidate_commit"] = k.CandidateCommit
	params["source"] = k.Source
	if k.Reproduce != nil {
		params["reproduce"] = map[string]string{"gate": k.Reproduce.Gate, "regenerate": k.Reproduce.Regenerate}
	}
	return p.acknowledged(ctx, opKickBack, params)
}

// Mark calls mark. A mark outside the three is refused before the script sees it.
func (p *Script) Mark(ctx context.Context, c types.Change, m types.Mark) error {
	if !m.Valid() {
		return fmt.Errorf("%s: mark %q, want queued, rejected or none", p.where(opMark), m)
	}
	params := changeParams(c)
	params["mark"] = string(m)
	return p.acknowledged(ctx, opMark, params)
}

// ListArtifacts calls list_artifacts.
func (p *Script) ListArtifacts(ctx context.Context, source string) (types.ArtifactListing, error) {
	r, err := p.callRecord(ctx, opListArtifacts, map[string]any{"source": source})
	if err != nil {
		return types.ArtifactListing{}, err
	}
	var out types.ArtifactListing
	var rows []record
	var run *record
	if err := r.decode(required("run", &run), required("complete", &out.Complete), required("artifacts", &rows), optional("headers", &out.Headers)); err != nil {
		return types.ArtifactListing{}, err
	}
	o := &out.Run
	if err := run.decode(required("repo", &o.Repo), required("head_repo", &o.HeadRepo), required("head_branch", &o.HeadBranch),
		required("event", &o.Event), required("branch_event", &o.BranchEvent), required("definition", &o.Definition)); err != nil {
		return types.ArtifactListing{}, err
	}
	for _, row := range rows {
		var a types.Artifact
		if err := row.decode(required("name", &a.Name), required("url", &a.URL)); err != nil {
			return types.ArtifactListing{}, err
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
		return fmt.Errorf("%s: provider refused", p.where(op))
	}
	return nil
}

// record is one record a script returned, and where it came from for errors.
type record struct {
	m     map[string]any
	where string
}

// field is one key of a record and where its value goes.
type field struct {
	key      string
	dst      any // *string, *bool, *[]string, *[]record, **record or *map[string]string
	required bool
}

func required(key string, dst any) field { return field{key: key, dst: dst, required: true} }
func optional(key string, dst any) field { return field{key: key, dst: dst} }

// decode reads fields out of r. A required key that is missing or null is an error.
func (r record) decode(fields ...field) error {
	for _, f := range fields {
		v, present := r.m[f.key]
		if !present || v == nil {
			if f.required {
				return fmt.Errorf("%s: field %q is missing", r.where, f.key)
			}
			continue
		}
		if err := r.set(f, v); err != nil {
			return err
		}
	}
	return nil
}

func (r record) set(f field, v any) error {
	wrong := func(want string) error { return fmt.Errorf("%s: field %q is %T, want %s", r.where, f.key, v, want) }
	switch dst := f.dst.(type) {
	case *string:
		s, ok := v.(string)
		if !ok {
			return wrong("str")
		}
		*dst = s
	case *bool:
		b, ok := v.(bool)
		if !ok {
			return wrong("bool")
		}
		*dst = b
	case *[]string:
		items, ok := v.([]any)
		if !ok {
			return wrong("[str]")
		}
		out := make([]string, len(items))
		for i, it := range items {
			s, ok := it.(string)
			if !ok {
				return fmt.Errorf("%s: %s[%d] is %T, want str", r.where, f.key, i, it)
			}
			out[i] = s
		}
		*dst = out
	case *[]record:
		items, ok := v.([]any)
		if !ok {
			return wrong("a list")
		}
		out := make([]record, len(items))
		for i, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: %s[%d] is %T, want a record", r.where, f.key, i, it)
			}
			out[i] = record{m: m, where: fmt.Sprintf("%s: %s[%d]", r.where, f.key, i)}
		}
		*dst = out
	case **record:
		m, ok := v.(map[string]any)
		if !ok {
			return wrong("a record")
		}
		*dst = &record{m: m, where: r.where + ": " + f.key}
	case *map[string]string:
		m, ok := v.(map[string]any)
		if !ok {
			return wrong("a record")
		}
		out := make(map[string]string, len(m))
		for k, x := range m {
			s, ok := x.(string)
			if !ok {
				return fmt.Errorf("%s: %s[%q] is %T, want str", r.where, f.key, k, x)
			}
			out[k] = s
		}
		*dst = out
	default:
		return fmt.Errorf("%s: field %q decodes into %T, which the bridge does not read", r.where, f.key, f.dst)
	}
	return nil
}

// toValue converts the records the bridge builds, whose values are strings, bools, lists
// of strings, string records and lists of them, into Buzz values.
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
		case map[string]string:
			m.MapSet(k, stringRecord(x))
		case []map[string]string:
			items := make([]vm.Value, len(x))
			for i, rec := range x {
				items[i] = stringRecord(rec)
			}
			m.MapSet(k, vm.ListValue(items))
		default:
			return vm.Null, fmt.Errorf("field %q is %T, which the bridge does not pass", k, v)
		}
	}
	return m, nil
}

func stringRecord(rec map[string]string) vm.Value {
	m := vm.NewMap()
	for k, v := range rec {
		m.MapSet(k, vm.StrValue(v))
	}
	return m
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
