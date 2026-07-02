package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
)

//go:embed completions/magus.bash
var completionBash string

//go:embed completions/magus.zsh
var completionZsh string

//go:embed completions/magus.fish
var completionFish string

//go:embed completions/magus.ps1
var completionPowerShell string

func completion(args []string) error {
	fs := flag.NewFlagSet("completion", flag.ContinueOnError)
	fs.Usage = completionUsage
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		completionUsage()
		return fmt.Errorf("magus completion: shell name required (bash, zsh, fish, or powershell)")
	}
	switch fs.Arg(0) {
	case "bash":
		_, err := io.WriteString(os.Stdout, completionBash)
		return err
	case "zsh":
		_, err := io.WriteString(os.Stdout, completionZsh)
		return err
	case "fish":
		_, err := io.WriteString(os.Stdout, completionFish)
		return err
	case "powershell", "pwsh":
		_, err := io.WriteString(os.Stdout, completionPowerShell)
		return err
	default:
		completionUsage()
		return fmt.Errorf("magus completion: unsupported shell %q (choose: bash, zsh, fish, powershell)", fs.Arg(0))
	}
}

func completionUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus completion <bash|zsh|fish|powershell>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Print a shell completion script to stdout.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Examples:")
	fmt.Fprintln(os.Stderr, "  # Bash")
	fmt.Fprintln(os.Stderr, "  magus completion bash >> ~/.bashrc")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  # Zsh")
	fmt.Fprintln(os.Stderr, "  magus completion zsh >> ~/.zshrc")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  # Fish")
	fmt.Fprintln(os.Stderr, "  magus completion fish >> ~/.config/fish/config.fish")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  # PowerShell (Windows / cross-platform pwsh)")
	fmt.Fprintln(os.Stderr, "  magus completion powershell >> $PROFILE")
}
