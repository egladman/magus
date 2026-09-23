package trail

import (
	"context"
	"os"
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
	ctx := WithTransport(t.Context(), types.TransportCLI)

	Append(ctx, dir, Event{Kind: KindJob, Action: "a", Origin: types.Origin{User: "forged", UID: "4242"}})
	Append(ctx, dir, Event{Kind: KindJob, Action: "b", Origin: types.Origin{Transport: types.TransportDaemon}})

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
	assert.Equal(t, types.Origin{User: local.User, UID: local.UID, Transport: types.TransportCLI}, byAction["a"],
		"the account is the OS's answer, never the producer's, and the entry point comes from ctx")
	assert.Equal(t, types.Origin{User: local.User, UID: local.UID, Transport: types.TransportDaemon}, byAction["b"],
		"a producer that names its entry point keeps it")
}

func TestTransportFromIsEmptyUntilRecorded(t *testing.T) {
	t.Parallel()
	assert.Empty(t, TransportFrom(context.Background()))
	ctx := WithTransport(context.Background(), types.TransportDaemon)
	assert.Equal(t, types.TransportRPC, TransportFrom(WithTransport(ctx, types.TransportRPC)), "the innermost entry point wins")
}
