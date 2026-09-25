package run

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/types"
)

// drainJobserver closes the write end and counts what is left in the pipe.
func drainJobserver(t *testing.T, j *Jobserver) string {
	t.Helper()
	require.NoError(t, j.w.Close())
	got, err := io.ReadAll(j.r)
	require.NoError(t, err)
	require.NoError(t, j.r.Close())
	return string(got)
}

func TestOpenJobserverPreloadsOneTokenPerSlotBeyondTheFirst(t *testing.T) {
	if !jobserverSupported(t) {
		return
	}
	for _, tc := range []struct {
		slots, tokens int
	}{
		{slots: 2, tokens: 1},
		{slots: 8, tokens: 7},
		{slots: 4096, tokens: maxJobserverTokens},
	} {
		j, err := OpenJobserver(tc.slots)
		require.NoError(t, err)
		assert.Equal(t, tc.tokens, j.tokens)
		assert.Equal(t, strings.Repeat("+", tc.tokens), drainJobserver(t, j), "slots=%d", tc.slots)
	}
}

func TestOpenJobserverRefusesAPoolWithNoTokens(t *testing.T) {
	for _, slots := range []int{-1, 0, 1} {
		j, err := OpenJobserver(slots)
		require.Error(t, err, "slots=%d", slots)
		assert.Nil(t, j)
	}
}

func TestJobserverCloseIsSafeOnNil(t *testing.T) {
	var j *Jobserver
	assert.NoError(t, j.Close())
}

func TestJobserverMakeflagsReplacesOnlyTheJobFlags(t *testing.T) {
	const pool = "-j --jobserver-fds=3,4 --jobserver-auth=3,4"
	j := &Jobserver{}
	for _, tc := range []struct {
		name, inherited, want string
	}{
		{name: "empty", inherited: "", want: pool},
		{name: "make 4.4 fifo", inherited: "k -j8 --jobserver-auth=fifo:/tmp/GMfifo1", want: "k " + pool},
		{name: "make 3.81", inherited: " --jobserver-fds=5,6 -j", want: pool},
		{name: "long forms", inherited: "--jobs=4 --jobserver-style=pipe --no-print-directory", want: "--no-print-directory " + pool},
		{name: "variables stay last", inherited: "s -j2 -- CC=clang CFLAGS=-j9", want: "s " + pool + " -- CC=clang CFLAGS=-j9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, j.makeflags(tc.inherited, firstExtraFD))
		})
	}
}

func TestJobserverEnvironReadsTheEffectiveMakeflags(t *testing.T) {
	j := &Jobserver{}
	got := j.environ([]string{"MAKEFLAGS=-j2", "PATH=/bin", "MAKEFLAGS=k"}, firstExtraFD)
	assert.Equal(t, []string{
		"MAKEFLAGS=k -j --jobserver-fds=3,4 --jobserver-auth=3,4",
		"CARGO_MAKEFLAGS=-j --jobserver-fds=3,4 --jobserver-auth=3,4",
	}, got)
}

func TestChildEnvCarriesTheJobserverUnderTheTargetsOverrides(t *testing.T) {
	ctx := WithJobserver(context.Background(), &Jobserver{})

	env, _ := childEnv(ctx, nil, nil)
	assert.Equal(t, "-j --jobserver-fds=3,4 --jobserver-auth=3,4", lookupEnv(env, "CARGO_MAKEFLAGS"))
	assert.Contains(t, lookupEnv(env, "MAKEFLAGS"), "--jobserver-auth=3,4")

	env, _ = childEnv(ctx, nil, []string{"MAKEFLAGS=-j1"})
	assert.Equal(t, "-j1", lookupEnv(env, "MAKEFLAGS"), "a target's own MAKEFLAGS wins")
}

// A child the sandbox launcher starts gets the pipe after the ruleset's slot, which the
// launcher closes, and its MAKEFLAGS has to say so or make reads a closed descriptor.
func TestChildEnvNamesTheJobserverWhereTheLauncherPutsIt(t *testing.T) {
	p := sandbox.BuildPolicy(sandbox.PolicyOptions{Mode: types.SandboxModeBestEffort, Workspace: t.TempDir()})
	ctx := WithJobserver(context.Background(), &Jobserver{})
	env, _ := childEnv(ctx, p, nil)

	want := "-j --jobserver-fds=3,4 --jobserver-auth=3,4"
	if confined, _ := p.KernelConfines(); confined {
		want = "-j --jobserver-fds=4,5 --jobserver-auth=4,5"
	}
	assert.Equal(t, want, lookupEnv(env, "CARGO_MAKEFLAGS"))
	assert.Equal(t, 4, jobserverFD(true), "the launcher's ruleset takes descriptor 3")
	assert.Equal(t, 3, jobserverFD(false))
}

func TestSeatJobserverSizesThePoolToTheSlots(t *testing.T) {
	outer := &Jobserver{}
	ctx := WithJobserver(context.Background(), outer)

	one, closeOne, err := SeatJobserver(ctx, 1)
	require.NoError(t, err)
	closeOne()
	assert.Nil(t, jobserverFrom(one), "a one-slot seat withdraws the pool it inherited")

	if !jobserverSupported(t) {
		return
	}
	four, closeFour, err := SeatJobserver(ctx, 4)
	require.NoError(t, err)
	j := jobserverFrom(four)
	require.NotNil(t, j)
	assert.NotSame(t, outer, j)
	assert.Equal(t, 3, j.tokens)
	closeFour()
	_, err = j.w.Write([]byte{jobserverToken})
	assert.Error(t, err, "closing the seat closes the pipe")
}

// jobserverSupported reports whether this platform can open a pool, logging why a
// test stops early when it cannot.
func jobserverSupported(t *testing.T) bool {
	t.Helper()
	j, err := OpenJobserver(2)
	if errors.Is(err, errors.ErrUnsupported) {
		t.Log("no pipe jobserver on this platform; the pool assertions do not apply")
		return false
	}
	require.NoError(t, err)
	require.NoError(t, j.Close())
	return true
}
