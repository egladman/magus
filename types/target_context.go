package types

import "context"

// ContextParamAnnot is the exact parameter type annotation that marks a ctx-form
// target: an exported function is a target if and only if its FIRST parameter
// carries this annotation. Buzz namespaces a qualified type with a backslash
// (`serialize\Boxed`), not a dot, so the magus context type is spelled
// `magus\Context` in a magusfile; a dotted `magus.Context` is not valid Buzz type
// syntax (the parser stops at the dot). Recognition keys on this raw annotation
// string, independent of whether the checker can resolve the type (it treats an
// unknown qualified name permissively).
const ContextParamAnnot = `magus\Context`

// IntendedAction is one side-effecting action a target body attempted while it
// ran in discovery (or dry-run) mode: the tool and argv that WOULD have run, plus
// the working directory. The exec choke point records it instead of executing, so
// a later dry-run/preview can show "would run: go build ..." without touching the
// system.
type IntendedAction struct {
	Tool string
	Args []string
	Dir  string
}

// DiscoveryRecord accumulates what a target declared, and what it would have done,
// during a discovery-mode evaluation of its body. The declaration fields
// (Needs/Inputs/Outputs/Charms/...) are written by the magus.Context methods as
// they RECORD instead of dispatch; Actions is written by the side-effect choke
// point (run.Exec) as record-then-skip. A discovery run assembles the target's
// graph node from the declarations, while the recorded intended actions are the
// raw material a later dry-run/playground preview reads.
//
// A DiscoveryRecord is written from a single goroutine: discovery evaluates one
// target body at a time and a discovered body performs no fan-out (ctx.needs only
// records), so no locking is needed.
type DiscoveryRecord struct {
	Needs      []string
	CrossDeps  []CrossTargetRef
	Inputs     []string
	Outputs    []string
	Charms     []string
	SkipCache  bool
	Exclusive  bool
	Slots      int
	Actions    []IntendedAction
}

// RecordAction appends the intended (but skipped) side-effecting action. Nil-safe
// so the choke point can call it unconditionally after a nil check on the record.
func (r *DiscoveryRecord) RecordAction(tool string, args []string, dir string) {
	if r == nil {
		return
	}
	r.Actions = append(r.Actions, IntendedAction{Tool: tool, Args: append([]string(nil), args...), Dir: dir})
}

type discoveryKey struct{}

// WithDiscovery marks ctx as a discovery-mode evaluation carrying rec. Discovery
// is a superset of dry-run tracing: it also sets WithTrace, so every effectful
// host op already gated on Tracing (exec, fs writes, http requests, env mutation)
// no-ops during discovery, and run.Exec additionally records the intended action
// onto rec (record-then-skip). The magus.Context methods detect discovery via
// DiscoveryFromContext and RECORD their declarations onto rec instead of running
// dependencies or executing work.
func WithDiscovery(ctx context.Context, rec *DiscoveryRecord) context.Context {
	ctx = WithTrace(ctx)
	return context.WithValue(ctx, discoveryKey{}, rec)
}

// DiscoveryFromContext returns the discovery record ctx carries, or nil when ctx
// is not a discovery-mode context (the ordinary run path).
func DiscoveryFromContext(ctx context.Context) *DiscoveryRecord {
	r, _ := ctx.Value(discoveryKey{}).(*DiscoveryRecord)
	return r
}
