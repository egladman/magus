package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/cmdline/exit"
)

func dispatchSub(ctx context.Context, root string, rc runConfig, sub string, subArgs []string) error {
	switch sub {
	case "ls":
		return ls(ctx, root, subArgs)
	case "describe":
		return describeCmd(ctx, root, subArgs)
	case "run":
		return runTarget(ctx, root, rc, subArgs)
	case "x":
		return x(ctx, root, rc, subArgs)
	case "where":
		return whereCmd(ctx, root, subArgs)
	case "affected":
		return affected(ctx, root, rc, subArgs)
	case "query":
		return queryCmd(ctx, root, subArgs)
	case "explain":
		return explainCmd(ctx, root, subArgs)
	case "path":
		return pathCmd(ctx, root, subArgs)
	case "refs":
		return refsCmd(ctx, root, subArgs)
	case "graph":
		return graphCmd(ctx, root, subArgs)
	case "spell":
		return spellCmd(ctx, root, subArgs)
	case "watch":
		return watchCmd(ctx, root, rc, subArgs)
	case "events":
		return eventsCmd(ctx, root, subArgs)
	case "status":
		return status(ctx, subArgs)
	case "clean":
		return cleanCmd(ctx, root, subArgs)
	case "vcs":
		return vcsCmd(ctx, root, rc, subArgs)
	case "queue":
		return queueCmd(ctx, root, subArgs)
	case "doctor":
		return doctorCmd(ctx, root, rc, subArgs)
	case "config":
		return configCmd(ctx, root, globalCfg, subArgs)
	case "session":
		return sessionCmd(ctx, root, subArgs)
	case "shell":
		return shellCmd(ctx, subArgs)
	case "job":
		return jobCmd(ctx, root, subArgs)
	case "notes":
		return notesCmd(ctx, root, subArgs)
	case "diff":
		return diffCmd(ctx, root, subArgs)
	case "server":
		return serverCmd(ctx, root, subArgs)
	case "broker":
		return brokerCmd(ctx, subArgs)
	case "mcp":
		return mcpCmd(ctx, root, subArgs)
	case "completion":
		return completion(subArgs)
	case "man":
		return manCmd(subArgs)
	case "init":
		return initCmd(ctx, root, subArgs)
	case "agent":
		return agentCmd(ctx, root, subArgs)
	case "self":
		return selfCmd(ctx, root, subArgs)
	case "buzz":
		return buzzCmd(ctx, root, subArgs)
	default:
		slog.ErrorContext(ctx, fmt.Sprintf("unknown subcommand %q", sub), attr.Notice(""), attr.Component("magus"))
		if suggestion := hint.Nearest(sub, knownSubcommands); suggestion != "" {
			interactive.Hint(ctx, fmt.Sprintf("did you mean %q?", suggestion))
		}
		unknownSubcommandUsage()
		return exit.Reported(exit.Usage)
	}
}

// knownSubcommands is the dispatcher's set, read from the registry so it cannot
// disagree with help, the man pages or the completions.
var knownSubcommands = func() []string {
	names := make([]string, 0, len(commands.Root.Children)+1)
	for _, c := range commands.Root.Children {
		names = append(names, c.Name)
	}
	return append(names, commands.Help.Name)
}()

func unknownSubcommandUsage() {
	fmt.Fprintln(os.Stderr, "")
	usage()
}
