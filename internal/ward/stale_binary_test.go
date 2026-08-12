package ward

import (
	"errors"
	"fmt"
	"testing"

	"github.com/egladman/magus/libs/diagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buzzErr builds the shape gopherbuzz returns when a name cannot be resolved, wrapped
// the way workspace load wraps it, so the test exercises errors.As rather than a
// top-level type assertion that would pass for the wrong reason.
func buzzErr(code, msg string) error {
	return fmt.Errorf("magusfile: exec magusfile.buzz: %w", &diagnostics.Error{
		Code: diagnostics.Code(code),
		Msg:  msg,
	})
}

// TestExplainStaleBinary_AnnotatesTheDeadlockShapes covers the two codes an
// out-of-date binary actually produces. Both were observed together in one incident:
// a spell referenced a type the binary did not provide (BZZ1002), which made the
// module unresolvable to the magusfile importing it (BZZ2001).
func TestExplainStaleBinary_AnnotatesTheDeadlockShapes(t *testing.T) {
	for _, tc := range []struct{ name, code, msg string }{
		{"undefined type from a spell", "BZZ1002", `undefined type "Secret"`},
		{"the import it cascades into", "BZZ2001", `import "spells/github/actions": module not found`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ExplainStaleBinary(buzzErr(tc.code, tc.msg), "0.4.0", ">= 0.4.0")
			require.Error(t, got)
			assert.Contains(t, got.Error(), tc.msg, "the original diagnostic must survive")
			assert.Contains(t, got.Error(), "out-of-date magus")
			assert.Contains(t, got.Error(), "0.4.0", "names the running build")
			assert.Contains(t, got.Error(), "requires >= 0.4.0", "names the declared floor")
			assert.ErrorIs(t, got, got, "still an error chain callers can inspect")
		})
	}
}

// TestExplainStaleBinary_LeavesEverythingElseAlone is the half that keeps this honest.
// A hint attached to every load failure would be noise on a plain syntax error, and
// worse, it would teach people to ignore it.
func TestExplainStaleBinary_LeavesEverythingElseAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"a plain error", errors.New("magusfile: exec magusfile.buzz: unexpected '}'")},
		{"a different buzz code", buzzErr("BZZ1006", "call may raise but is neither declared with !> nor caught")},
		{"a magus code", buzzErr("MGS1002", "duplicate spell source")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ExplainStaleBinary(tc.err, "0.4.0", ">= 0.4.0")
			assert.Equal(t, tc.err, got, "must be returned untouched")
		})
	}
}

// TestExplainStaleBinary_WorksWithNothingDeclared is the case that matters for every
// workspace that is not this one: no required_version, and a build with no stamp. The
// hint still has to say something useful, because a workspace with no floor is exactly
// the one whose users get no warning from CheckRequiredVersion.
func TestExplainStaleBinary_WorksWithNothingDeclared(t *testing.T) {
	got := ExplainStaleBinary(buzzErr("BZZ1002", `undefined type "Secret"`), "", "")
	require.Error(t, got)
	assert.Contains(t, got.Error(), "an unstamped build")
	assert.Contains(t, got.Error(), "declares no required_version floor")
	assert.Contains(t, got.Error(), "rebuild it from this checkout",
		"the fix for an unstamped build is a rebuild, not `self update`")
}

// TestExplainStaleBinary_AppliesToDevBuilds pins the deliberate difference from
// CheckRequiredVersion, which exempts them. A dev build predating a pull is the most
// common way to reach this, so exempting it here would skip the majority case.
func TestExplainStaleBinary_AppliesToDevBuilds(t *testing.T) {
	got := ExplainStaleBinary(buzzErr("BZZ1002", `undefined type "Secret"`), DevVersion, ">= 0.4.0")
	require.Error(t, got)
	assert.Contains(t, got.Error(), "out-of-date magus")

	// And the contrast, in one place so the asymmetry is visible: the floor ward does
	// exempt the same build.
	assert.Nil(t, CheckRequiredVersion(">= 99.0.0", DevVersion),
		"CheckRequiredVersion exempts dev builds; ExplainStaleBinary deliberately does not")
}
