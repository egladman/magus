package client

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	magustypes "github.com/egladman/magus/types"
	magusmocks "github.com/egladman/magus/types/gen/mocks"
)

func TestOpenVCSNeedsARootABackendAndARemote(t *testing.T) {
	for _, args := range [][3]string{{"", "hg", "origin"}, {"/r", "", "origin"}, {"/r", "hg", ""}} {
		_, err := OpenVCS(t.Context(), args[0], args[1], args[2])
		require.EqualError(t, err, "opening a VCS needs a root, a backend name and a remote", "%q", args)
	}
}

// The queue's version control is the one it names. Before, MAGUS_VCS_ENABLED=false in the
// job's environment turned it off, and MAGUS_VCS_NAME swapped the backend.
func TestResolveVCSIgnoresMagussOwnVCSSettings(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "false")
	t.Setenv("MAGUS_VCS_NAME", "jj")
	drv, err := resolveVCS(t.Context(), t.TempDir(), "hg")
	require.NoError(t, err)
	require.NotNil(t, drv)
	assert.Equal(t, "hg", drv.Name())

	_, err = resolveVCS(t.Context(), t.TempDir(), "cvs")
	require.ErrorIs(t, err, magustypes.ErrVCSUnknown)
}

// Before, a backend declining the capabilities, a root that is no checkout, or a remote
// nobody configured surfaced on the first candidate rather than when the queue opened.
func TestCheckVCSRefusesWhatCannotServeTheQueue(t *testing.T) {
	for _, tc := range []struct {
		name        string
		checkouts   error
		remote      error
		wantErr     string
		wantErrIs   error
		readsRemote bool
	}{
		{name: "serves", readsRemote: true},
		{name: "declines checkouts", checkouts: errors.ErrUnsupported, wantErrIs: errors.ErrUnsupported},
		{name: "no checkout", checkouts: errors.New("not a repository"), wantErr: "/clone: not a repository"},
		{name: "no such remote", remote: magustypes.ErrVCSUnsupported, readsRemote: true, wantErr: `/clone: no remote named "origin" is configured`},
		{name: "remote read fails", remote: errors.New("boom"), readsRemote: true, wantErr: "/clone: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drv := magusmocks.NewMockVCSDriver(t)
			drv.EXPECT().Checkouts(mock.Anything, "/clone").Return(nil, tc.checkouts)
			if tc.readsRemote {
				drv.EXPECT().RemoteURL(mock.Anything, "/clone", "origin").Return("https://example.invalid/r.git", tc.remote)
			}
			err := checkVCS(t.Context(), drv, "/clone", "origin")
			switch {
			case tc.wantErrIs != nil:
				require.ErrorIs(t, err, tc.wantErrIs)
			case tc.wantErr != "":
				require.EqualError(t, err, tc.wantErr)
			default:
				require.NoError(t, err)
			}
		})
	}
}
