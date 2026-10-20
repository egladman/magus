package unmarked

import "os/exec"

// Spawn starts script in the child's default process group, which a go test's
// own cancellation reaches, and is a non-test file.
func Spawn(script string) *exec.Cmd {
	return exec.Command("sh", "-c", `while :; do sleep 1; done`)
}
