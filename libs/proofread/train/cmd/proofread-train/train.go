package main

import (
	"errors"
	"flag"
	"io"

	"github.com/egladman/magus/libs/proofread/train/wordclass"
)

func runTrain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("proofread-train train", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var o wordclass.Options

	fs.StringVar(&o.ModelPath, "model", "", "write the gzipped model to this `file`")
	fs.StringVar(&o.TestPath, "testdata", "", "write the gzipped EWT test split to this `file`")
	fs.IntVar(&o.Iterations, "iterations", wordclass.DefaultIterations, "passes over the training split")
	fs.Uint64Var(&o.Seed, "seed", wordclass.DefaultSeed, "seed for the per-pass shuffle")
	fs.IntVar(&o.Levels, "levels", wordclass.DefaultLevels, "the largest quantized weight")

	if err := fs.Parse(args); err != nil {
		return exitError
	}

	if fs.NArg() > 0 {
		return failure(stderr, errors.New("train takes no arguments"))
	}

	if err := wordclass.Run(stdout, o); err != nil {
		return failure(stderr, err)
	}

	return exitOK
}
