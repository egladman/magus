// Package sandbox confines the processes a caller starts to the paths a policy binds:
// read, write and exec, nothing else.
//
// A Policy is built from Binds with New and consulted two ways. Check answers whether
// one access is inside it, for a caller that guards its own file operations. Command
// starts a child the host's kernel holds to it: landlock on Linux, sandbox-exec on
// macOS. The calling process is never confined.
//
// The zero Mode is Required: Command refuses (an *UnconfinedError) on a host that
// cannot confine the child, and WithMode(BestEffort) opts into running it unconfined
// there. Windows has no kernel layer, so Required always refuses there.
//
// The policy holds paths alone. A child keeps the caller's environment, its temp dir
// and the network, and may write any file inside a write Bind: scrubbing the
// environment, a private TMPDIR and refusing writes to files another tool later runs
// code from are the caller's to add.
package sandbox

import (
	"errors"
	"fmt"
	"slices"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

// Bind is one path a policy exposes to a confined child, and the rights it carries.
type Bind struct {
	// Path is resolved through its symlinks when the policy is built, so it names the
	// file the child would touch. A relative path is resolved against the working
	// directory.
	Path string
	// Mode spells the rights: ro, rw, rx or rwx (see filesystem.ModeRule). Empty is ro.
	Mode string
	// Create lets the kernel layer make Path as a directory when it is missing and Mode
	// writes, so a cache the child has not made yet can be bound before it does.
	Create bool
}

// Mode decides a child the host cannot confine.
type Mode int

const (
	// Required refuses to start a child the host cannot confine. The zero Mode.
	Required Mode = iota
	// BestEffort starts it unconfined, under whatever checks the caller runs itself.
	BestEffort
)

// String names m: required or best-effort.
func (m Mode) String() string {
	switch m {
	case Required:
		return "required"
	case BestEffort:
		return "best-effort"
	}
	return fmt.Sprintf("mode(%d)", int(m))
}

// Policy is the compiled form of a set of Binds. It is immutable once built and safe
// for concurrent use. A nil Policy confines nothing: Check passes every access and
// Command starts a plain child.
type Policy struct {
	rules     filesystem.Ruleset
	mode      Mode
	lease     string
	leaseFrom job.LeaseSource
	// probe reports the host's confinement level; nil asks the host (see defaultProbe).
	probe func() (int, error)
}

// Option configures New.
type Option func(*Policy)

// WithMode sets what Command does on a host that cannot confine a child.
func WithMode(m Mode) Option {
	return func(p *Policy) { p.mode = m }
}

// WithLease names the job lease whose boundary the binds are, and the source that
// answered it, so a DeniedError can name them.
func WithLease(id string, from job.LeaseSource) Option {
	return func(p *Policy) { p.lease, p.leaseFrom = id, from }
}

// WithKernelProbe replaces the host probe Confines and Command ask: probe reports the
// host's confinement level (the landlock ABI on Linux, 1 where sandbox-exec exists on
// macOS) or why there is none. A test answers it without the host.
func WithKernelProbe(probe func() (int, error)) Option {
	return func(p *Policy) { p.probe = probe }
}

// New compiles binds into a Policy. A Bind with an unknown Mode or an empty Path is
// an error naming it, so a typo cannot quietly grant less, or more, than was meant.
func New(binds []Bind, opts ...Option) (*Policy, error) {
	p := &Policy{}
	for _, o := range opts {
		o(p)
	}
	var errs []error
	rules := make([]filesystem.Rule, 0, len(binds))
	for _, b := range binds {
		if b.Path == "" {
			errs = append(errs, fmt.Errorf("bind %d: empty path", len(rules)+len(errs)))
			continue
		}
		r, err := filesystem.ModeRule(b.Mode)
		if err != nil {
			errs = append(errs, fmt.Errorf("bind %s: %w", b.Path, err))
			continue
		}
		r.Path = filesystem.ResolveRulePath(b.Path)
		r.Create = b.Create && r.Write
		rules = append(rules, r)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("sandbox: %w", errors.Join(errs...))
	}
	p.rules = filesystem.Ruleset{Rules: rules}
	return p, nil
}

// Rules returns the compiled rules, paths resolved, in the order the binds gave them.
func (p *Policy) Rules() []filesystem.Rule {
	if p == nil {
		return nil
	}
	return slices.Clone(p.rules.Rules)
}

// Lease reports the job lease the policy was narrowed to (see WithLease and
// Boundary.Apply) and the source that answered it; "" for none.
func (p *Policy) Lease() (string, job.LeaseSource) {
	if p == nil {
		return "", ""
	}
	return p.lease, p.leaseFrom
}

// Check reports whether p permits access to path, resolved through its symlinks
// first. A refusal is a *DeniedError, which wraps filesystem.ErrDenied. Check records
// nothing; a nil p permits everything.
func (p *Policy) Check(access filesystem.Access, path string) error {
	if p == nil {
		return nil
	}
	if err := p.rules.Check(path, access); err != nil {
		return &DeniedError{Access: access, Path: path, Lease: p.lease, LeaseFrom: p.leaseFrom, Err: err}
	}
	return nil
}
