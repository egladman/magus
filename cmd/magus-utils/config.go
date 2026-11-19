package main

import (
	"context"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/generate"
)

// configTarget is the struct the generated overlay and flag binder write.
var configTarget = generate.Target{
	Import:   "github.com/egladman/magus/internal/config",
	Type:     "config.Config",
	EnvCheck: "config.RetiredEnv",
}

// configCommand renders the settings table and the MAGUS_* overlay that
// generate.FromStruct derives from the config struct.
func configCommand() command.Command {
	var configPath, settingsOut, settingsPkg, applyEnvOut, applyEnvPkg string
	return command.Command{
		Name:  "config",
		Short: "Generate the config settings table and its MAGUS_* overlay",
		Flags: []command.Flag{
			{Name: "config", Kind: command.String, Default: "../../internal/config/config.go", Doc: "Path to config.go, read for field docs", Target: &configPath},
			{Name: "settings-out", Kind: command.String, Doc: "Generated settings table output path", Target: &settingsOut},
			{Name: "settings-pkg", Kind: command.String, Doc: "Package clause of the settings table", Target: &settingsPkg},
			{Name: "apply-env-out", Kind: command.String, Doc: "Generated ApplyEnv output path", Target: &applyEnvOut},
			{Name: "apply-env-pkg", Kind: command.String, Doc: "Package clause of ApplyEnv", Target: &applyEnvPkg},
		},
		Run: func(context.Context, command.Invocation) error {
			if settingsOut == "" || settingsPkg == "" || applyEnvOut == "" || applyEnvPkg == "" {
				return exit.Usagef("usage: magus-utils config -settings-out <file> -settings-pkg <package> -apply-env-out <file> -apply-env-pkg <package>")
			}
			settings, err := generate.FromStruct(config.Defaults(), "MAGUS", configPath)
			if err != nil {
				return err
			}
			table, err := generate.SettingsTable(settings, settingsPkg)
			if err != nil {
				return err
			}
			env, err := generate.ApplyEnv(settings, applyEnvPkg, configTarget)
			if err != nil {
				return err
			}
			for _, out := range []struct {
				path string
				data []byte
			}{{settingsOut, table}, {applyEnvOut, env}} {
				if err := emit.File(out.path, out.data); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "config: wrote %s\n", out.path)
			}
			return nil
		},
	}
}
