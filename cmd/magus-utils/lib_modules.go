package main

import (
	"context"

	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
)

// libModulesCommand writes one std leaf package per Go package of a library
// module, adapting its exported API to Buzz. The Buzz import path is the
// module's last path element, then the package's directory under it.
func libModulesCommand() command.Command {
	var dir, outDir, genImport string
	return command.Command{
		Name:  "libmodules",
		Short: "Generate the Buzz packages of a library module",
		Flags: []command.Flag{
			{Name: "dir", Kind: command.String, Doc: "the library module's directory, holding its go.mod", Target: &dir},
			{Name: "outdir", Kind: command.String, Doc: "directory the generated leaf packages go under", Target: &outDir},
			{Name: "import", Kind: command.String, Doc: "Go import path of -outdir", Target: &genImport},
		},
		Run: func(context.Context, command.Invocation) error {
			if dir == "" || outDir == "" || genImport == "" {
				return exit.Usagef("usage: magus-utils libmodules -dir <module> -outdir <dir> -import <path>")
			}
			return emit.BuzzLibrary(dir, outDir, genImport)
		},
	}
}
