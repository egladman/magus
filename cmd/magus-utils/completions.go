package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/generate"
)

// The completion scripts are hand-written shell in four dialects, and the LOGIC in
// them is stable; what drifted was the data. So this generator owns only the marked
// list regions and leaves the surrounding shell alone:
//
//	# magus-utils:subcommands:begin
//	  ...regenerated...
//	# magus-utils:subcommands:end

// shellMarker names the generated list region; all four dialects comment with #.
var shellMarker = emit.CommentMarker("#", "subcommands")

// completionScripts maps each script to the shell generate.Completion renders for it.
var completionScripts = map[string]string{
	"magus.bash": "bash",
	"magus.zsh":  "zsh",
	"magus.fish": "fish",
	"magus.ps1":  "powershell",
}

func completionsCommand() command.Command {
	var outDir string
	return command.Command{
		Name:  "completions",
		Short: "Write magus's shell completion scripts",
		Flags: []command.Flag{
			{Name: "out", Kind: command.String, Default: "completions", Doc: "Directory holding the completion scripts", Target: &outDir},
		},
		Run: func(context.Context, command.Invocation) error {
			return writeCompletions(outDir, slices.Concat(commands.Root.Spec.Children, []command.Command{commands.Help}))
		},
	}
}

// writeCompletions fills the marked region of each completion script in dir with subs.
func writeCompletions(dir string, subs []command.Command) error {
	if len(subs) == 0 {
		return errors.New("completions: no subcommands to write")
	}
	for name, shell := range completionScripts {
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		list, err := generate.Completion(shell, subs)
		if err != nil {
			return err
		}
		updated, err := emit.Region(string(src), shellMarker, list)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if updated == string(src) {
			continue // already current; do not touch the mtime
		}
		if err := emit.File(path, []byte(updated)); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "completions: wrote %d subcommands to %s\n", len(subs), path)
	}
	return nil
}
