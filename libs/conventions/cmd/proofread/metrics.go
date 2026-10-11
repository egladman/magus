package main

import (
	"errors"
	"io"

	"github.com/egladman/magus/libs/conventions/proofread"
)

// runMetrics writes the readability metrics of each text kind names.
func runMetrics(kind proofread.Kind, paths []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return failure(stderr, errors.New("-metrics is not implemented"))
}
