package main

import (
	"errors"
	"flag"

	"github.com/egladman/magus/internal/generate/emit"
)

// runLibModules writes one std leaf package per Go package of a library
// module, adapting its exported API to Buzz. The Buzz import path is the
// module's last path element, then the package's directory under it.
func runLibModules(args []string) error {
	fs := flag.NewFlagSet("libmodules", flag.ExitOnError)
	dir := fs.String("dir", "", "the library module's directory, holding its go.mod")
	outDir := fs.String("outdir", "", "directory the generated leaf packages go under")
	genImport := fs.String("import", "", "Go import path of -outdir")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *outDir == "" || *genImport == "" {
		return errors.New("usage: magus-utils libmodules -dir <module> -outdir <dir> -import <path>")
	}
	return emit.BuzzLibrary(*dir, *outDir, *genImport)
}
