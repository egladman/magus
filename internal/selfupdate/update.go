//go:build !noselfupdate

package selfupdate

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/egladman/magus/types"
	minioselfupdate "github.com/minio/selfupdate"
)

// Overridable for tests (unexported; test files set them directly).
var (
	overrideKeys         Keyring
	overrideClient       *http.Client
	overrideDiscoveryURL string
)

func activeOptions() Options {
	opts := Options{DiscoveryURL: overrideDiscoveryURL, HTTPClient: overrideClient}
	if overrideKeys != nil {
		opts.Keys = overrideKeys
	} else {
		opts.Keys = ReleaseKeys()
	}
	return opts
}

// DiscoveryHost returns the hostname a release index fetch will use, mirroring
// Options.discoveryURL's precedence (explicit opts.DiscoveryURL, then
// MAGUS_UPDATE_URL, then the default site) so the confirmation prompt names the
// actual source instead of assuming GitHub, which only ever hosts the artifacts.
func DiscoveryHost(opts Options) string {
	raw := opts.DiscoveryURL
	if raw == "" {
		raw = os.Getenv("MAGUS_UPDATE_URL")
	}
	if raw == "" {
		raw = DefaultDiscoveryURL
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

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
	// Every other input (no recorded level, or a level such as 5 that the release does
	// not build) resolves to armv6, which also runs on ARMv7 hardware. Echoing the level
	// back as "armv5" would name an asset that does not exist, turning a working update
	// into a download failure.
	if level, _, _ := strings.Cut(goarm, ","); level == "7" {
		return "armv7"
	}
	return "armv6"
}

// The flags `magus self update` binds, as the errors that name a remedy spell them.
const (
	flagVersion = "version"
	flagForce   = "force"
)

// What an Update did.
const (
	OutcomeChecked   = "checked"
	OutcomeDryRun    = "dry-run"
	OutcomeInstalled = "installed"
)

// Request is one `magus self update`.
type Request struct {
	// Current is the running build's version; "unknown" is a dev build.
	Current string
	// Version names the release to install; empty selects the newest stable one.
	Version string
	BinDir  string
	// Check stops after choosing a release; DryRun stops after verifying it.
	Check, DryRun bool
	// Force allows a downgrade, a reinstall and an unversioned build's auto-select.
	Force bool
	// Confirm is asked to approve installing version from host over target, once
	// everything is verified and before the binary is touched.
	Confirm func(version, host, target string) error
}

// Result is what an Update did and to which release.
type Result struct {
	Current string
	Latest  string
	Path    string
	Outcome string
}

// Update implements `magus self update`: atomically replaces the running
// binary with the latest (or a specified) release.
//
// Discovery reads ONLY the site's index.json. The GitHub API is not used.
// MAGUS_UPDATE_URL overrides the discovery URL (e.g. for organizations that
// self-host the site as a private update channel). If the index is unreachable,
// the command fails with a clear error; there is no silent fallback.
//
// Downgrade/freeze protection: moving to a lower semver than the running binary
// is refused unless a version is named explicitly (explicit opt-in) or Force
// is set. When no version is named, the newest non-yanked stable release from
// the index is used; a prerelease installs only when Version names it. A dev build (version == "unknown") has no baseline to compare
// against, so it also requires Version or Force before auto-selecting a
// release.
func Update(ctx context.Context, req Request) (Result, error) {
	current := req.Current
	if req.Version != "" && !strings.HasPrefix(req.Version, "v") {
		req.Version = "v" + req.Version
	}

	opts := activeOptions()

	// Fetch and verify the release index. Fails closed on unreachable or bad sig.
	idx, err := FetchAndVerifyIndex(ctx, opts)
	if err != nil {
		return Result{}, fmt.Errorf("fetch release index: %w", err)
	}

	// Everything fetched from here on is verified against the ring MINUS whatever the
	// signed index revoked. A binary built trusting a key the publisher revoked learns
	// about it here and nowhere else.
	opts.Keys = opts.Keys.Without(idx.Revoked)

	rel, err := SelectRelease(idx, req.Version)
	if err != nil {
		return Result{}, err
	}

	if req.Check {
		return Result{Current: current, Latest: rel.Version, Outcome: OutcomeChecked}, nil
	}

	// Downgrade/freeze protection.
	// An explicit version is a request; allow downgrade only with it or Force.
	// Without one (auto-latest), never silently downgrade.
	if current == "unknown" {
		// Dev build: there is no running version to compare against, so
		// auto-latest could silently install anything, including an older
		// release mislabeled by a compromised or stale index. Require an
		// explicit choice instead of guessing.
		if !req.Force && req.Version == "" {
			return Result{}, fmt.Errorf(
				"running build is unversioned (dev build): refusing to auto-select a release, "+
					"use --%s to install a specific release, or --%s to proceed anyway",
				flagVersion, flagForce,
			)
		}
	} else if !req.Force {
		switch Compare(rel.Version, current) {
		case 0:
			return Result{}, fmt.Errorf("magus self update: already running %s (use --%s to reinstall)", current, flagForce)
		case -1:
			if req.Version == "" {
				// Auto-latest is below running: refuse unconditionally unless forced.
				if _, stable := types.ParseVersion(current); !stable {
					return Result{}, fmt.Errorf(
						"you are running prerelease %s and automatic updates follow stable releases only, "+
							"the newest stable release is %s, "+
							"name a later prerelease with --%s, or use --%s %s --%s to return to stable",
						current, rel.Version, flagVersion,
						flagVersion, rel.Version, flagForce,
					)
				}
				return Result{}, fmt.Errorf(
					"index advertises %s but you are running %s: refusing downgrade, "+
						"use --%s %s to install a specific older release, or --%s to override",
					rel.Version, current, flagVersion, rel.Version, flagForce,
				)
			}
			// Explicit version downgrade: still require Force.
			return Result{}, fmt.Errorf(
				"target %s is older than current %s (use --%s to allow downgrade)",
				rel.Version, current, flagForce,
			)
		}
	}

	assetName := fmt.Sprintf("magus_%s_%s_%s_static.tar.gz", rel.Version, runtime.GOOS, releaseArch())
	assets, err := FindAssets(rel, assetName)
	if err != nil {
		return Result{}, err
	}

	manifest, err := FetchAndVerifyManifest(ctx, assets.Sums, assets.Sig, opts)
	if err != nil {
		return Result{}, fmt.Errorf("manifest verification failed: %w", err)
	}
	if manifest.Version != rel.Version {
		// assetName was built from rel.Version; the tarball's hash is only
		// trustworthy under the SHA256SUMS that was signed for that same
		// version. A mismatch means a stale or tampered index/manifest pair.
		return Result{}, fmt.Errorf(
			"release index advertises %s but the signed SHA256SUMS manifest is for %s: refusing to install",
			rel.Version, manifest.Version,
		)
	}

	binary, err := FetchAndVerifyTarball(ctx, assets.Tarball, assetName, manifest, opts)
	if err != nil {
		return Result{}, fmt.Errorf("tarball verification failed: %w", err)
	}

	targetPath, err := ResolveTargetPath(req.BinDir)
	if err != nil {
		return Result{}, err
	}

	if req.DryRun {
		return Result{Current: current, Latest: manifest.Version, Path: targetPath, Outcome: OutcomeDryRun}, nil
	}

	if err := req.Confirm(manifest.Version, DiscoveryHost(opts), targetPath); err != nil {
		return Result{}, err
	}

	if err := CheckWritable(targetPath); err != nil {
		return Result{}, err
	}

	if err := minioselfupdate.Apply(binary, minioselfupdate.Options{TargetPath: targetPath}); err != nil {
		return Result{}, fmt.Errorf("apply update: %w", err)
	}
	return Result{Current: current, Latest: manifest.Version, Path: targetPath, Outcome: OutcomeInstalled}, nil
}
