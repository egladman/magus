package main

import (
	"context"

	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
)

// This generator builds independently of the host registry that imports its output.
func main() {
	var dir, outDir, genImport string
	command.Main(command.Command{
		Name:  "magus-libmodules",
		Short: "Generate the Buzz packages of a library module",
		Flags: []command.Flag{
			{Name: "dir", Kind: command.String, Doc: "the library module's directory, holding its go.mod", Target: &dir},
			{Name: "outdir", Kind: command.String, Doc: "directory the generated leaf packages go under", Target: &outDir},
			{Name: "import", Kind: command.String, Doc: "Go import path of -outdir", Target: &genImport},
		},
		Run: func(context.Context, command.Invocation) error {
			if dir == "" || outDir == "" || genImport == "" {
				return exit.Usagef("-dir, -outdir and -import are required")
			}
			return emit.BuzzLibrary(dir, outDir, genImport)
		},
	})
}
