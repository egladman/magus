package main

import (
	"context"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/config/gen/settings"
	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/generate"
)

// cliFlagsCommand writes the flag-name constants and binders generate.Binders renders
// from magus's command tree, and BindFlags for the config settings.
func cliFlagsCommand() command.Command {
	var out, pkg string
	return command.Command{
		Name:  "cliflags",
		Short: "Generate magus's flag binders",
		Flags: []command.Flag{
			{Name: "out", Kind: command.String, Default: "cmd/magus/gen/cli_flags.go", Doc: "output file", Target: &out},
			{Name: "pkg", Kind: command.String, Doc: "package clause of the generated file", Target: &pkg},
		},
		Run: func(context.Context, command.Invocation) error {
			if pkg == "" {
				return exit.Usagef("usage: magus-utils cliflags -out <file> -pkg <package>")
			}
			data, err := generate.Binders(commands.Root.Spec.Command, pkg, generate.WithSettings(settings.Settings, configTarget))
			if err != nil {
				return err
			}
			return emit.File(out, data)
		},
	}
}
