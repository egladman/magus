package main

import (
	"errors"
	"flag"
	"io"

	"github.com/egladman/magus/libs/proofread/internal/wordclass/train"
)

func runTrain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("proofread train", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var o train.Options

	fs.StringVar(&o.ModelPath, "model", "", "write the gzipped model to this `file`")
	fs.StringVar(&o.TestPath, "testdata", "", "write the gzipped EWT test split to this `file`")
	fs.IntVar(&o.Iterations, "iterations", train.DefaultIterations, "passes over the training split")
	fs.Uint64Var(&o.Seed, "seed", train.DefaultSeed, "seed for the per-pass shuffle")
	fs.IntVar(&o.Levels, "levels", train.DefaultLevels, "the largest quantized weight")

	if err := fs.Parse(args); err != nil {
		return exitError
	}

	if fs.NArg() > 0 {
		return failure(stderr, errors.New("train takes no arguments"))
	}

	if err := train.Run(stdout, o); err != nil {
		return failure(stderr, err)
	}

	return exitOK
}
