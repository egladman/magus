// Package client wires the merge queue to magus: [OpenVCS] resolves magus's version
// control backend, which implements every VCS capability the queue's steps take, and
// [Workspace] is the build tool's facts, through magus's Go SDK.
package client

import (
	"context"
	"errors"
	"fmt"

	magustypes "github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// OpenVCS resolves the backend named name for the clone at root and checks that it can
// serve the queue there and that remote is a remote the clone has configured. It reads
// neither MAGUS_VCS_ENABLED nor MAGUS_VCS_NAME: an environment that turns magus's own
// version control off, or points it at another backend, must not turn the queue's off
// with it.
func OpenVCS(ctx context.Context, root, name, remote string) (magustypes.VCSDriver, error) {
	if root == "" || name == "" || remote == "" {
		return nil, errors.New("opening a VCS needs a root, a backend name and a remote")
	}
	drv, err := resolveVCS(ctx, root, name)
	if err != nil {
		return nil, err
	}
	if err := checkVCS(ctx, drv, root, remote); err != nil {
		return nil, err
	}
	return drv, nil
}

// resolveVCS resolves the backend named name, whatever the environment says.
func resolveVCS(ctx context.Context, root, name string) (magustypes.VCSDriver, error) {
	enabled := true
	res, err := vcs.Resolve(ctx, root, "", magustypes.VCSOptions{Enabled: &enabled, Name: name})
	if err != nil {
		return nil, err
	}
	return res.VCS, nil
}

// checkVCS makes cheap reads, so a backend that declines what the queue needs, a root
// that is no checkout, or a remote nobody configured fails when opened, not mid-run.
func checkVCS(ctx context.Context, drv magustypes.VCSDriver, root, remote string) error {
	if _, err := drv.Checkouts(ctx, root); err != nil {
		return fmt.Errorf("%s: %w", root, err)
	}
	if _, err := drv.RemoteURL(ctx, root, remote); err != nil {
		if errors.Is(err, magustypes.ErrVCSUnsupported) {
			return fmt.Errorf("%s: no remote named %q is configured", root, remote)
		}
		return fmt.Errorf("%s: %w", root, err)
	}
	return nil
}
