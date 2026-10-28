package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
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
		return writeSWEBench(ctx, httpGetter{}, *out)
	case "history":
		fs := flag.NewFlagSet("history", flag.ContinueOnError)
		var o historyOptions
		fs.StringVar(&o.repo, "repo", "", "repository to mine")
		fs.StringVar(&o.ref, "ref", "", "branch whose first-parent history is walked, newest first")
		fs.StringVar(&o.after, "after", "", "the model's training cutoff, YYYY-MM-DD; only commits after it are mined")
		fs.StringVar(&o.out, "o", "", "task file to write")
		fs.IntVar(&o.want.bugfix, "bugfix", 20, "bugfix tasks to keep")
		fs.IntVar(&o.want.interrogation, "interrogation", 6, "interrogation tasks to keep")
		fs.IntVar(&o.want.regeneration, "regeneration", 4, "regeneration tasks to keep")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return writeHistory(ctx, o)
	default:
		return fmt.Errorf("unknown suite %q\n%s", args[0], usage)
	}
}

// command runs name in dir and returns its stdout; a failure carries stderr. A nil env
// inherits the process environment.
func command(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, lastLines(stderr.String(), 20))
	}
	return string(out), nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
