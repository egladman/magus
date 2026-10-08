package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/spell"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serviceWorkspace hosts a script's leases on a broker stand-in that records every
// acquire and release, so no service process runs.
type serviceWorkspace struct {
	types.WorkspaceRepository
	root     string
	owned    bool
	acquired []string
	released []string
}

func (w *serviceWorkspace) Root() string { return w.root }

func (w *serviceWorkspace) ServiceSession() *service.Session {
	return service.NewSession(service.New(service.ExecRunner{}, 0),
		func(_ context.Context, key string, _ spells.Service) (bool, error) {
			w.acquired = append(w.acquired, key)
			return w.owned, nil
		},
		func(_ context.Context, key string) { w.released = append(w.released, key) },
	)
}

// TestServiceLeasesThroughBuzzScript drives acquire and release from Buzz, and pins that
// a lease the script never released is released when its scope ends.
func TestServiceLeasesThroughBuzzScript(t *testing.T) {
	ws := &serviceWorkspace{root: t.TempDir(), owned: true}
	ctx, scope := service.WithScope(types.WithWorkspace(t.Context(), ws))
	sess := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModules(ctx, sess)
	DeclareMagusTypes(sess)
	RegisterMagusNamespace(ctx, sess)
	require.NoError(t, sess.Exec(ctx, `
import "magus";

export fun hold() > str !> any {
    final early: magus\ServiceLease = magus\service.acquire("podman", op: "machine");
    magus\service.release(early);
    final kept: magus\ServiceLease = magus\service.acquire("podman", op: "machine");
    return "{kept.owned} {kept.brokered} {kept.idle}";
}
`))
	got, err := sess.CallValue(ctx, sess.Exports()["hold"], nil)
	require.NoError(t, err)
	assert.Equal(t, "true true 30m", got.AsString())

	key := service.Key(ws.root, *spell.Builtins()["podman"].Ops["machine"].Service)
	assert.Equal(t, []string{key, key}, ws.acquired)
	assert.Equal(t, []string{key}, ws.released, "the explicit release")

	scope.ReleaseAll(context.Background())
	assert.Equal(t, []string{key, key}, ws.released, "the script's end releases the lease it kept")
}

func TestServiceAcquireRefusals(t *testing.T) {
	ws := &serviceWorkspace{root: t.TempDir()}
	scoped, _ := service.WithScope(types.WithWorkspace(context.Background(), ws))
	tests := []struct {
		name    string
		ctx     context.Context
		spell   string
		op      string
		wantErr string
	}{
		{"a magusfile has no scope", types.WithWorkspace(context.Background(), ws), "podman", "machine",
			"only a `magus buzz` script holds a service this way; a magusfile target holds one through ctx.needs"},
		{"no workspace", func() context.Context { c, _ := service.WithScope(context.Background()); return c }(), "podman", "machine",
			"no workspace on the context"},
		{"unknown spell", scoped, "nope", "machine", `there is no built-in spell "nope"`},
		{"a command op", scoped, "podman", "podman-build", `spell "podman" has no service op "podman-build" (its service ops: [machine])`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := acquireService(tt.ctx, tt.spell, tt.op)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
	assert.Empty(t, ws.acquired, "a refused acquire reaches no host")
}
