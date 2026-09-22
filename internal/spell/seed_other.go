//go:build !darwin

package spell

import "errors"

const cloneSupported = false

var errNoTreeClone = errors.New("no whole-tree clone on this platform")

func cloneTree(_, _ string) error { return errNoTreeClone }

func renameExclusive(_, _ string) error { return errNoTreeClone }

func processAlive(_ int) bool { return true }
