package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordRunner is a service.Runner that records starts without forking a process.
type recordRunner struct{ started int }

func (r *recordRunner) Start(context.Context, types.Service) (service.Handle, error) {
	r.started++
	return struct{}{}, nil
}
func (r *recordRunner) Stop(service.Handle) {}

func serviceOp() types.SpellOp {
	// bin "true" exits 0, so the non-supervised fall-through fork is harmless.
	return types.SpellOp{
		Kind:    types.OpKindService,
		Command: types.Command{Bin: "true"},
		Service: &types.Service{Command: types.Command{Bin: "true"}},
	}
}

// TestRunCommandSupervisesServiceDependency proves runCommand routes a service op to
// the supervisor (not a foreground fork) when supervision is active - the case of a
// service reached via magus.needs.
func TestRunCommandSupervisesServiceDependency(t *testing.T) {
	rr := &recordRunner{}
	sess := service.NewSession(service.New(rr, 0), nil, nil)
	ctx := service.WithSupervision(service.WithSession(context.Background(), sess))

	_, err := runCommand(ctx, serviceOp(), commandOpts{})
	require.NoError(t, err)
	assert.Equal(t, 1, rr.started, "service dependency should be supervised, not forked")
}

// TestRunCommandForegroundsDirectService proves a service op with no active
// supervision falls through to a real fork (the directly-run case), and is not
// handed to the supervisor.
func TestRunCommandForegroundsDirectService(t *testing.T) {
	rr := &recordRunner{}
	sess := service.NewSession(service.New(rr, 0), nil, nil)
	// Session present but supervision NOT active: this is a directly-run service.
	ctx := service.WithSession(context.Background(), sess)

	_, err := runCommand(ctx, serviceOp(), commandOpts{})
	require.NoError(t, err) // `true` exits 0
	assert.Equal(t, 0, rr.started, "directly-run service must foreground, not be supervised")
}
