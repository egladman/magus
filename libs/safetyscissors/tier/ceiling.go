package tier

import "fmt"

// Ceiling is the reference spawn rule: a child's tier is at most its parent's, by the
// order the declaration gives its cases (cheapest first). An unassigned tier on either
// side bounds nothing, so it allows the spawn. A refusal is an *AboveParentError for a
// child ranked above its parent, and an *IncomparableError for tiers from two
// declarations.
func Ceiling(parent, child Tier) error {
	if parent.IsZero() || child.IsZero() {
		return nil
	}
	above, err := child.Above(parent)
	if err != nil {
		return &IncomparableError{Err: err}
	}
	if above {
		return &AboveParentError{Parent: parent, Child: child}
	}
	return nil
}

// AboveParentError is Ceiling's refusal of a child ranked above its parent.
type AboveParentError struct {
	Parent, Child Tier
}

func (e *AboveParentError) Error() string {
	return fmt.Sprintf("a job at model tier %q may not spawn one at %q: a child's tier is at most its parent's",
		e.Parent.Name(), e.Child.Name())
}

// IncomparableError is Ceiling's refusal of two tiers [Tier.Above] cannot compare. It
// unwraps to Above's error.
type IncomparableError struct {
	Err error
}

func (e *IncomparableError) Error() string {
	return "the parent's and the child's model tiers do not compare: " + e.Err.Error()
}

func (e *IncomparableError) Unwrap() error { return e.Err }
