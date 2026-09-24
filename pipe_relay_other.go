//go:build !linux && !darwin

package magus

import (
	"errors"
	"os"
)

// Unreachable in practice: pipepeer proves no upstream here, so no run waits on one.
var errNoRelay = errors.New("stdin relay is not supported on this platform")

func dupForDrain(*os.File) (*os.File, func() error, error) { return nil, nil, errNoRelay }

func installRelay(*os.File) (*os.File, error) { return nil, errNoRelay }
