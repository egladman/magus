// Subcommand `config` renders the config-derived artifacts. The generator itself
// lives in internal/config/generate, beside the schema whose tags it interprets;
// this file is only the flag parsing that names the outputs.

package main

import (
	"flag"

	"github.com/egladman/magus/internal/config/generate"
)

func runConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	configPath := fs.String("config", "../../internal/config/config.go", "Path to config.go")
	fieldsOut := fs.String("fields-out", "", "Generated Fields-table output path (skip when empty)")
	fieldsPkg := fs.String("fields-pkg", "", "Package clause of the Fields table")
	bindOut := fs.String("bind-out", "", "Generated BindFlags output path (skip when empty)")
	bindPkg := fs.String("bind-pkg", "", "Package clause of BindFlags")
	applyEnvOut := fs.String("apply-env-out", "", "Generated ApplyEnv output path (skip when empty)")
	applyEnvPkg := fs.String("apply-env-pkg", "", "Package clause of ApplyEnv")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return generate.Write(*configPath, generate.Paths{
		Fields:   generate.Output{Path: *fieldsOut, Package: *fieldsPkg},
		Bind:     generate.Output{Path: *bindOut, Package: *bindPkg},
		ApplyEnv: generate.Output{Path: *applyEnvOut, Package: *applyEnvPkg},
	})
}
