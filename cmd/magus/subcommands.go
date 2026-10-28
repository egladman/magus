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
		return describeCommand(ctx, root, subArgs)
	case "run":
		return runTarget(ctx, root, rc, subArgs)
	case "x":
		return x(ctx, root, rc, subArgs)
	case "where":
		return whereCommand(ctx, root, subArgs)
	case "affected":
		return affected(ctx, root, rc, subArgs)
	case "query":
		return queryCommand(ctx, root, subArgs)
	case "explain":
		return explainCommand(ctx, root, subArgs)
	case "path":
		return pathCommand(ctx, root, subArgs)
	case "refs":
		return refsCommand(ctx, root, subArgs)
	case "graph":
		return graphCommand(ctx, root, subArgs)
	case "spell":
		return spellCommand(ctx, root, subArgs)
	case "watch":
		return watchCommand(ctx, root, rc, subArgs)
	case "events":
		return eventsCommand(ctx, root, subArgs)
	case "status":
		return status(ctx, subArgs)
	case "clean":
		return cleanCommand(ctx, root, subArgs)
	case "vcs":
		return vcsCommand(ctx, root, rc, subArgs)
	case "queue":
		return queueCommand(ctx, root, subArgs)
	case "doctor":
		return doctorCommand(ctx, root, rc, subArgs)
	case "config":
		return configCommand(ctx, root, globalCfg, subArgs)
	case "session":
		return sessionCommand(ctx, root, subArgs)
	case "shell":
		return shellCommand(ctx, subArgs)
	case "job":
		return jobCommand(ctx, root, subArgs)
	case "notes":
		return notesCommand(ctx, root, subArgs)
	case "diff":
		return diffCommand(ctx, root, subArgs)
	case "server":
		return serverCommand(ctx, root, subArgs)
	case "broker":
		return brokerCommand(ctx, subArgs)
	case "mcp":
		return mcpCommand(ctx, root, subArgs)
	case "completion":
		return completion(subArgs)
	case "man":
		return manCommand(subArgs)
	case "init":
		return initCommand(ctx, root, subArgs)
	case "agent":
		return agentCommand(ctx, root, subArgs)
	case "self":
		return selfCommand(ctx, root, subArgs)
	case "buzz":
		return buzzCommand(ctx, root, subArgs)
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
