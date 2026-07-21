package std

import (
	"context"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTesterModule drives the ported `testing` module (gopherbuzz's rendition of
// upstream Buzz's Tester) end to end: static init, lifecycle hooks via the
// optional-call-narrowing `->`, `it`, and every assertion — including the three
// that upstream expresses with generics (assertOfType / assertThrows /
// assertDoesNotThrow), adapted here since gopherbuzz erases generics. summary()
// is not called (it would os.exit); the pass/fail tallies are read directly.
func TestTesterModule(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	Register(sess)

	src := `
import "testing";

var hooks = mut [<str>];
final t = testing\Tester.init(
    fun (t: testing\Tester) { hooks.append("beforeAll"); },
    fun (t: testing\Tester) { hooks.append("beforeEach"); },
    fun (t: testing\Tester) { hooks.append("afterAll"); },
    fun (t: testing\Tester) { hooks.append("afterEach"); },
);

t.it("passing", fun () {
    t.assertEqual(2 + 2, 4, "");
    t.assertNotEqual(1, 2, "");
    t.assertOfType(42, "int", "");
    t.assertOfType("hi", "str", "");
    t.assertAreEqual([3, 3, 3], "");
    t.assertThrows(fun () > void { throw "boom"; }, "");
    t.assertDoesNotThrow(fun () > void {}, "");
});

t.it("failing", fun () {
    t.assertEqual(1, 2, "");
});

var passed = t.succeededTests();
var failed = t.failedTests();
var hookTrail = hooks.join(",");
`
	require.NoError(t, sess.Exec(ctx, src), "exec Tester program")
	assert.Equal(t, "1", sess.GetGlobal("passed").String(), "one test should pass")
	assert.Equal(t, "1", sess.GetGlobal("failed").String(), "one test should fail")
	assert.Equal(t,
		"beforeAll,beforeEach,afterEach,beforeEach,afterEach",
		sess.GetGlobal("hookTrail").String(),
		"lifecycle hooks fire around each it via -> narrowing",
	)
}
