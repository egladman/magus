package main

import (
	"context"
	"errors"
	"io"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/proofread/train/wordclass"
)

// trainOpts holds the flags of the one command a process runs.
var trainOpts struct {
	model, testdata          string
	iterations, seed, levels int
}

var trainCmd = command.Command{
	Name:   "train",
	Short:  "train the word-class model on UD English EWT into --model, its test split into --testdata",
	Effect: command.EffectLocalWrite,
	Flags: []command.Flag{
		{Name: "model", Kind: command.String, Doc: "write the gzipped model to this `file`", Target: &trainOpts.model},
		{Name: "testdata", Kind: command.String, Doc: "write the gzipped EWT test split to this `file`", Target: &trainOpts.testdata},
		{Name: "iterations", Kind: command.Int, Default: wordclass.DefaultIterations, Doc: "passes over the training split", Target: &trainOpts.iterations},
		{Name: "seed", Kind: command.Int, Default: wordclass.DefaultSeed, Doc: "seed for the per-pass shuffle", Target: &trainOpts.seed},
		{Name: "levels", Kind: command.Int, Default: wordclass.DefaultLevels, Doc: "the largest quantized weight", Target: &trainOpts.levels},
	},
	Run: func(_ context.Context, inv command.Invocation) error {
		if trainOpts.seed < 0 {
			return reported(failure(inv.Stderr, errors.New("-seed is negative: want a whole number from 0")))
		}

		o := wordclass.Options{ModelPath: trainOpts.model, TestPath: trainOpts.testdata, Iterations: trainOpts.iterations, Seed: uint64(trainOpts.seed), Levels: trainOpts.levels}

		return reported(runTrain(o, inv.Args, inv.Stdout, inv.Stderr))
	},
}

func runTrain(o wordclass.Options, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		return failure(stderr, errors.New("train takes no arguments"))
	}

	if err := wordclass.Run(stdout, o); err != nil {
		return failure(stderr, err)
	}

	return exitOK
}
