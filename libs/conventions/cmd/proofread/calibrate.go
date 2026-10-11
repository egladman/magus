package main

import (
	"errors"
	"io"
)

// runCalibrate replays the labeled cases and prints each rule's precision and
// recall.
func runCalibrate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return failure(stderr, errors.New("calibrate is not implemented"))
}
