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
	outPath := fs.String("out", "", "Flag-binding output file path (skip when empty)")
	fieldsOut := fs.String("fields-out", "", "Generated Fields-table output path (skip when empty)")
	bindOut := fs.String("bind-out", "", "Generated BindFlags output path (skip when empty)")
	applyEnvOut := fs.String("apply-env-out", "", "Generated ApplyEnv output path (skip when empty)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return generate.Write(*configPath, generate.Paths{
		Flags:    *outPath,
		Fields:   *fieldsOut,
		Bind:     *bindOut,
		ApplyEnv: *applyEnvOut,
	})
}
