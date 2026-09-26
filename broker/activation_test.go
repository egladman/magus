package broker

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// activatedEnv makes the test binary a socket-activated broker: it adopts descriptor 3
// for the address the variable names and serves until shut down.
const activatedEnv = "MAGUS_BROKER_TEST_ACTIVATED"

// serveActivated is the child half of the socket-activation test. It sets LISTEN_PID
// itself because a supervisor sets it after fork, when the child's pid is known.
func serveActivated(addr string) int {
	_ = os.Setenv(envListenPID, strconv.Itoa(os.Getpid()))
	ln, err := Activated(addr)
	if err != nil || ln == nil {
		fmt.Fprintln(os.Stderr, "activation:", ln, err)
		return 2
	}
	for _, k := range []string{envListenPID, envListenFDs, envListenFDNames} {
		if _, ok := os.LookupEnv(k); ok {
			fmt.Fprintln(os.Stderr, k, "is still set, so a service the broker starts inherits it")
			return 3
		}
	}
	if err := Serve(context.Background(), ln, WithIdleExit(0)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	return 0
}

func TestActivationListenFDsValidatesStrictly(t *testing.T) {
	const pid = 4242
	for _, tc := range []struct {
		name    string
		env     map[string]string
		want    int
		wantErr bool
	}{
		{name: "nothing handed over", env: map[string]string{}},
		{name: "one socket", env: map[string]string{envListenPID: "4242", envListenFDs: "1"}, want: 1},
		{name: "one named socket", env: map[string]string{envListenPID: "4242", envListenFDs: "1", envListenFDNames: "magus-broker.socket"}, want: 1},
		{name: "addressed to another process", env: map[string]string{envListenPID: "7", envListenFDs: "1"}},
		{name: "a count with no pid", env: map[string]string{envListenFDs: "1"}, wantErr: true},
		{name: "a pid with no count", env: map[string]string{envListenPID: "4242"}, wantErr: true},
		{name: "a pid that is not one", env: map[string]string{envListenPID: "me", envListenFDs: "1"}, wantErr: true},
		{name: "a negative pid", env: map[string]string{envListenPID: "-4242", envListenFDs: "1"}, wantErr: true},
		{name: "a count that is not one", env: map[string]string{envListenPID: "4242", envListenFDs: "one"}, wantErr: true},
		{name: "zero sockets", env: map[string]string{envListenPID: "4242", envListenFDs: "0"}, wantErr: true},
		{name: "two sockets", env: map[string]string{envListenPID: "4242", envListenFDs: "2"}, wantErr: true},
		{name: "names for two sockets", env: map[string]string{envListenPID: "4242", envListenFDs: "1", envListenFDNames: "a:b"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(k string) (string, bool) { v, ok := tc.env[k]; return v, ok }
			n, err := listenFDs(lookup, pid)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, n)
		})
	}
}

func TestActivationWithNothingHandedOverBindsNothing(t *testing.T) {
	for _, k := range []string{envListenPID, envListenFDs, envListenFDNames} {
		t.Setenv(k, "")
		require.NoError(t, os.Unsetenv(k))
	}
	ln, err := Activated(testAddr(t))
	require.NoError(t, err)
	assert.Nil(t, ln, "no handover means the broker binds its own socket")
}
