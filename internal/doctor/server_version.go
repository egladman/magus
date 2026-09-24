package doctor

import (
	"fmt"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// checkServerVersion fails when the server answering this workspace is a different build
// from the binary asking.
//
// IT FAILS RATHER THAN ADVISES, on the rule this repository states for every
// misconfiguration: the value is not honored. A server of another vintage decodes every
// record with a struct that does not know this build's fields, drops what it does not
// know, and writes the whole file back; two live job rows lost their write paths that way on
// 2026-09-11, silently, while both sides looked healthy.
//
// The predicate carries no judgment: two version strings match or they do not. For a
// normal install both sides are one binary and this never fires, so it is not an advisory
// competing for attention and uptake is the wrong measure of it.
func (r *runner) checkServerVersion() types.Check {
	const name = "server-version"

	di := r.opts.serverInfo
	switch {
	case di == nil || !di.Reachable || !di.Persistent:
		// A PERSISTENT server only. magus adopts a per-process proc server for ordinary
		// commands, and that one is this very binary, so comparing against it would always
		// match and would hide exactly the case this check exists for.
		return types.Check{
			Name: name, Status: types.CheckOK, Evidence: types.EvidenceUnknown,
			Message: "no persistent server answered, so nothing here is served by another build",
		}
	case di.ServerVersion == "" || di.ClientVersion == "":
		return types.Check{
			Name: name, Status: types.CheckOK, Evidence: types.EvidenceUnknown,
			Message: "one side did not report a version, so the two cannot be compared",
		}
	case di.ServerVersion == di.ClientVersion:
		return types.Check{
			Name: name, Status: types.CheckOK,
			Message: fmt.Sprintf("the server serving this workspace is this build (%s)", di.ClientVersion),
		}
	}

	details := []string{
		"every call through that server is answered by the other build, which decodes what it knows and writes back the rest without it",
	}
	for _, ws := range di.Workspaces {
		details = append(details, "it is serving "+ws.Root)
	}
	details = append(details,
		fmt.Sprintf("restart it to pick up this build: `%s` then `%s`", hint.ServerStop, hint.ServerStart),
		"it may be serving other workspaces, which stop for them too",
	)
	return types.Check{
		Name:   name,
		Status: types.CheckFail,
		Message: fmt.Sprintf("this magus is %s and the server serving it is %s (pid %d)",
			di.ClientVersion, di.ServerVersion, di.ParentPID),
		Details: details,
	}
}
