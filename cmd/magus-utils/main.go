package main

import (
	"context"

	"github.com/egladman/magus/libs/clikit/command"
)

func main() { command.Main(root()) }

// root declares every scribe. Each reads a source of truth and writes the
// derived artifact; see the per-subcommand file for the details. It builds
// fresh flag targets on each call, so tests can invoke it repeatedly.
func root() command.Command {
	return command.Command{
		Name:  "magus-utils",
		Short: "Generate the artifacts magus derives from its sources of truth.",
		Children: []command.Command{
			apiCommand(),
			bindingsCommand(),
			boundaryDecodeCommand(),
			boundaryListCommand(),
			boundaryObjectsCommand(),
			cliFlagsCommand(),
			completionsCommand(),
			configCommand(),
			contractCommand(),
			cutCommand(),
			devShimsCommand(),
			diffDemoCommand(),
			goAPICommand(),
			jobSchemaCommand(),
			libModulesCommand(),
			mcpToolsCommand(),
			moduleDeclsCommand(),
			moduleSetCommand(),
			releaseIndexCommand(),
			tsTypesCommand(),
			typesCommand(),
		},
	}
}

// moduleDeclsCommand declares moduledecls, whose own flag parsing lives in
// module_decls.go: another change owns that file, so this hands it its one flag
// back as arguments until that file declares its command here.
func moduleDeclsCommand() command.Command {
	var outDir string
	return command.Command{
		Name:  "moduledecls",
		Short: "Write the per-module .buzz declarations",
		Flags: []command.Flag{
			{Name: "outdir", Kind: command.String, Doc: "output directory for the per-module .buzz declarations", Target: &outDir},
		},
		Run: func(_ context.Context, inv command.Invocation) error {
			return runModuleDecls(append([]string{"-outdir", outDir}, inv.Args...))
		},
	}
}
