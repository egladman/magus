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

// retiredEnv maps a MAGUS_* variable magus no longer reads to the one that replaced it.
//
// MAGUS_DAEMON_SOCKET is absent on purpose: magus exports it to its own children, never a
// person, so the only way it is set is an older magus spawning this one, and a child that
// ignores it runs self-contained, which is correct against a parent it cannot adopt anyway.
var retiredEnv = map[string]string{
	"MAGUS_DAEMON_ENABLED":                       "MAGUS_SERVER_ENABLED",
	"MAGUS_DAEMON_ADDRESS":                       "MAGUS_SERVER_ADDRESS",
	"MAGUS_DAEMON_IDLE_TTL":                      "MAGUS_SERVER_IDLE_TTL",
	"MAGUS_DAEMON_WORKSPACES":                    "MAGUS_SERVER_WORKSPACES",
	"MAGUS_DAEMON_MAINTENANCE_ROTATE_ACTIVITIES": "MAGUS_SERVER_MAINTENANCE_ROTATE_ACTIVITIES",
	"MAGUS_DAEMON_MAINTENANCE_ROTATE_LOGS":       "MAGUS_SERVER_MAINTENANCE_ROTATE_LOGS",
	"MAGUS_DAEMON_MAINTENANCE_PRUNE_PRESERVED":   "MAGUS_SERVER_MAINTENANCE_PRUNE_PRESERVED",
	"MAGUS_DAEMON_MAINTENANCE_SYNC_GRAPH":        "MAGUS_SERVER_MAINTENANCE_SYNC_GRAPH",
	"MAGUS_DAEMON_MAINTENANCE_CHECK_REVIEW":      "MAGUS_SERVER_MAINTENANCE_CHECK_REVIEW",
	// Set and ignored, it would turn a sandbox somebody asked for off without a word.
	"MAGUS_SANDBOX_ENABLED": "MAGUS_SANDBOX",
}

// RetiredEnv returns an error naming each retired MAGUS_* variable getenv reports set,
// with the variable that replaced it, or nil when none is set. A retired variable is
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
			errs = append(errs, fmt.Errorf("%s was renamed to %s; magus no longer reads it", old, retiredEnv[old]))
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
