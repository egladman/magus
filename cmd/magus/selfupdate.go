//go:build !noselfupdate

package main

import (
	"context"
	"crypto/ed25519"
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

// Overridable for tests (unexported; test files set them directly).
var (
	overridePubKey       []byte
	overrideClient       *http.Client
	overrideDiscoveryURL string
)

func activeOpts() selfupdate.Options {
	opts := selfupdate.Options{DiscoveryURL: overrideDiscoveryURL, HTTPClient: overrideClient}
	if overridePubKey != nil {
		opts.PubKey = ed25519.PublicKey(overridePubKey)
	} else {
		opts.PubKey = selfupdate.PubKey
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
//
// Discovery reads ONLY the site's index.json. The GitHub API is not used.
// MAGUS_UPDATE_URL overrides the discovery URL (e.g. for organisations that
// self-host the site as a private update channel). If the index is unreachable,
// the command fails with a clear error - there is no silent fallback.
//
// Downgrade/freeze protection: moving to a lower semver than the running binary
// is refused unless --version is given explicitly (explicit opt-in) or --force
// is set. When --version is omitted, the newest non-yanked release from the
// index is used. A dev build (version == "unknown") has no baseline to compare
// against, so it also requires --version or --force before auto-selecting a
// release.
func selfUpdateCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("self update", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus self update [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Download the latest magus release, verify its Ed25519 signature and")
		fmt.Fprintln(os.Stderr, "SHA-256 hash, then atomically replace the running binary.")
		fmt.Fprintln(os.Stderr, "Without --bin-dir the running binary is replaced in place.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Discovery reads the release index at the site's public/release/index.json.")
		fmt.Fprintln(os.Stderr, "Override with MAGUS_UPDATE_URL to use a private update channel.")
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

	// Fetch and verify the release index. Fails closed on unreachable or bad sig.
	idx, err := selfupdate.FetchAndVerifyIndex(ctx, opts)
	if err != nil {
		return fmt.Errorf("fetch release index: %w", err)
	}

	rel, err := selfupdate.SelectRelease(idx, targetVer)
	if err != nil {
		return err
	}

	if checkOnly {
		selfupdate.PrintUpdateStatus(rel.Version, version)
		return nil
	}

	// Downgrade/freeze protection.
	// --version is an explicit request; allow downgrade only with --version or --force.
	// Without --version (auto-latest), never silently downgrade.
	if version == "unknown" {
		// Dev build: there is no running version to compare against, so
		// auto-latest could silently install anything, including an older
		// release mislabeled by a compromised or stale index. Require an
		// explicit choice instead of guessing.
		if !force && targetVer == "" {
			return errors.New(
				"running build is unversioned (dev build): refusing to auto-select a release\n" +
					"  use --version to install a specific release, or --force to proceed anyway",
			)
		}
	} else if !force {
		switch selfupdate.Compare(rel.Version, version) {
		case 0:
			return fmt.Errorf("already running %s (use --force to reinstall)", version)
		case -1:
			if targetVer == "" {
				// Auto-latest is below running: refuse unconditionally unless forced.
				return fmt.Errorf(
					"index advertises %s but you are running %s - refusing downgrade\n"+
						"  use --version %s to install a specific older release, or --force to override",
					rel.Version, version, rel.Version,
				)
			}
			// Explicit --version downgrade: still require --force.
			return fmt.Errorf(
				"target %s is older than current %s (use --force to allow downgrade)",
				rel.Version, version,
			)
		}
	}

	assetName := fmt.Sprintf("magus_%s_%s_%s.tar.gz", rel.Version, runtime.GOOS, runtime.GOARCH)
	assets, err := selfupdate.FindAssets(rel, assetName)
	if err != nil {
		return err
	}

	manifest, err := selfupdate.FetchAndVerifyManifest(ctx, assets.Sums, assets.Sig, opts)
	if err != nil {
		return fmt.Errorf("manifest verification failed: %w", err)
	}
	if manifest.Version != rel.Version {
		// assetName was built from rel.Version; the tarball's hash is only
		// trustworthy under the SHA256SUMS that was signed for that same
		// version. A mismatch means a stale or tampered index/manifest pair.
		return fmt.Errorf(
			"release index advertises %s but the signed SHA256SUMS manifest is for %s - refusing to install",
			rel.Version, manifest.Version,
		)
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
		fmt.Printf("dry-run: would install magus %s -> %s\n", manifest.Version, targetPath)
		return nil
	}

	if !yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("non-interactive terminal: use --yes / -y to confirm the update")
		}
		fmt.Printf("Install magus %s -> %s? [y/N] ", manifest.Version, targetPath)
		var answer string
		if _, err := fmt.Scanln(&answer); err != nil || strings.ToLower(strings.TrimSpace(answer)) != "y" {
			fmt.Fprintln(os.Stderr, "aborted")
			return nil //nolint:nilerr // Scanln failure (e.g. empty line) is treated as a declined prompt, not a fatal error
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
