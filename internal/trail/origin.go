package trail

import (
	"context"
	"os"
	"os/user"
	"strconv"
	"sync"

	"github.com/egladman/magus/types"
)

// osAccount is read once: a process cannot change the account it runs as, and the
// lookup can cost a directory-service round trip.
var osAccount = sync.OnceValues(func() (name, uid string) {
	if u, err := user.Current(); err == nil {
		return u.Username, u.Uid
	}
	// The lookup can fail (no passwd entry in a container); the uid still cannot.
	if id := os.Getuid(); id >= 0 {
		return "", strconv.Itoa(id)
	}
	return "", ""
})

type entryPointKey struct{}

// ContextWithEntryPoint returns ctx recording e as where work under it entered magus. The
// innermost call wins, so a daemon handling an RPC call stamps rpc over daemon.
func ContextWithEntryPoint(ctx context.Context, e types.EntryPoint) context.Context {
	return context.WithValue(ctx, entryPointKey{}, e)
}

// EntryPointFromContext returns the entry point [ContextWithEntryPoint] recorded on ctx,
// or "" when none was.
func EntryPointFromContext(ctx context.Context) types.EntryPoint {
	e, _ := ctx.Value(entryPointKey{}).(types.EntryPoint)
	return e
}

// LocalOrigin is the part of an [types.Origin] this process can read for itself: the OS
// account it runs as, and the entry point ctx records. The host fields stay empty for the
// caller to fill from what the host delivered.
func LocalOrigin(ctx context.Context) types.Origin {
	name, uid := osAccount()
	return types.Origin{User: name, UID: uid, EntryPoint: EntryPointFromContext(ctx)}
}

// hookOrigin is the origin of an observation a host's hook delivered. An observation
// that names no entry point came through the hook, the only producer that omits it.
func hookOrigin(e types.EntryPoint, host, session, agent string) types.Origin {
	if e == "" {
		e = types.EntryPointHook
	}
	return types.Origin{EntryPoint: e, Host: host, Session: session, Agent: agent}
}

// StampOrigin returns o with the OS account this process runs as and, when o names none,
// the entry point ctx records. The account is never taken from o: it is the one field no
// caller can know better than the OS.
func StampOrigin(ctx context.Context, o types.Origin) types.Origin {
	local := LocalOrigin(ctx)
	o.User, o.UID = local.User, local.UID
	if o.EntryPoint == "" {
		o.EntryPoint = local.EntryPoint
	}
	return o
}
