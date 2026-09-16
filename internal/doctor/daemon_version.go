package doctor

import (
	"fmt"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// checkDaemonVersion fails when the daemon answering this workspace is a different build
// from the binary asking.
//
// IT FAILS RATHER THAN ADVISES, on the rule this repository states for every
// misconfiguration: the value is not honored. A daemon of another vintage decodes every
// record with a struct that does not know this build's fields, drops what it does not
// know, and writes the whole file back; two live job rows lost their lanes that way on
// 2026-09-11, silently, while both sides looked healthy.
//
// The predicate carries no judgment: two version strings match or they do not. For a
// normal install both sides are one binary and this never fires, so it is not an advisory
// competing for attention and uptake is the wrong measure of it.
func (r *runner) checkDaemonVersion() types.DoctorCheck {
	const name = "daemon-version"

	di := r.opts.daemonInfo
	switch {
	case di == nil || !di.Reachable || !di.Persistent:
		// A PERSISTENT daemon only. magus adopts a per-process proc server for ordinary
		// commands, and that one is this very binary, so comparing against it would always
		// match and would hide exactly the case this check exists for.
		return types.DoctorCheck{
			Name: name, Status: types.DoctorOK, Evidence: types.EvidenceUnknown,
			Message: "no persistent daemon answered, so nothing here is served by another build",
		}
	case di.DaemonVersion == "" || di.ClientVersion == "":
		return types.DoctorCheck{
			Name: name, Status: types.DoctorOK, Evidence: types.EvidenceUnknown,
			Message: "one side did not report a version, so the two cannot be compared",
		}
	case di.DaemonVersion == di.ClientVersion:
		return types.DoctorCheck{
			Name: name, Status: types.DoctorOK,
			Message: fmt.Sprintf("the daemon serving this workspace is this build (%s)", di.ClientVersion),
		}
	}

	details := []string{
		"every call through that daemon is answered by the other build, which decodes what it knows and writes back the rest without it",
	}
	for _, ws := range di.Workspaces {
		details = append(details, "it is serving "+ws.Root)
	}
	details = append(details,
		fmt.Sprintf("restart it to pick up this build: `%s` then `%s`", hint.ServerStop, hint.ServerStart),
		"it may be serving other workspaces, which stop for them too",
	)
	return types.DoctorCheck{
		Name:   name,
		Status: types.DoctorFail,
		Message: fmt.Sprintf("this magus is %s and the daemon serving it is %s (pid %d)",
			di.ClientVersion, di.DaemonVersion, di.ParentPID),
		Details: details,
	}
}
