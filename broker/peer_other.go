//go:build !darwin && !linux

package broker

import "errors"

func peerPID(int) (int, error) { return 0, errors.ErrUnsupported }
