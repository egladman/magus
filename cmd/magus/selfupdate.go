//go:build !noselfupdate

package main

import (
	"context"
	"crypto/ed25519"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/egladman/magus/internal/selfupdate"
	minioselfupdate "github.com/minio/selfupdate"
	"golang.org/x/term"
)

//go:embed embed/magus-release.pub
var embeddedPubKey []byte

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

// selfUpdateCompiled is true when the binary includes self-update support
// (the default; disable with -tags noselfupdate), enabling `self update`.
const selfUpdateCompiled = true

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
