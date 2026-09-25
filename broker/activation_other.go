//go:build !unix

package broker

import (
	"fmt"
	"net"
	"runtime"
)

func adoptListener(int) (net.Listener, error) {
	return nil, fmt.Errorf("broker: socket activation is not supported on %s", runtime.GOOS)
}
