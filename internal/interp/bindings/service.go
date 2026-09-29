package bindings

import (
	"context"
	"errors"
	"fmt"

	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/spell"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// serviceSessions is the workspace a script's leases are hosted through; *magus.Magus
// is it, routing to the broker exactly as a run does.
type serviceSessions interface {
	ServiceSession() *service.Session
}

// buildService assembles magus\service: a `magus buzz` script's hold on the shared
// services built-in spells declare. Hand-bound for the reason buildJob is. The leases
// ride the service.Scope the script's caller put on ctx and releases at its end.
func buildService(obs buzz.DirectObserver) vm.Value {
	ns := vm.NewMap()
	ns.MapSet("acquire", directVal(obs, "magus.service.acquire", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		lease, err := acquireService(ctx, ffi.Str(args, 0), ffi.Str(args, 1))
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return bindinggen.ObjectServiceLease(lease), nil
	}))
	ns.MapSet("release", directVal(obs, "magus.service.release", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		if err := releaseService(ctx, args); err != nil {
			return vm.Null, ffi.Error(err)
		}
		return vm.Null, nil
	}))
	return ns
}

// serviceScope is the script's scope and the workspace that opens its session.
func serviceScope(ctx context.Context, member string) (*service.Scope, types.WorkspaceRepository, serviceSessions, error) {
	sc := service.ScopeFrom(ctx)
	if sc == nil {
		return nil, nil, nil, types.DiagnosticErrorf(types.MagusfileOnlyMember,
			"magus\\service.%s: only a `magus buzz` script holds a service this way; a magusfile target holds one through ctx.needs", member)
	}
	ws := types.WorkspaceFromContext(ctx)
	host, ok := ws.(serviceSessions)
	if ws == nil || !ok {
		return nil, nil, nil, types.DiagnosticErrorf(types.MagusfileOnlyMember,
			"magus\\service.%s: no workspace on the context: the broker hosts a service for a workspace, so run the script from inside one", member)
	}
	return sc, ws, host, nil
}

func acquireService(ctx context.Context, spellName, op string) (types.ServiceLease, error) {
	sc, ws, host, err := serviceScope(ctx, "acquire")
	if err != nil {
		return types.ServiceLease{}, err
	}
	desc, ok := spell.Builtins()[spellName]
	if !ok {
		return types.ServiceLease{}, fmt.Errorf("magus\\service.acquire: there is no built-in spell %q", spellName)
	}
	o, ok := desc.Ops[types.Normalize(op)]
	if !ok || !o.IsService() || o.Service == nil {
		return types.ServiceLease{}, fmt.Errorf("magus\\service.acquire: spell %q has no service op %q (its service ops: %v)",
			spellName, op, desc.ServiceOpNames())
	}
	svc := *o.Service
	key := service.Key(ws.Root(), svc)
	owned, brokered, err := sc.Session(host.ServiceSession).Acquire(ctx, key, svc)
	if err != nil {
		return types.ServiceLease{}, fmt.Errorf("magus\\service.acquire %s:%s: %w", spellName, op, err)
	}
	return types.ServiceLease{Key: key, Owned: owned, Brokered: brokered, Idle: svc.Idle}, nil
}

func releaseService(ctx context.Context, args []vm.Value) error {
	sc, _, host, err := serviceScope(ctx, "release")
	if err != nil {
		return err
	}
	var key string
	if len(args) > 0 {
		if fields, ok := args[0].MapView(); ok {
			if v, ok := fields.MapGet("key"); ok && v.IsStr() {
				key = v.AsString()
			}
		}
	}
	if key == "" {
		return errors.New("magus\\service.release: expected the ServiceLease magus\\service\\acquire returned")
	}
	sc.Session(host.ServiceSession).Release(ctx, key)
	return nil
}
