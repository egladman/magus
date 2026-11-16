package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/clikit/generate"
)

// configTarget is the struct the generated overlay and flag binder write.
var configTarget = generate.Target{
	Import:   "github.com/egladman/magus/internal/config",
	Type:     "config.Config",
	EnvCheck: "config.RetiredEnv",
}

// runConfig renders the settings table and the MAGUS_* overlay that
// generate.FromStruct derives from the config struct.
func runConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	configPath := fs.String("config", "../../internal/config/config.go", "Path to config.go, read for field docs")
	settingsOut := fs.String("settings-out", "", "Generated settings table output path")
	settingsPkg := fs.String("settings-pkg", "", "Package clause of the settings table")
	applyEnvOut := fs.String("apply-env-out", "", "Generated ApplyEnv output path")
	applyEnvPkg := fs.String("apply-env-pkg", "", "Package clause of ApplyEnv")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *settingsOut == "" || *settingsPkg == "" || *applyEnvOut == "" || *applyEnvPkg == "" {
		return errors.New("usage: magus-utils config -settings-out <file> -settings-pkg <package> -apply-env-out <file> -apply-env-pkg <package>")
	}
	settings, err := generate.FromStruct(config.Defaults(), "MAGUS", *configPath)
	if err != nil {
		return err
	}
	table, err := generate.SettingsTable(settings, *settingsPkg)
	if err != nil {
		return err
	}
	env, err := generate.ApplyEnv(settings, *applyEnvPkg, configTarget)
	if err != nil {
		return err
	}
	for _, out := range []struct {
		path string
		data []byte
	}{{*settingsOut, table}, {*applyEnvOut, env}} {
		if err := emit.File(out.path, out.data); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "config: wrote %s\n", out.path)
	}
	return nil
}
