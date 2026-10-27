package ffi

import (
	"context"
	"testing"
	"time"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func shadeSession(t *testing.T) *buzz.Session {
	t.Helper()
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })
	sess.SetNativeModule("paint/shades", vm.NewMap())
	sess.SetModuleDecls("paint/shades", `export enum<str> Shade { none = "", light = "light", dark = "dark-ish" }`)
	return sess
}

// The case SessionEnums hands back is the one the session interned, so it equals the
// case Buzz code names, found by its value and not its name.
func TestSessionEnumsReturnsTheSessionsCase(t *testing.T) {
	ctx := context.Background()
	sess := shadeSession(t)
	enums := SessionEnums(ctx, sess, "paint/shades")

	got := enums.Case("Shade", "dark-ish")
	enum, name, ok := got.EnumCase()
	require.True(t, ok, "got %s, not an enum case", got.Kind())
	assert.Equal(t, "Shade", enum)
	assert.Equal(t, "dark", name)

	sess.SetGlobal("got", got)
	same, err := sess.Eval(ctx, `import "paint/shades"; return got == shades\Shade.dark;`)
	require.NoError(t, err)
	assert.True(t, same.AsBool(), "the case must compare equal to the one Buzz code names")
}

func TestSessionEnumsAnswersNullForAnEmptyValue(t *testing.T) {
	enums := SessionEnums(context.Background(), shadeSession(t), "paint/shades")
	assert.True(t, enums.Case("Shade", "").IsNull())
}

// A script run by `magus buzz` has the strict session, which rejects a top-level
// return, so reading an enum's cases must not need one.
func TestSessionEnumsReadsFromAStrictSession(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = sess.Close() })
	sess.SetNativeModule("paint/shades", vm.NewMap())
	sess.SetModuleDecls("paint/shades", `export enum<str> Shade { none = "", light = "light", dark = "dark-ish" }`)

	got := SessionEnums(ctx, sess, "paint/shades").Case("Shade", "dark-ish")

	_, name, ok := got.EnumCase()
	require.True(t, ok, "got %s, not an enum case", got.Kind())
	assert.Equal(t, "dark", name)
}

// Two readers over one session each read the same enum, and the second must not fail on
// what the first left in the session.
func TestSessionEnumsReadsTwiceOverOneSession(t *testing.T) {
	ctx := context.Background()
	sess := shadeSession(t)

	SessionEnums(ctx, sess, "paint/shades").Case("Shade", "light")
	got := SessionEnums(ctx, sess, "paint/shades").Case("Shade", "light")

	_, name, ok := got.EnumCase()
	require.True(t, ok, "got %s, not an enum case", got.Kind())
	assert.Equal(t, "light", name)
}

// A value outside the enum is a Go-side bug, and it must not cross as anything.
func TestSessionEnumsPanicsOnAValueOutsideTheEnum(t *testing.T) {
	enums := SessionEnums(context.Background(), shadeSession(t), "paint/shades")
	assert.PanicsWithValue(t, `"purple" is not a case value of shades\Shade`, func() { enums.Case("Shade", "purple") })
	assert.Panics(t, func() { enums.Case("Hue", "light") }, "an enum the module does not declare")
}

type countedCompiles struct{ phases int }

func (c *countedCompiles) Phase(buzz.CompilePhase, time.Duration, error) { c.phases++ }

func (c *countedCompiles) Import(string, buzz.ImportOutcome, time.Duration, error) {}

// A hook reads cases on every call, so a session that imported the module already
// reads them off its definition and compiles nothing.
func TestSessionEnumsCompilesNothingOnceTheModuleIsImported(t *testing.T) {
	ctx := context.Background()
	sess := shadeSession(t)
	_, err := sess.Eval(ctx, `import "paint/shades"; return shades\Shade.light;`)
	require.NoError(t, err)
	compiles := &countedCompiles{}
	sess.AddCompileObserver(compiles)

	got := SessionEnums(ctx, sess, "paint/shades").Case("Shade", "dark-ish")

	_, name, ok := got.EnumCase()
	require.True(t, ok, "got %s, not an enum case", got.Kind())
	assert.Equal(t, "dark", name)
	assert.Zero(t, compiles.phases)
}
