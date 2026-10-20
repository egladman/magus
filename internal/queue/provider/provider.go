package provider

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/vm"

	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/interp/bindings/gen/decode"
	"github.com/egladman/magus/internal/interp/bindings/gen/providerops"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
)

//go:embed github.buzz
var githubSource string

// builtin maps each provider compiled into this package to its source.
var builtin = map[string]string{"github": githubSource}

// Script is a [types.Provider] backed by a Buzz script, a [types.ArtifactLister] when
// it exports list_artifacts, and a [types.ReviewDismisser] when it exports reviews and
// dismiss_review. Calls are serialized: one VM session answers them all. The ops and
// their records are decode.QueueFuncs; docs/concepts/merge-queue.md describes them.
type Script struct {
	name string
	mu   sync.Mutex
	sess *buzz.Session
	fns  map[string]vm.Value
}

var (
	_ types.Provider        = (*Script)(nil)
	_ types.ArtifactLister  = (*Script)(nil)
	_ types.ReviewDismisser = (*Script)(nil)
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
		return nil, fmt.Errorf("provider %q: not built in: %w", spec, err)
	}
	return newScript(ctx, strings.TrimSuffix(filepath.Base(spec), ".buzz"), string(src))
}

// newScript runs source and checks it exports every required op: branch protection
// requires the queue's status once it is wired, so a provider that can list changes but
// not merge them would hold every change forever. name labels its errors.
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
	for _, op := range decode.QueueFuncs {
		fn, ok := exports[op.Name]
		switch {
		case ok && fn.IsFun():
			p.fns[op.Name] = fn
		case op.Required:
			missing = append(missing, op.Name)
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
	_, ok := p.fns[providerops.QueueListArtifacts.Name]
	return ok
}

// newSession is a session with the modules a provider script may import.
func newSession(ctx context.Context) (*buzz.Session, error) {
	sess := buzz.NewSession(ctx)
	out := printRecords{ctx: ctx}
	buzzstd.RegisterWithOutput(sess, out)
	if err := sess.Provide(buzz.ModuleEnv{Ctx: ctx, Out: out}, hostModule); err != nil {
		_ = sess.Close()
		return nil, err
	}
	return sess, nil
}

// printRecords carries what a provider script prints onto the log display, one record
// per print, so the verbosity flags and redaction reach it.
type printRecords struct{ ctx context.Context }

func (w printRecords) Write(p []byte) (int, error) {
	slog.InfoContext(w.ctx, strings.TrimSuffix(string(p), "\n"), attr.Notice(""))
	return len(p), nil
}

// Close releases the VM session.
func (p *Script) Close() error { return p.sess.Close() }

// call runs op on p with req and decodes its answer strictly: a member the answer's type
// does not declare, or one of the wrong kind, is an error naming it.
func call[Req, Resp any](ctx context.Context, p *Script, op bindgen.ProviderOp[Req, Resp], req Req) (Resp, error) {
	var zero Resp
	fn, ok := p.fns[op.Name]
	if !ok {
		return zero, fmt.Errorf("provider %q does not export %s", p.name, op.Name)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	v, err := p.sess.CallValue(ctx, fn, []vm.Value{op.Encode(req)})
	if err != nil {
		return zero, fmt.Errorf("%s: %w", p.where(op.Name), err)
	}
	resp, err := op.Decode(v)
	if err != nil {
		// The decoder's path already starts at the op's name.
		return zero, fmt.Errorf("provider %q: %w", p.name, err)
	}
	return resp, nil
}

// acknowledged runs op, reading an Ack that is not ok as a refusal: a caller told a
// status posted when it did not would merge around its own gate.
func acknowledged[Req any](ctx context.Context, p *Script, op bindgen.ProviderOp[Req, magustypes.Ack], req Req) error {
	ack, err := call(ctx, p, op, req)
	if err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("%s: provider refused", p.where(op.Name))
	}
	return nil
}

func (p *Script) where(op string) string { return fmt.Sprintf("provider %q: %s", p.name, op) }

// Describe calls describe. The queue checks what it reports before relying on it.
func (p *Script) Describe(ctx context.Context, q types.ListQuery) (types.Capabilities, error) {
	op := providerops.QueueDescribe
	r, err := call(ctx, p, op, types.DescribeRequest{
		Base: q.Base, RemoteURL: q.RemoteURL, StatusContext: q.StatusContext, App: q.App, SetupSteps: q.SetupSteps,
	})
	if err != nil {
		return types.Capabilities{}, err
	}
	if (r.Refused == nil) == (r.Capabilities == nil) {
		return types.Capabilities{}, fmt.Errorf("%s: answer exactly one of capabilities and refused", p.where(op.Name))
	}
	if e := r.Refused; e != nil {
		if e.Reason == "" || e.URL == "" || e.App == "" {
			return types.Capabilities{}, fmt.Errorf("%s: field %q needs a reason, a URL and an app", p.where(op.Name), "refused")
		}
		return types.Capabilities{}, e
	}
	c := *r.Capabilities
	if (c.Committer.Name == "") != (c.Committer.Email == "") {
		return types.Capabilities{}, fmt.Errorf("%s: field %q needs a name and an email", p.where(op.Name), "committer")
	}
	if c.Setup != nil {
		if err := c.Setup.Check(); err != nil {
			return types.Capabilities{}, fmt.Errorf("%s: setup: %w", p.where(op.Name), err)
		}
	}
	return c, nil
}

// ListChanges calls list_changes and checks every change it returns: the queue would
// build and post against nothing, or hand the version control an option.
func (p *Script) ListChanges(ctx context.Context, q types.ListQuery) (types.Changes, error) {
	op := providerops.QueueListChanges
	out, err := call(ctx, p, op, types.ListChangesRequest{Base: q.Base, RemoteURL: q.RemoteURL, Only: q.Only})
	if err != nil {
		return types.Changes{}, err
	}
	out.Schema, out.Base, out.RemoteURL = types.SchemaChanges, q.Base, q.RemoteURL
	for _, c := range out.Changes {
		if err := c.Check(); err != nil {
			return types.Changes{}, fmt.Errorf("%s: %w", p.where(op.Name), err)
		}
	}
	// The same checks the document gets when it is read back, here where the script
	// that broke them can be named.
	if err := out.Check(); err != nil {
		return types.Changes{}, fmt.Errorf("%s: %s: %w", p.where(op.Name), types.SchemaChanges, err)
	}
	return out, nil
}

// ApprovalAt calls approval_at. An answer whose head is not a valid change head is an
// error.
func (p *Script) ApprovalAt(ctx context.Context, c types.Change, commit string) (types.Approval, error) {
	op := providerops.QueueApprovalAt
	a, err := call(ctx, p, op, types.ApprovalRequest{Change: c, Commit: commit})
	if err != nil {
		return types.Approval{}, err
	}
	moved := c
	moved.Head = a.Head
	if err := moved.Check(); err != nil {
		return types.Approval{}, fmt.Errorf("%s: head: %w", p.where(op.Name), err)
	}
	return a, nil
}

// RequiredChecks calls required_checks, and answers none when the script does not
// export it. A state outside pending, success and failure is an error.
func (p *Script) RequiredChecks(ctx context.Context, c types.Change, commit string) ([]types.Check, error) {
	op := providerops.QueueRequiredChecks
	if _, ok := p.fns[op.Name]; !ok {
		return nil, nil
	}
	checks, err := call(ctx, p, op, types.RequiredChecksRequest{Change: c, Commit: commit})
	if err != nil {
		return nil, err
	}
	for _, ch := range checks {
		switch ch.State {
		case types.StatePending, types.StateSuccess, types.StateFailure:
		default:
			return nil, fmt.Errorf("%s: check %q has state %q, want pending, success or failure", p.where(op.Name), ch.Name, ch.State)
		}
	}
	return checks, nil
}

// ListGreen calls list_green. A change whose id or head git could read as something
// else is an error, since the queue posts a status on that head.
func (p *Script) ListGreen(ctx context.Context, q types.ListQuery, statusContext string) ([]types.GreenChange, error) {
	op := providerops.QueueListGreen
	green, err := call(ctx, p, op, types.ListGreenRequest{Base: q.Base, RemoteURL: q.RemoteURL, Context: statusContext})
	if err != nil {
		return nil, err
	}
	for i, g := range green {
		if err := types.CheckID(g.ID); err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", p.where(op.Name), i, err)
		}
		if !types.IsObjectID(g.Head) {
			return nil, fmt.Errorf("%s[%d]: head %q is not a full commit id", p.where(op.Name), i, g.Head)
		}
	}
	return green, nil
}

// PostStatus calls post_status.
func (p *Script) PostStatus(ctx context.Context, c types.Change, commit string, s types.CommitStatus) error {
	return acknowledged(ctx, p, providerops.QueuePostStatus, types.StatusRequest{Change: c, CommitStatus: s, Commit: commit})
}

// Retarget calls retarget.
func (p *Script) Retarget(ctx context.Context, c types.Change, base string) error {
	return acknowledged(ctx, p, providerops.QueueRetarget, types.RetargetRequest{Change: c, To: base})
}

// MergeChange calls merge_change. An answer that leaves ByProvider false says the
// queue's call merged the change.
func (p *Script) MergeChange(ctx context.Context, c types.Change, opts types.MergeOptions) (types.MergeResult, error) {
	op := providerops.QueueMergeChange
	a, err := call(ctx, p, op, types.MergeRequest{Change: c, MergeOptions: opts})
	if err != nil {
		return types.MergeResult{}, err
	}
	if !a.Merged {
		if a.Reason == "" {
			return types.MergeResult{}, fmt.Errorf("%s: not merged, and the provider gave no reason", p.where(op.Name))
		}
		return types.MergeResult{}, fmt.Errorf("%s: not merged: %s", p.where(op.Name), a.Reason)
	}
	return types.MergeResult{ByProvider: a.ByProvider}, nil
}

// KickBack calls kick_back.
func (p *Script) KickBack(ctx context.Context, c types.Change, commit string, k types.Kick) error {
	return acknowledged(ctx, p, providerops.QueueKickBack, types.KickBackRequest{Change: c, Kick: k, Commit: commit})
}

// Mark calls mark. A mark outside the set is refused before the script sees it.
func (p *Script) Mark(ctx context.Context, c types.Change, m types.Mark) error {
	op := providerops.QueueMark
	if !m.Valid() {
		return fmt.Errorf("%s: mark %q, want queued, kicked_back, needs_regeneration or none", p.where(op.Name), m)
	}
	return acknowledged(ctx, p, op, types.MarkRequest{Change: c, Mark: m})
}

// ListArtifacts calls list_artifacts.
func (p *Script) ListArtifacts(ctx context.Context, source string) (types.ArtifactListing, error) {
	return call(ctx, p, providerops.QueueListArtifacts, types.ListArtifactsRequest{Source: source})
}

// Reviews calls reviews. A head or an approved commit that is not a full commit id, or
// a review without an id, is an error: the queue would classify or dismiss the wrong
// thing.
func (p *Script) Reviews(ctx context.Context, c types.Change) (types.Reviews, error) {
	op := providerops.QueueReviews
	out, err := call(ctx, p, op, types.ReviewsRequest{Change: c})
	if err != nil {
		return types.Reviews{}, err
	}
	if !types.IsObjectID(out.Head) {
		return types.Reviews{}, fmt.Errorf("%s: head %q is not a full commit id", p.where(op.Name), out.Head)
	}
	for i, rv := range out.Approving {
		switch {
		case rv.ID == "":
			return types.Reviews{}, fmt.Errorf("%s: approving[%d]: a review by %q has no id", p.where(op.Name), i, rv.Reviewer)
		case !types.IsObjectID(rv.Commit):
			return types.Reviews{}, fmt.Errorf("%s: approving[%d]: review %s: commit %q is not a full commit id", p.where(op.Name), i, rv.ID, rv.Commit)
		}
	}
	return out, nil
}

// DismissReview calls dismiss_review.
func (p *Script) DismissReview(ctx context.Context, c types.Change, rv types.Review, message string) error {
	return acknowledged(ctx, p, providerops.QueueDismissReview, types.DismissReviewRequest{Change: c, Review: rv, Message: message})
}
