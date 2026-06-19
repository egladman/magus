//go:build selfmanage

package main

import (
	"context"
	"crypto/ed25519"
	"embed"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egladman/magus/internal/selfupdate"
	minioselfupdate "github.com/minio/selfupdate"
	"golang.org/x/term"
)

//go:embed embed/magus-release.pub
var embeddedPubKey []byte

//go:embed manpages
var embeddedManpages embed.FS

func init() {
	if len(embeddedPubKey) != ed25519.PublicKeySize {
		panic(fmt.Sprintf(
			"magus: embedded release public key is %d bytes, want %d; rebuild from a valid release",
			len(embeddedPubKey), ed25519.PublicKeySize,
		))
	}
}

// Overridable for tests (unexported; test files set them directly).
var (
	overridePubKey  []byte
	overrideClient  *http.Client
	overrideAPIBase string
)

func activeOpts() selfupdate.Options {
	opts := selfupdate.Options{APIBase: overrideAPIBase, HTTPClient: overrideClient}
	if overridePubKey != nil {
		opts.PubKey = ed25519.PublicKey(overridePubKey)
	} else {
		opts.PubKey = ed25519.PublicKey(embeddedPubKey)
	}
	return opts
}

// selfManageCompiled is true when the binary was built with -tags selfmanage,
// enabling `self update` and `self install`.
const selfManageCompiled = true

// selfCmd is the dispatcher for `magus self <subcommand>`.
func selfCmd(ctx context.Context, _ string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		selfCmdUsage()
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "update":
		return selfUpdateCmd(ctx, rest)
	case "install":
		return selfInstallCmd(ctx, rest)
	default:
		fmt.Fprintf(os.Stderr, "magus self: unknown subcommand %q\n\n", sub)
		selfCmdUsage()
		return errSilent{exitCode: 2}
	}
}

func selfCmdUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus self <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  update    update magus to the latest release (replaces running binary)")
	fmt.Fprintln(os.Stderr, "  install   install magus into ~/.local/bin (fresh install or bootstrap)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "To bootstrap a workspace, use: magus init")
	fmt.Fprintln(os.Stderr, "Run 'magus self <subcommand> --help' for subcommand flags.")
}

// selfUpdateCmd implements `magus self update`: atomically replaces the running
// binary with the latest (or a specified) release.
func selfUpdateCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("self update", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus self update [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Download the latest magus release from GitHub, verify its Ed25519")
		fmt.Fprintln(os.Stderr, "signature and SHA-256 hash, then atomically replace the running binary.")
		fmt.Fprintln(os.Stderr, "Without --bin-dir the running binary is replaced in place.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	var (
		checkOnly bool
		targetVer string
		binDir    string
		force     bool
		dryRun    bool
		yes       bool
	)
	fs.BoolVar(&checkOnly, "check", false, "print whether an update is available and exit")
	fs.StringVar(&targetVer, "version", "", "install a specific release tag (e.g. v0.4.2)")
	fs.StringVar(&binDir, "bin-dir", "", "install the updated binary into this directory instead of replacing in place")
	fs.BoolVar(&force, "force", false, "allow downgrades and re-installs of the current version")
	fs.BoolVar(&dryRun, "dry-run", false, "verify everything but do not replace the running binary")
	fs.BoolVar(&yes, "yes", false, "skip interactive confirmation")
	fs.BoolVar(&yes, "y", false, "skip interactive confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if targetVer != "" && !strings.HasPrefix(targetVer, "v") {
		targetVer = "v" + targetVer
	}

	opts := activeOpts()

	rel, err := selfupdate.FetchRelease(ctx, targetVer, opts)
	if err != nil {
		return fmt.Errorf("fetch release: %w", err)
	}

	if checkOnly {
		selfupdate.PrintUpdateStatus(rel.TagName, version)
		return nil
	}

	assetName := fmt.Sprintf("magus_%s_%s_%s.tar.gz", rel.TagName, runtime.GOOS, runtime.GOARCH)
	assets, err := selfupdate.FindAssets(rel, assetName)
	if err != nil {
		return err
	}

	manifest, err := selfupdate.FetchAndVerifyManifest(ctx, assets.Sums, assets.Sig, opts)
	if err != nil {
		return fmt.Errorf("manifest verification failed: %w", err)
	}

	if !force && version != "unknown" {
		switch selfupdate.Compare(manifest.Version, version) {
		case 0:
			return fmt.Errorf("already running %s (use --force to reinstall)", version)
		case -1:
			return fmt.Errorf("target %s is older than current %s (use --force to allow downgrade)",
				manifest.Version, version)
		}
	}

	binary, err := selfupdate.FetchAndVerifyTarball(ctx, assets.Tarball, assetName, manifest, opts)
	if err != nil {
		return fmt.Errorf("tarball verification failed: %w", err)
	}

	targetPath, err := selfupdate.ResolveTargetPath(binDir)
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Printf("dry-run: would install magus %s → %s\n", manifest.Version, targetPath)
		return nil
	}

	if !yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("non-interactive terminal: use --yes / -y to confirm the update")
		}
		fmt.Printf("Install magus %s → %s? [y/N] ", manifest.Version, targetPath)
		var answer string
		if _, err := fmt.Scanln(&answer); err != nil || strings.ToLower(strings.TrimSpace(answer)) != "y" {
			fmt.Fprintln(os.Stderr, "aborted")
			return nil
		}
	}

	if err := selfupdate.CheckWritable(targetPath); err != nil {
		return err
	}

	if err := minioselfupdate.Apply(binary, minioselfupdate.Options{TargetPath: targetPath}); err != nil {
		return fmt.Errorf("apply update: %w", err)
	}
	fmt.Printf("magus %s installed to %s\n", manifest.Version, targetPath)
	return nil
}

// selfInstallCmd implements `magus self install`: downloads and installs a
// magus release to a user-scoped directory (~/.local/bin by default). Unlike
// self update it skips the version gate and defaults to a fresh install
// location rather than the running binary path.
func selfInstallCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("self install", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus self install [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Download, verify, and install a magus release into ~/.local/bin.")
		fmt.Fprintln(os.Stderr, "Manpages are written to ~/.local/share/man/man1 by default.")
		fmt.Fprintln(os.Stderr, "No dotfiles are modified: PATH, mgs symlink, and completion")
		fmt.Fprintln(os.Stderr, "setup are printed as next steps.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	var (
		targetVer string
		binDir    string
		manDir    string
		dryRun    bool
		yes       bool
	)
	fs.StringVar(&targetVer, "version", "", "install a specific release tag (e.g. v0.4.2)")
	fs.StringVar(&binDir, "bin-dir", "", "install binary into this directory instead of ~/.local/bin")
	fs.StringVar(&manDir, "man-dir", "", "install man pages into this directory instead of ~/.local/share/man/man1")
	fs.BoolVar(&dryRun, "dry-run", false, "verify everything but do not write any files")
	fs.BoolVar(&yes, "yes", false, "skip interactive confirmation")
	fs.BoolVar(&yes, "y", false, "skip interactive confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if targetVer != "" && !strings.HasPrefix(targetVer, "v") {
		targetVer = "v" + targetVer
	}

	// Resolve target directories. Create them if missing.
	resolvedBinDir := binDir
	if resolvedBinDir == "" {
		resolvedBinDir = selfupdate.DefaultUserBinDir()
	}
	resolvedManDir := manDir
	if resolvedManDir == "" {
		resolvedManDir = selfupdate.DefaultUserManDir()
	}

	opts := activeOpts()

	rel, err := selfupdate.FetchRelease(ctx, targetVer, opts)
	if err != nil {
		return fmt.Errorf("fetch release: %w", err)
	}

	assetName := fmt.Sprintf("magus_%s_%s_%s.tar.gz", rel.TagName, runtime.GOOS, runtime.GOARCH)
	assets, err := selfupdate.FindAssets(rel, assetName)
	if err != nil {
		return err
	}

	manifest, err := selfupdate.FetchAndVerifyManifest(ctx, assets.Sums, assets.Sig, opts)
	if err != nil {
		return fmt.Errorf("manifest verification failed: %w", err)
	}

	binary, err := selfupdate.FetchAndVerifyTarball(ctx, assets.Tarball, assetName, manifest, opts)
	if err != nil {
		return fmt.Errorf("tarball verification failed: %w", err)
	}

	// Resolve the final binary path, creating the directory if needed.
	if err := selfupdate.EnsureDir(resolvedBinDir); err != nil {
		return err
	}
	targetPath, err := selfupdate.ResolveTargetPath(resolvedBinDir)
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Printf("dry-run: would install magus %s → %s\n", manifest.Version, targetPath)
		fmt.Printf("dry-run: would install man pages → %s\n", resolvedManDir)
		return nil
	}

	if !yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("non-interactive terminal: use --yes / -y to confirm the install")
		}
		fmt.Printf("Install magus %s → %s? [y/N] ", manifest.Version, targetPath)
		var answer string
		if _, err := fmt.Scanln(&answer); err != nil || strings.ToLower(strings.TrimSpace(answer)) != "y" {
			fmt.Fprintln(os.Stderr, "aborted")
			return nil
		}
	}

	if err := selfupdate.CheckParentWritable(targetPath); err != nil {
		return err
	}

	if err := minioselfupdate.Apply(binary, minioselfupdate.Options{TargetPath: targetPath}); err != nil {
		return fmt.Errorf("apply install: %w", err)
	}
	fmt.Printf("magus %s installed to %s\n", manifest.Version, targetPath)

	// Install embedded man pages.
	if err := installManpages(resolvedManDir); err != nil {
		// Non-fatal: warn but don't fail the overall install.
		slog.WarnContext(ctx, "man pages not installed; retry with --man-dir pointing to a writable directory", slog.String("error", err.Error()))
	} else {
		fmt.Printf("man pages installed to %s\n", resolvedManDir)
	}

	// Print post-install next steps. No dotfiles are modified.
	printInstallNextSteps(targetPath, resolvedBinDir)
	return nil
}

// installManpages writes the embedded .1 files to manDir, creating it if needed.
func installManpages(manDir string) error {
	if err := selfupdate.EnsureDir(manDir); err != nil {
		return err
	}
	entries, err := embeddedManpages.ReadDir("manpages")
	if err != nil {
		return fmt.Errorf("read embedded manpages: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := embeddedManpages.ReadFile("manpages/" + e.Name())
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}
		dest := filepath.Join(manDir, e.Name())
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
	}
	return nil
}

// printInstallNextSteps prints shell-setup guidance. Nothing is written to
// dotfiles; the user applies these steps at their own pace.
func printInstallNextSteps(binaryPath, binDir string) {
	fmt.Println("")
	fmt.Println("Next steps:")

	// PATH: only print if the bin dir isn't already on PATH.
	if !dirOnPath(binDir) {
		fmt.Printf("  # Add magus to PATH (add to ~/.bashrc, ~/.zshrc, etc.):\n")
		fmt.Printf("  export PATH=\"%s:$PATH\"\n", binDir)
		fmt.Println("")
	}

	// mgs symlink.
	mgsPath := filepath.Join(binDir, "mgs")
	fmt.Printf("  # Optional: add the 'mgs' shorthand:\n")
	fmt.Printf("  ln -s %s %s\n", binaryPath, mgsPath)
	fmt.Println("")

	// Shell completion.
	fmt.Printf("  # Optional: enable tab completion (bash shown; also: zsh, fish):\n")
	fmt.Printf("  magus completion bash >> ~/.bashrc\n")
}

// dirOnPath reports whether dir appears in the PATH environment variable.
func dirOnPath(dir string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d == absDir || d == dir {
			return true
		}
	}
	return false
}
