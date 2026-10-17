//go:build !linux

package endpoint

import "net"

func boundAt(*net.UnixListener, string) (bool, error) { return false, nil }
