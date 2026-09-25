package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// retiredKeys maps a top-level magus.yaml key magus no longer reads to the key that took
// its settings. Every setting the daemon block held belongs to `magus server` now.
var retiredKeys = map[string]string{"daemon": "server"}

// retiredVar is a MAGUS_* variable magus once read and no longer does.
type retiredVar struct {
	release     string // the release that stopped reading it
	replacement string // the variable that took its value, or "" when nothing did
	instead     string // what to do when there is no replacement
}

// retiredEnv holds every MAGUS_* variable magus stopped reading, so the startup error can
// say what happened to it instead of only that it is unknown.
//
// MAGUS_DAEMON_SOCKET is absent on purpose: see inheritedEnv.
var retiredEnv = map[string]retiredVar{
	"MAGUS_ASSUME_INTERACTIVE": {release: "v0.4.2", instead: "delete it; x and tail need a real terminal"},
	"MAGUS_NO_WAIT":            {release: "v0.5.0", instead: "delete it; magus never waits on another invocation, a held lock or budget refuses at once with exit 75"},

	"MAGUS_DAEMON_ENABLED":                       {release: "v0.5.0", replacement: "MAGUS_SERVER_ENABLED"},
	"MAGUS_DAEMON_ADDRESS":                       {release: "v0.5.0", replacement: "MAGUS_SERVER_ADDRESS"},
	"MAGUS_DAEMON_IDLE_TTL":                      {release: "v0.5.0", replacement: "MAGUS_SERVER_IDLE_TTL"},
	"MAGUS_DAEMON_WORKSPACES":                    {release: "v0.5.0", replacement: "MAGUS_SERVER_WORKSPACES"},
	"MAGUS_DAEMON_MAINTENANCE_ROTATE_ACTIVITIES": {release: "v0.5.0", replacement: "MAGUS_SERVER_MAINTENANCE_ROTATE_ACTIVITIES"},
	"MAGUS_DAEMON_MAINTENANCE_ROTATE_LOGS":       {release: "v0.5.0", replacement: "MAGUS_SERVER_MAINTENANCE_ROTATE_LOGS"},
	"MAGUS_DAEMON_MAINTENANCE_PRUNE_PRESERVED":   {release: "v0.5.0", replacement: "MAGUS_SERVER_MAINTENANCE_PRUNE_PRESERVED"},
	"MAGUS_DAEMON_MAINTENANCE_SYNC_GRAPH":        {release: "v0.5.0", replacement: "MAGUS_SERVER_MAINTENANCE_SYNC_GRAPH"},
	"MAGUS_DAEMON_MAINTENANCE_CHECK_REVIEW":      {release: "v0.5.0", replacement: "MAGUS_SERVER_MAINTENANCE_CHECK_REVIEW"},
}

func (r retiredVar) describe(name string) string {
	if r.replacement != "" {
		return fmt.Sprintf("%s was renamed to %s in %s; magus no longer reads it", name, r.replacement, r.release)
	}
	return fmt.Sprintf("%s was removed in %s; %s", name, r.release, r.instead)
}

// RetiredEnv returns an error naming each retired MAGUS_* variable getenv reports set,
// with what replaced it, or nil when none is set. A retired variable is
// misconfiguration rather than something to skip: magus would not honor its value, and
// a setting that silently stops applying is the failure nobody notices.
func RetiredEnv(getenv func(string) string) error {
	names := make([]string, 0, len(retiredEnv))
	for old := range retiredEnv {
		names = append(names, old)
	}
	slices.Sort(names)
	var errs []error
	for _, old := range names {
		if getenv(old) != "" {
			errs = append(errs, errors.New(retiredEnv[old].describe(old)))
		}
	}
	return errors.Join(errs...)
}

// retiredKeyError returns the error for a dotted config key whose top-level segment magus
// retired, naming the key that replaced it, or nil when key is not retired.
func retiredKeyError(key string) error {
	head, rest, _ := strings.Cut(key, ".")
	renamed, ok := retiredKeys[head]
	if !ok {
		return nil
	}
	if rest != "" {
		renamed += "." + rest
	}
	return fmt.Errorf("config key %q was renamed to %q", key, renamed)
}
