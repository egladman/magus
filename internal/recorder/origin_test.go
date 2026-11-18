package recorder

import (
	"context"
	"errors"
	"os"
	"os/user"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
)

// Every event carries the OS account that wrote it, whatever the producer said, so a row
// with no host session still says whose account acted instead of being read as "a person".
func TestAppendStampsTheOSAccountAndTheEntryPoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := ContextWithEntryPoint(t.Context(), job.EntryPointCLI)

	AppendCall(ctx, dir, journal.Call{Kind: journal.CallKindJob, Name: "a", Origin: job.Origin{User: "forged", UID: "4242"}})
	AppendCall(ctx, dir, journal.Call{Kind: journal.CallKindJob, Name: "b", Origin: job.Origin{EntryPoint: job.EntryPointServer}})

	events, err := RecentCalls(t.Context(), dir, 2)
	require.NoError(t, err)
	require.Len(t, events, 2)
	local := LocalOrigin(ctx)
	require.NotEmpty(t, local.UID)
	if os.Getuid() >= 0 {
		assert.Equal(t, strconv.Itoa(os.Getuid()), local.UID)
	}
	byAction := map[string]job.Origin{}
	for _, e := range events {
		byAction[e.Name] = e.Origin
	}
	assert.Equal(t, job.Origin{User: local.User, UID: local.UID, EntryPoint: job.EntryPointCLI}, byAction["a"],
		"the account is the OS's answer, never the producer's, and the entry point comes from ctx")
	assert.Equal(t, job.Origin{User: local.User, UID: local.UID, EntryPoint: job.EntryPointServer}, byAction["b"],
		"a producer that names its entry point keeps it")
}

// The credential a bearer guard verified rides the context and is stamped on every record
// made under it, so no handler copies it by hand.
func TestStampOriginReadsTheCredentialFromTheContext(t *testing.T) {
	t.Parallel()
	cred := auth.Credential{Kind: auth.KindStored, ID: "3fa9c1d2", Name: "console-1", Grant: auth.AccessConsole()}
	ctx := ContextWithCredential(ContextWithEntryPoint(t.Context(), job.EntryPointRPC), cred)

	got := StampOrigin(ctx, job.Origin{Host: "console"})
	local := LocalOrigin(ctx)
	assert.Equal(t, job.Origin{
		User: local.User, UID: local.UID, EntryPoint: job.EntryPointRPC, Host: "console", Credential: cred.Recorded(),
	}, got, "the origin records the credential without its grant")
	share := job.Credential{Kind: auth.KindShare, ID: "9b2e04aa"}
	assert.Equal(t, share, StampOrigin(ctx, job.Origin{Credential: share}).Credential, "a named credential is kept")
	assert.Zero(t, StampOrigin(t.Context(), job.Origin{}).Credential, "no guard, no credential")
}

// A failed passwd lookup records the uid alone. user.Current's pure-Go fallback reads
// $USER, which any process sets, so it would put a forged name on every record.
func TestAccountOfNeverReadsTheEnvironment(t *testing.T) {
	t.Setenv("USER", "forged")
	failing := func(string) (*user.User, error) { return nil, errors.New("no passwd entry") }
	current := func() (*user.User, error) { return &user.User{Username: "forged", Uid: "0"}, nil }

	name, uid := lookupAccount(4242, failing, current)
	assert.Empty(t, name)
	assert.Equal(t, "4242", uid)

	found := func(id string) (*user.User, error) { return &user.User{Username: "eli", Uid: id}, nil }
	name, uid = lookupAccount(501, found, current)
	assert.Equal(t, "eli", name)
	assert.Equal(t, "501", uid)
}

func TestEntryPointFromContextIsEmptyUntilRecorded(t *testing.T) {
	t.Parallel()
	assert.Empty(t, EntryPointFromContext(context.Background()))
	ctx := ContextWithEntryPoint(context.Background(), job.EntryPointServer)
	assert.Equal(t, job.EntryPointRPC, EntryPointFromContext(ContextWithEntryPoint(ctx, job.EntryPointRPC)), "the innermost entry point wins")
}
