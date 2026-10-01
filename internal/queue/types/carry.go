package types

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/types/enum"
)

// CarryTier is how far a change made since an approval reaches, from a rebase that
// changed nothing to code no reviewer saw. The constants are in that order.
type CarryTier string

const (
	// CarryRebase is the approved delta, replayed onto the head's base, giving exactly the
	// head. It is a verdict's tier, never a path's.
	CarryRebase CarryTier = "rebase"
	// CarryGenerated is a path the build tool writes whole or maintains itself.
	CarryGenerated CarryTier = "generated"
	// CarryProse is a path the build tool's prose globs claim (magus: gate_low_risk,
	// markdown by default), which covers changelog fragments.
	CarryProse CarryTier = "prose"
	// CarryCommentOnly is a file whose edit touches only comments, by the comment syntax
	// the build tool declares for its language.
	CarryCommentOnly CarryTier = "comment-only"
	// CarryCode is everything else, and every path the build tool cannot classify.
	CarryCode CarryTier = "code"
)

var carryTiers = enum.Set[CarryTier]{CarryRebase, CarryGenerated, CarryProse, CarryCommentOnly, CarryCode}

func (t CarryTier) Values() []string { return carryTiers.Strings() }
func (t CarryTier) Valid() bool      { return carryTiers.Valid(t) }
func (t CarryTier) String() string   { return enum.String(t) }

// reach orders tiers by how far they reach; an unknown tier reaches as far as code.
func (t CarryTier) reach() int {
	if i := slices.Index(carryTiers, t); i >= 0 {
		return i
	}
	return slices.Index(carryTiers, CarryCode)
}

// CarryPolicy is the tiers whose changes leave an approval standing. CarryRebase is
// always allowed and CarryCode never is: an approval carried over unseen code would
// vouch for code nobody reviewed. The zero policy is the strictest, rebase alone.
type CarryPolicy []CarryTier

// DefaultCarryPolicy is what a workspace that declares no policy carries: everything but
// code.
func DefaultCarryPolicy() CarryPolicy {
	return CarryPolicy{CarryRebase, CarryGenerated, CarryProse, CarryCommentOnly}
}

// Allows reports whether a change of tier t leaves an approval standing.
func (p CarryPolicy) Allows(t CarryTier) bool {
	switch t {
	case CarryRebase:
		return true
	case CarryCode:
		return false
	}
	return slices.Contains(p, t)
}

// CheckCarryTier refuses a name a [CarryPolicy] cannot hold: one outside
// [CarryTier.Values], and code, which never carries.
func CheckCarryTier(name string) error {
	t := CarryTier(name)
	switch {
	case t == CarryCode:
		return fmt.Errorf("%q never carries an approval: a reviewer has to see a code change (want some of %s)", name, carriable())
	case t == "" || !t.Valid():
		return fmt.Errorf("%q is not an approval carry tier (want some of %s)", name, carriable())
	}
	return nil
}

func carriable() string {
	return strings.Join(slices.DeleteFunc(CarryTier("").Values(), func(s string) bool { return s == string(CarryCode) }), ", ")
}

// ClassifiedPath is the build tool's tier for one path's edit, and the fact it rests on,
// so a reader can dispute it from the message alone.
type ClassifiedPath struct {
	Path string
	Tier CarryTier
	Why  string
}

// Line is how verdicts name the path: `<path> (<tier>: <why>)`.
func (c ClassifiedPath) Line() string { return c.Path + " (" + string(c.Tier) + ": " + c.Why + ")" }

// CarryVerdict says whether the approval given at From still covers Head. From's own
// delta is replayed onto Head's base; what Head holds beyond that replay is what changed
// since the approval, and the approval carries when the policy allows the tier of every
// changed path.
type CarryVerdict struct {
	From  string
	Head  string
	Carry bool
	// Tier is how far the change since From reaches: the furthest-reaching tier of
	// Changed when the approval carries, CarryRebase when nothing changed, and when it
	// does not carry, the furthest-reaching tier the policy refused. A delta that cannot
	// be replayed, or conflicts when it is, is CarryCode.
	Tier CarryTier
	// Changed is every path Head differs in from the replay, classified; empty for a
	// rebase and for a delta that could not be replayed.
	Changed []ClassifiedPath
	// Refused are the paths that kept the approval from carrying: the changed paths the
	// policy refused, or the paths the replay conflicted in.
	Refused []string
	// Reason is one line saying why, naming the paths, for a person to read.
	Reason string
}

// furthest is the furthest-reaching tier of paths, CarryRebase when there are none.
func furthest(paths []ClassifiedPath) CarryTier {
	t := CarryRebase
	for _, p := range paths {
		if p.Tier.reach() > t.reach() {
			t = p.Tier
		}
	}
	return t
}

// Decide settles v from v.Changed under policy, writing Carry, Tier, Refused and
// Reason.
func (v *CarryVerdict) Decide(policy CarryPolicy) {
	var refused []ClassifiedPath
	for _, p := range v.Changed {
		if !policy.Allows(p.Tier) {
			refused = append(refused, p)
		}
	}
	if len(refused) == 0 {
		v.Carry, v.Tier, v.Refused = true, furthest(v.Changed), nil
		if len(v.Changed) == 0 {
			v.Reason = "head " + shortID(v.Head) + " is " + shortID(v.From) + " rebased with its diff unchanged"
			return
		}
		v.Reason = "head " + shortID(v.Head) + " is " + shortID(v.From) + " rebased; changed since the approval: " + lines(v.Changed)
		return
	}
	v.Carry, v.Tier = false, furthest(refused)
	v.Refused = make([]string, len(refused))
	for i, p := range refused {
		v.Refused[i] = p.Path
	}
	v.Reason = "changed since the approval at " + shortID(v.From) + ": " + lines(refused)
}

func lines(paths []ClassifiedPath) string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = p.Line()
	}
	return strings.Join(out, ", ")
}

func shortID(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
