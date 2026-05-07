package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

// probeKind identifies which Kubernetes probe check to perform.
type probeKind int

const (
	probeLiveness  probeKind = iota // daemon answers the status RPC
	probeReadiness                  // daemon answers AND target workspace is loaded
)

// parseProbeKind converts a flag string value to a probeKind.
func parseProbeKind(s string) (probeKind, error) {
	switch s {
	case "liveness":
		return probeLiveness, nil
	case "readiness":
		return probeReadiness, nil
	default:
		return 0, fmt.Errorf("unknown probe kind %q: must be liveness or readiness", s)
	}
}

// evaluateHealth reports whether the daemon is healthy. root, if non-empty, pins readiness to a specific workspace.
func evaluateHealth(reply *types.StatusOutput, err error, kind probeKind, root string) (ok bool, reason string) {
	if err != nil || reply == nil {
		if err != nil {
			return false, fmt.Sprintf("daemon unreachable: %v", err)
		}
		return false, "daemon unreachable"
	}
	if kind == probeLiveness {
		return true, fmt.Sprintf("daemon pid %d is alive", reply.ParentPID)
	}
	// Readiness is a multi-workspace daemon concept — only the daemon reports
	// which workspaces are loaded. A per-process proc server never does, so
	// report that honestly instead of a misleading "no workspaces loaded".
	if reply.Mode == "proc" {
		return false, "daemon is in per-process mode; readiness requires `magus server start`"
	}
	if root != "" {
		clean := filepath.Clean(root)
		for _, ws := range reply.Workspaces {
			if filepath.Clean(ws.Root) == clean {
				return true, fmt.Sprintf("workspace %s is loaded", root)
			}
		}
		return false, fmt.Sprintf("workspace %s is not loaded", root)
	}
	if len(reply.Workspaces) > 0 {
		return true, fmt.Sprintf("%d workspace(s) loaded", len(reply.Workspaces))
	}
	return false, "no workspaces loaded"
}

// runProbe checks the daemon health; prints "ok: <reason>" on success, or stderr reason with exit 1.
func runProbe(ctx context.Context, socket string, kind probeKind, root string) error {
	r := buildStatusReport(ctx, socket)
	var queryErr error
	if r.PoolError != "" {
		queryErr = errors.New(r.PoolError)
	}
	ok, reason := evaluateHealth(r.Pool, queryErr, kind, root)
	if ok {
		fmt.Println("ok:", reason)
		return nil
	}
	fmt.Fprintln(os.Stderr, reason)
	return errSilent{exitCode: 1}
}

// statusQuerier fetches a daemon status snapshot for an HTTP health request.
// It is a seam so tests can supply a snapshot without dialing a live socket.
type statusQuerier func(ctx context.Context) (*proc.StatusReply, error)

// daemonStatusQuerier dials socket (auto-discovered when empty) for a live status snapshot.
func daemonStatusQuerier(socket string) statusQuerier {
	return func(ctx context.Context) (*proc.StatusReply, error) {
		addr, err := resolveStatusSocket(ctx, socket)
		if err != nil {
			return nil, err
		}
		return proc.QueryStatus(ctx, addr)
	}
}

// healthHTTPHandler returns an http.HandlerFunc that writes 200 when healthy or 503 with a reason line.
// Accepts ?workspace= to pin readiness to a specific workspace root.
func healthHTTPHandler(kind probeKind, query statusQuerier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reply, err := query(r.Context())
		ok, reason := evaluateHealth(statusOutputFromReply(reply), err, kind, r.URL.Query().Get("workspace"))
		if ok {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		fmt.Fprintln(w, reason)
	}
}
