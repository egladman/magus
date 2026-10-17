package clean

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
)

func testPidEnv() string {
	return "TEST_PID=" + strconv.Itoa(os.Getpid())
}

func run(t *testing.T, script string) {
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), testPidEnv())
	SetupProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
}

// The reap tests as they are now: the condition is the liveness check.
func TestConditionChecksTheTest(t *testing.T) {
	run(t, `for i in 1 2 3 4; do (while kill -0 "$TEST_PID" 2>/dev/null; do : & wait; done) & done; sleep 0.05`)
	run(t, `trap 'echo term > got' TERM; (trap '' TERM; sleep 3; touch late) & while kill -0 "$TEST_PID" 2>/dev/null; do sleep 0.05; done`)
}

func TestBodyChecksTheTest(t *testing.T) {
	run(t, `while :; do kill -0 "$TEST_PID" || exit; sleep 1; done`)
	run(t, "until false\ndo\n kill -0 $TEST_PID 2>/dev/null || break\ndone")
	run(t, `for ((;;)); do kill -0 "$TEST_PID" || exit; done`)
}

func TestNestedLoopsBothTied(t *testing.T) {
	run(t, `while :; do kill -0 "$TEST_PID" || exit; while :; do kill -0 "$TEST_PID" || break; done; done`)
}

func TestBoundedLoops(t *testing.T) {
	run(t, `for i in 1 2 3; do sleep 1; done; i=0; while [ $i -lt 3 ]; do i=$((i+1)); done`)
	run(t, `while read -r line; do echo "$line"; done </dev/null`)
}

func TestWordsThatOnlyLookLikeLoops(t *testing.T) {
	run(t, `echo whiletrue; echo while:; echo until falsehood`)
}

func TestNoShellLoopAtAll(t *testing.T) {
	run(t, `sleep 1`)
}

func TestNonConstantScript(t *testing.T) {
	run(t, os.Getenv("SCRIPT"))
}
