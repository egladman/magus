package unmarked

import (
	"os/exec"
	"testing"
)

// No marker in this file: the child shares the test's process group, so the
// group kill that ends the go test ends the loop.
func TestEndlessLoopInTheDefaultGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", `while :; do sleep 1; done`)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Process.Kill()
}
