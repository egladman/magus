package flagged

import (
	"os/exec"
	"testing"
)

func run(t *testing.T, script string) {
	cmd := exec.Command("sh", "-c", script)
	SetupProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
}

func TestColon(t *testing.T) {
	run(t, `while :; do sleep 1; done`) // want `shell loop "while :" never ends, and this test puts its child in a process group \(SetupProcessGroup\).*; make the loop condition check that the test is alive, as in while kill -0 "\$TEST_PID"; use testloops hint$`
}

func TestTrue(t *testing.T) {
	run(t, `while true; do sleep 1; done`) // want `shell loop "while true" never ends`
}

func TestTrueWithDoOnTheSameLine(t *testing.T) {
	run(t, "while true\ndo\n sleep 1\ndone") // want `shell loop "while true" never ends`
}

func TestUntilFalse(t *testing.T) {
	run(t, `until false; do sleep 1; done`) // want `shell loop "until false" never ends`
}

func TestArithmeticFor(t *testing.T) {
	run(t, `for ((;;)); do sleep 1; done`) // want `shell loop "for \(\(;;\)\)" never ends`
}

// The reap tests before they were tied to the test process.
func TestSubshellLoop(t *testing.T) {
	run(t, `for i in 1 2 3 4; do (while :; do : & wait; done) & done; sleep 0.05`) // want `shell loop "while :" never ends`
}

func TestTrapAndLoop(t *testing.T) {
	run(t, `trap 'echo term > got' TERM; (trap '' TERM; sleep 3; touch late) & while :; do sleep 0.05; done`) // want `shell loop "while :" never ends`
}

func TestConcatenatedScript(t *testing.T) {
	run(t, "while :; "+"do sleep 1; done") // want `shell loop "while :" never ends`
}

const detached = `while :; do sleep 1; done` // want `shell loop "while :" never ends`

func TestNamedConstantReportsOnce(t *testing.T) {
	run(t, detached)
	run(t, detached)
}

func TestTwoLoops(t *testing.T) {
	run(t, `while :; do a; done; until false; do b; done`) // want `shell loop "while :" never ends` `shell loop "until false" never ends`
}

// The outer check does not cover a loop nested inside its body that has none of its own.
func TestInnerLoopUntied(t *testing.T) {
	run(t, `while kill -0 "$TEST_PID"; do while :; do sleep 1; done; done`) // want `shell loop "while :" never ends`
}

func TestLoopWithoutDone(t *testing.T) {
	run(t, `while :; do sleep 1`) // want `shell loop "while :" never ends`
}

// A check after the loop is not part of it.
func TestCheckAfterTheLoop(t *testing.T) {
	run(t, `while :; do sleep 1; done; kill -0 "$TEST_PID"`) // want `shell loop "while :" never ends`
}
