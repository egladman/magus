//go:build !unix

package broker

import (
	"errors"
	"os"
	"os/exec"
)

// InOwnGroup does nothing on a platform with no process groups: the group is the one
// process cmd starts, and what that process starts outlives a kill.
func InOwnGroup(*exec.Cmd) {}

// SignalGroup ends the one process pgid names; there is no group to reach past it.
func SignalGroup(pgid int, _ bool) error {
	p, err := os.FindProcess(pgid)
	if err != nil {
		return err
	}
	if err := p.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// groupAlive cannot tell a live process from a gone one here without opening it, so it
// reports none left once a kill was sent.
func groupAlive(int) bool { return false }

// processAlive cannot tell a live process from a gone one here, as groupAlive cannot.
func processAlive(int) bool { return false }

// ProcessGroup has no group to answer on a platform with no process groups.
func ProcessGroup(int) (int, error) {
	return 0, errors.New("broker: this platform has no process groups")
}
