package trail

import (
	"context"
	"errors"
	"os"
	"os/user"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// Every event carries the OS account that wrote it, whatever the producer said, so a row
// with no host session still says whose account acted instead of being read as "a person".
func TestAppendStampsTheOSAccountAndTheEntryPoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := ContextWithEntryPoint(t.Context(), types.EntryPointCLI)

	Append(ctx, dir, Event{Kind: KindJob, Action: "a", Origin: types.Origin{User: "forged", UID: "4242"}})
	Append(ctx, dir, Event{Kind: KindJob, Action: "b", Origin: types.Origin{EntryPoint: types.EntryPointDaemon}})

	events, err := ReadRecent(dir, 2)
	require.NoError(t, err)
	require.Len(t, events, 2)
	local := LocalOrigin(ctx)
	require.NotEmpty(t, local.UID)
	if os.Getuid() >= 0 {
		assert.Equal(t, strconv.Itoa(os.Getuid()), local.UID)
	}
	byAction := map[string]types.Origin{}
	for _, e := range events {
		byAction[e.Action] = e.Origin
	}
	assert.Equal(t, types.Origin{User: local.User, UID: local.UID, EntryPoint: types.EntryPointCLI}, byAction["a"],
		"the account is the OS's answer, never the producer's, and the entry point comes from ctx")
	assert.Equal(t, types.Origin{User: local.User, UID: local.UID, EntryPoint: types.EntryPointDaemon}, byAction["b"],
		"a producer that names its entry point keeps it")
}

// The credential a bearer guard verified rides the context and is stamped on every record
// made under it, so no handler copies it by hand.
func TestStampOriginReadsTheCredentialFromTheContext(t *testing.T) {
	t.Parallel()
	cred := types.Credential{Class: types.ClassStored, ID: "3fa9c1d2", Name: "console-1", Grant: types.GrantConsole}
	ctx := ContextWithCredential(ContextWithEntryPoint(t.Context(), types.EntryPointRPC), cred)

	got := StampOrigin(ctx, types.Origin{Host: "console"})
	assert.Equal(t, types.EntryPointRPC, got.EntryPoint)
	assert.Equal(t, cred, got.Credential)
	assert.Equal(t, "console", got.Host)
	share := types.Credential{Class: types.ClassShare, ID: "9b2e04aa", Grant: types.GrantViewer}
	assert.Equal(t, share, StampOrigin(ctx, types.Origin{Credential: share}).Credential, "a named credential is kept")
	assert.Zero(t, StampOrigin(t.Context(), types.Origin{}).Credential, "no guard, no credential")
}

// A failed passwd lookup records the uid alone. user.Current's pure-Go fallback reads
// $USER, which any process sets, so it would put a forged name on every record.
func TestAccountOfNeverReadsTheEnvironment(t *testing.T) {
	t.Setenv("USER", "forged")
	failing := func(string) (*user.User, error) { return nil, errors.New("no passwd entry") }
	current := func() (*user.User, error) { return &user.User{Username: "forged", Uid: "0"}, nil }

	name, uid := accountOf(4242, failing, current)
	assert.Empty(t, name)
	assert.Equal(t, "4242", uid)

	found := func(id string) (*user.User, error) { return &user.User{Username: "eli", Uid: id}, nil }
	name, uid = accountOf(501, found, current)
	assert.Equal(t, "eli", name)
	assert.Equal(t, "501", uid)
}

func TestEntryPointFromContextIsEmptyUntilRecorded(t *testing.T) {
	t.Parallel()
	assert.Empty(t, EntryPointFromContext(context.Background()))
	ctx := ContextWithEntryPoint(context.Background(), types.EntryPointDaemon)
	assert.Equal(t, types.EntryPointRPC, EntryPointFromContext(ContextWithEntryPoint(ctx, types.EntryPointRPC)), "the innermost entry point wins")
}
