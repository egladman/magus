//go:build !darwin && !linux

package main

import (
	"fmt"
	"runtime"
)

// recordShowcase needs a pseudo-terminal, which internal/proc/run drives only on
// darwin and linux.
func recordShowcase(string) error {
	return fmt.Errorf("recording the showcase needs a pseudo-terminal, which %s does not provide here", runtime.GOOS)
}
