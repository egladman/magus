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
	return accountOf(os.Getuid(), user.LookupId, user.Current)
})

// accountOf names the account uid, looked up by id. user.Current is not used on Unix: its
// pure-Go fallback reads $USER when the passwd lookup fails, and any process can set that.
// A failed lookup records the uid alone. A uid below zero is Windows, where the account
// comes from the process token rather than the environment.
func accountOf(uid int, lookupID func(string) (*user.User, error), current func() (*user.User, error)) (name, id string) {
	if uid < 0 {
		if u, err := current(); err == nil {
			return u.Username, u.Uid
		}
		return "", ""
	}
	id = strconv.Itoa(uid)
	if u, err := lookupID(id); err == nil {
		return u.Username, id
	}
	return "", id
}

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

type credentialKey struct{}

// ContextWithCredential returns ctx recording name as the bearer credential the request
// presented. httpx's bearer guard is the one door that verifies a credential, and so the
// one caller that sets it; [StampOrigin] reads it onto every record made under ctx.
func ContextWithCredential(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, credentialKey{}, name)
}

// CredentialFromContext returns the name of the credential [ContextWithCredential]
// recorded on ctx, or "" when the request passed no bearer guard.
func CredentialFromContext(ctx context.Context) string {
	name, _ := ctx.Value(credentialKey{}).(string)
	return name
}

type hostKey struct{}

// ContextWithHost returns ctx recording host as the program the request came from, as it
// named itself. The MCP door sets it from the client's initialize handshake, the one
// place that name is known; [StampOrigin] reads it onto every record made under ctx.
func ContextWithHost(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, hostKey{}, host)
}

// HostFromContext returns the host [ContextWithHost] recorded on ctx, or "".
func HostFromContext(ctx context.Context) string {
	host, _ := ctx.Value(hostKey{}).(string)
	return host
}

// LocalOrigin is the part of an [types.Origin] this process can read for itself: the OS
// account it runs as, and the entry point, credential and host ctx records. Session and
// Agent stay empty for the caller to fill from what a host's hook delivered.
func LocalOrigin(ctx context.Context) types.Origin {
	name, uid := osAccount()
	return types.Origin{
		User: name, UID: uid,
		EntryPoint: EntryPointFromContext(ctx), Credential: CredentialFromContext(ctx), Host: HostFromContext(ctx),
	}
}

// hookOrigin is the origin of an observation a host's hook delivered. An observation
// that names no entry point came through the hook, the only producer that omits it.
func hookOrigin(o types.Origin) types.Origin {
	if o.EntryPoint == "" {
		o.EntryPoint = types.EntryPointHook
	}
	return o
}

// StampOrigin returns o with the OS account this process runs as and, where o names none,
// the entry point, credential and host ctx records. The account is never taken from o: it
// is the one field no caller can know better than the OS.
func StampOrigin(ctx context.Context, o types.Origin) types.Origin {
	local := LocalOrigin(ctx)
	o.User, o.UID = local.User, local.UID
	if o.EntryPoint == "" {
		o.EntryPoint = local.EntryPoint
	}
	if o.Credential == "" {
		o.Credential = local.Credential
	}
	if o.Host == "" {
		o.Host = local.Host
	}
	return o
}
