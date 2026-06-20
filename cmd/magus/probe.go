package main

import (
	"context"
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
func evaluateHealth(status *types.StatusOutput, err error, kind probeKind, root string) (ok bool, reason string) {
	if err != nil || status == nil {
		if err != nil {
			return false, fmt.Sprintf("daemon unreachable: %v", err)
		}
		return false, "daemon unreachable"
	}
	if kind == probeLiveness {
		return true, fmt.Sprintf("daemon pid %d is alive", status.ParentPID)
	}
	// Readiness is a multi-workspace daemon concept — only the daemon reports
	// which workspaces are loaded. A per-process proc server never does, so
	// report that honestly instead of a misleading "no workspaces loaded".
	if status.Mode == "proc" {
		return false, "daemon is in per-process mode; readiness requires `magus server start`"
	}
	if root != "" {
		clean := filepath.Clean(root)
		for _, ws := range status.Workspaces {
			if filepath.Clean(ws.Root) == clean {
				return true, fmt.Sprintf("workspace %s is loaded", root)
			}
		}
		return false, fmt.Sprintf("workspace %s is not loaded", root)
	}
	if len(status.Workspaces) > 0 {
		return true, fmt.Sprintf("%d workspace(s) loaded", len(status.Workspaces))
	}
	return false, "no workspaces loaded"
}

// runProbe checks the daemon health; prints "ok: <reason>" on success, or stderr reason with exit 1.
func runProbe(ctx context.Context, socket string, kind probeKind, root string) error {
	status, err := daemonStatus(socket)(ctx)
	ok, reason := evaluateHealth(status, err, kind, root)
	if ok {
		fmt.Println("ok:", reason)
		return nil
	}
	fmt.Fprintln(os.Stderr, reason)
	return errSilent{exitCode: 1}
}

// statusFunc returns the daemon's current status snapshot for a health check.
// It is a seam so tests can supply a snapshot without dialing a live socket.
type statusFunc func(ctx context.Context) (*types.StatusOutput, error)

// daemonStatus dials socket (auto-discovered when empty) for a live status snapshot.
func daemonStatus(socket string) statusFunc {
	return func(ctx context.Context) (*types.StatusOutput, error) {
		addr, err := resolveStatusSocket(ctx, socket)
		if err != nil {
			return nil, err
		}
		reply, err := proc.QueryStatus(ctx, addr)
		if err != nil {
			return nil, err
		}
		return statusOutputFromReply(reply), nil
	}
}

// healthHTTPHandler returns an http.HandlerFunc that writes 200 when healthy or 503 with a reason line.
// Accepts ?workspace= to pin readiness to a specific workspace root.
func healthHTTPHandler(kind probeKind, status statusFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := status(r.Context())
		ok, reason := evaluateHealth(snapshot, err, kind, r.URL.Query().Get("workspace"))
		if ok {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		fmt.Fprintln(w, reason)
	}
}
