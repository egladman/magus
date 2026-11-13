package main

import (
	"errors"
	"flag"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/config/gen/settings"
	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/clikit/generate"
)

// runCLIFlags writes the flag-name constants and binders generate.Binders renders
// from magus's command tree, and BindFlags for the config settings.
func runCLIFlags(args []string) error {
	fs := flag.NewFlagSet("cliflags", flag.ExitOnError)
	out := fs.String("out", "cmd/magus/gen/cli_flags.go", "output file")
	pkg := fs.String("pkg", "", "package clause of the generated file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pkg == "" {
		return errors.New("usage: magus-utils cliflags -out <file> -pkg <package>")
	}
	data, err := generate.Binders(commands.Root, *pkg, generate.WithSettings(settings.Settings, configTarget))
	if err != nil {
		return err
	}
	return emit.File(*out, data)
}
