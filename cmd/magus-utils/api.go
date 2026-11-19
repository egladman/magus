package main

import (
	"context"
	"os"
	"strings"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/clikit/command"
)

// apiCommand writes magus's public CLI API as a sorted .lock snapshot (the same plain
// sorted-line format as urls.lock): one line per subcommand, flag, operand and
// config key. Committed and drift-gated, its diff is what a reviewer inspects for
// backward-incompatible changes: a removed line is a removed API element.
func apiCommand() command.Command {
	var out string
	return command.Command{
		Name:  "api",
		Short: "Write magus's public CLI API lock",
		Flags: []command.Flag{
			{Name: "out", Kind: command.String, Doc: "write the public-API lock to this path (default: stdout)", Target: &out},
		},
		Run: func(context.Context, command.Invocation) error {
			lines, err := commands.API(config.KnownKeys())
			if err != nil {
				return err
			}
			data := []byte(strings.Join(lines, "\n") + "\n")
			if out == "" {
				_, err := os.Stdout.Write(data)
				return err
			}
			return emit.File(out, data)
		},
	}
}
