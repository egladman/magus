package main

import (
	"context"

	"github.com/egladman/magus/benchmarks/agent/internal/benchtasks"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
)

func main() { command.Main(root()) }

// root builds fresh flag targets on each call, so tests can invoke it again.
func root() command.Command {
	var out string
	var o benchtasks.HistoryOptions
	return command.Command{
		Name:  "benchtasks",
		Short: "Write a benchmark task file from SWE-bench or from this repository's history.",
		Long: "swebench fetches SWE-bench Multilingual, keeps the rows of its five Go repositories and " +
			"refuses them when their digest differs from the pin compiled into benchtasks. history " +
			"mines the first-parent commits on --ref after --after, the model's training cutoff, for " +
			"bugfix, interrogation and regeneration tasks, keeping a commit only when its tests " +
			"behave at the parent and at the fix. Both replace the task file whole.",
		Usage: "benchtasks <suite> [flags]",
		Children: []command.Command{
			{
				Name:  "swebench",
				Short: "Write the SWE-bench Multilingual Go rows.",
				Usage: "benchtasks swebench -o <file>",
				Flags: []command.Flag{
					{Name: "o", Kind: command.String, Doc: "task file to write", Target: &out},
				},
				Examples: []command.Example{
					{Comment: "Pin the SWE-bench Go rows", Command: "benchtasks swebench -o benchmarks/agent/tasks/swebench-multilingual-go.jsonl"},
				},
				Run: func(ctx context.Context, _ command.Invocation) error {
					if out == "" {
						return exit.Usagef("swebench: -o is required")
					}
					return benchtasks.WriteSWEBench(ctx, out)
				},
			},
			{
				Name:  "history",
				Short: "Mine this repository's history for bugfix, interrogation and regeneration tasks.",
				Usage: "benchtasks history --repo <dir> --ref <branch> --after <date> -o <file>",
				Flags: []command.Flag{
					{Name: "repo", Kind: command.String, Doc: "repository to mine", Target: &o.Repo},
					{Name: "ref", Kind: command.String, Doc: "branch whose first-parent history is walked, newest first", Target: &o.Ref},
					{Name: "after", Kind: command.String, Doc: "the model's training cutoff, YYYY-MM-DD; only commits after it are mined", Target: &o.After},
					{Name: "o", Kind: command.String, Doc: "task file to write", Target: &o.Out},
					{Name: "bugfix", Kind: command.Int, Default: 20, Doc: "bugfix tasks to keep", Target: &o.Bugfix},
					{Name: "interrogation", Kind: command.Int, Default: 6, Doc: "interrogation tasks to keep", Target: &o.Interrogation},
					{Name: "regeneration", Kind: command.Int, Default: 4, Doc: "regeneration tasks to keep", Target: &o.Regeneration},
				},
				Examples: []command.Example{
					{Comment: "Mine the commits after the training cutoff", Command: "benchtasks history --repo . --ref main --after 2026-07-01 -o benchmarks/agent/tasks/history-go.jsonl"},
				},
				Run: func(ctx context.Context, _ command.Invocation) error {
					return benchtasks.WriteHistory(ctx, o)
				},
			},
		},
	}
}
