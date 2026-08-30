//go:build !noselfupdate

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/interactive/tty"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/egladman/magus/internal/selfupdate"
	minioselfupdate "github.com/minio/selfupdate"
)

// Overridable for tests (unexported; test files set them directly).
var (
	overrideKeys         selfupdate.Keyring
	overrideClient       *http.Client
	overrideDiscoveryURL string
)

func activeOpts() selfupdate.Options {
	opts := selfupdate.Options{DiscoveryURL: overrideDiscoveryURL, HTTPClient: overrideClient}
	if overrideKeys != nil {
		opts.Keys = overrideKeys
	} else {
		opts.Keys = selfupdate.ReleaseKeys
	}
	return opts
}

// selfUpdateCompiled is true when the binary includes self-update support
// (the default; disable with -tags noselfupdate), enabling `self update`.
const selfUpdateCompiled = true

// releaseArch returns the architecture token release assets are named with. That is
// runtime.GOARCH everywhere except 32-bit ARM, where the release ships one asset per
// GOARM level because neither binary serves the other's hardware. Go records GOARM in
// the build info, so a running binary can name its own asset without the magusfile
// having to stamp the level in via ldflags.
func releaseArch() string {
	var goarm string
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "GOARM" {
				goarm = s.Value
				break
			}
		}
	}
	return archToken(runtime.GOARCH, goarm)
}

// archToken is releaseArch's logic over explicit inputs, so the arm cases are testable
// from any host. goarm is the raw build-info value, which may be empty or carry
// options ("7,softfloat").
func archToken(goarch, goarm string) string {
	if goarch != "arm" {
		return goarch
	}
	// A release publishes at most two 32-bit ARM assets (.github/workflows/release.yaml,
	// which currently publishes neither), so only those two names may be requested.
	// Every other input - no recorded level, or a level such as 5 that the release does
	// not build - resolves to armv6, which also runs on ARMv7 hardware. Echoing the level
	// back as "armv5" would name an asset that does not exist, turning a working update
	// into a download failure.
	if level, _, _ := strings.Cut(goarm, ","); level == "7" {
		return "armv7"
	}
	return "armv6"
}

// selfUpdateCmd implements `magus self update`: atomically replaces the running
// binary with the latest (or a specified) release.
//
// Discovery reads ONLY the site's index.json. The GitHub API is not used.
// MAGUS_UPDATE_URL overrides the discovery URL (e.g. for organizations that
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
	bindDisplayFlags(fs)
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
	// -y and --uf.Yes are one switch, which the registry expresses with AliasOf.
	uf := gen.BindSelfUpdate(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if uf.Version != "" && !strings.HasPrefix(uf.Version, "v") {
		uf.Version = "v" + uf.Version
	}

	opts := activeOpts()

	// Fetch and verify the release index. Fails closed on unreachable or bad sig.
	idx, err := selfupdate.FetchAndVerifyIndex(ctx, opts)
	if err != nil {
		return fmt.Errorf("fetch release index: %w", err)
	}

	// Everything fetched from here on is verified against the ring MINUS whatever the
	// signed index revoked. A binary built trusting a key the publisher has since
	// revoked learns about it here and nowhere else.
	opts.Keys = opts.Keys.Without(idx.Revoked)

	rel, err := selfupdate.SelectRelease(idx, uf.Version)
	if err != nil {
		return err
	}

	if uf.Check {
		selfupdate.PrintUpdateStatus(rel.Version, version)
		return nil
	}

	// Downgrade/freeze protection.
	// --version is an explicit request; allow downgrade only with --version or --uf.Force.
	// Without --version (auto-latest), never silently downgrade.
	if version == "unknown" {
		// Dev build: there is no running version to compare against, so
		// auto-latest could silently install anything, including an older
		// release mislabeled by a compromised or stale index. Require an
		// explicit choice instead of guessing.
		if !uf.Force && uf.Version == "" {
			return errors.New(
				"running build is unversioned (dev build): refusing to auto-select a release\n" +
					"  use --version to install a specific release, or --uf.Force to proceed anyway",
			)
		}
	} else if !uf.Force {
		switch selfupdate.Compare(rel.Version, version) {
		case 0:
			return fmt.Errorf("magus self update: already running %s (use --uf.Force to reinstall)", version)
		case -1:
			if uf.Version == "" {
				// Auto-latest is below running: refuse unconditionally unless forced.
				return fmt.Errorf(
					"index advertises %s but you are running %s - refusing downgrade\n"+
						"  use --version %s to install a specific older release, or --uf.Force to override",
					rel.Version, version, rel.Version,
				)
			}
			// Explicit --version downgrade: still require --uf.Force.
			return fmt.Errorf(
				"target %s is older than current %s (use --uf.Force to allow downgrade)",
				rel.Version, version,
			)
		}
	}

	assetName := fmt.Sprintf("magus_%s_%s_%s_static.tar.gz", rel.Version, runtime.GOOS, releaseArch())
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

	targetPath, err := selfupdate.ResolveTargetPath(uf.BinDir)
	if err != nil {
		return err
	}

	if uf.DryRun {
		fmt.Printf("dry-run: would install magus %s -> %s\n", manifest.Version, targetPath)
		return nil
	}

	if !uf.Yes {
		if !tty.StdinIsTerminal() {
			return errors.New("non-interactive terminal: use --uf.Yes / -y to confirm the update")
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
