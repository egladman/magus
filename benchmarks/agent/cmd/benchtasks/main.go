package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/egladman/magus/benchmarks/agent/internal/benchtasks"
)

const usage = `usage:
  benchtasks swebench -o benchmarks/agent/tasks/swebench-multilingual-go.jsonl
  benchtasks history --repo . --ref main --after 2026-07-01 -o benchmarks/agent/tasks/history-go.jsonl

swebench fetches SWE-bench Multilingual, keeps the rows of its five Go repositories and
refuses them when their digest differs from the pin compiled into benchtasks. history
mines the first-parent commits on --ref after --after, the model's training cutoff, for
bugfix, interrogation and regeneration tasks, keeping a commit only when its tests
behave at the parent and at the fix. Both replace the task file whole.`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchtasks: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("name a suite\n" + usage)
	}
	switch args[0] {
	case "swebench":
		fs := flag.NewFlagSet("swebench", flag.ContinueOnError)
		out := fs.String("o", "", "task file to write")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *out == "" {
			return errors.New("swebench: -o is required")
		}
		return benchtasks.WriteSWEBench(ctx, *out)
	case "history":
		fs := flag.NewFlagSet("history", flag.ContinueOnError)
		var o benchtasks.HistoryOptions
		fs.StringVar(&o.Repo, "repo", "", "repository to mine")
		fs.StringVar(&o.Ref, "ref", "", "branch whose first-parent history is walked, newest first")
		fs.StringVar(&o.After, "after", "", "the model's training cutoff, YYYY-MM-DD; only commits after it are mined")
		fs.StringVar(&o.Out, "o", "", "task file to write")
		fs.IntVar(&o.Bugfix, "bugfix", 20, "bugfix tasks to keep")
		fs.IntVar(&o.Interrogation, "interrogation", 6, "interrogation tasks to keep")
		fs.IntVar(&o.Regeneration, "regeneration", 4, "regeneration tasks to keep")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return benchtasks.WriteHistory(ctx, o)
	default:
		return fmt.Errorf("unknown suite %q\n%s", args[0], usage)
	}
}
