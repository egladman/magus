//go:build mcp

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus/internal/mcp/auth"
)

// probeLiveBridgeTimeout bounds the real HTTP probe of the web bridge below.
const probeLiveBridgeTimeout = 2 * time.Second

// probeLiveBridge issues a real HTTP GET to the web bridge's guarded
// /api/v1/graph route to confirm it is actually up, mirroring the doctor
// bridge check (internal/doctor/checks_mcp.go probeBridgeReachability). A
// daemon-status probe alone is not enough: daemonStatus("") accepts ANY
// reachable proc socket (Mode=="proc"), which is a different transport than
// the web bridge this URL targets - a proc-mode daemon with no bridge running
// would otherwise let a token be printed for an address nothing is listening
// on. A 401/403 response proves the guarded route exists (auth runs before
// the handler); connection refused/timeout means the bridge is down.
func probeLiveBridge(ctx context.Context, addr string) error {
	target := "http://" + addr + "/api/v1/graph"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("bridge not reachable at %s: %w", target, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil
	default:
		return fmt.Errorf("bridge at %s responded with unexpected status %d", target, resp.StatusCode)
	}
}

// liveBridgeReachable reports whether the web bridge is actually up, for the
// zero-arg auto-switch in graphOpen. It never emits a token; it only decides
// whether to attempt live mode at all.
func liveBridgeReachable(ctx context.Context) bool {
	pctx, cancel := context.WithTimeout(ctx, probeLiveBridgeTimeout)
	defer cancel()
	return probeLiveBridge(pctx, mcpAddrString()) == nil
}

// graphOpenLive opens the Graph Explorer connected to the running daemon via a
// #live= fragment. The host in the fragment is the daemon's loopback address;
// the page enforces that the host is literally 127.0.0.1 or [::1] before any
// network request is made client-side.
//
// The token is loaded from the on-disk token file written by auth.Save/SaveNew.
// It is embedded in the URL fragment (which browsers do not transmit in HTTP
// requests) and is stripped from the fragment by the page on first load.
func graphOpenLive(ctx context.Context, base string, printOnly, useTargets bool) error {
	hostPort := mcpAddrString()

	// Probe the ACTUAL web bridge (not just the proc socket) so we never emit a
	// URL and token for a transport nothing is listening on. Explicit --live
	// with no reachable bridge is an error; magus never auto-starts a daemon.
	pctx, cancel := context.WithTimeout(ctx, probeLiveBridgeTimeout)
	defer cancel()
	if err := probeLiveBridge(pctx, hostPort); err != nil {
		fmt.Fprintln(os.Stderr, "magus graph open --live: the web bridge is not reachable.")
		fmt.Fprintln(os.Stderr, "start it: magus server start")
		return errSilent{exitCode: 1}
	}

	token, err := auth.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus graph open --live: could not load the MCP token: %v\n", err)
		fmt.Fprintln(os.Stderr, "If no token exists yet, run: magus config mcp token generate")
		return errSilent{exitCode: 1}
	}

	openURL := strings.TrimRight(base, "/") + "/#live=" + hostPort + "&token=" + url.PathEscape(token)
	if useTargets {
		openURL += "&flavor=targets"
	}

	if printOnly {
		fmt.Println(openURL)
		return nil
	}

	fmt.Fprintf(os.Stderr, "opening the graph explorer in live mode (daemon at %s).\n", hostPort)
	fmt.Fprintln(os.Stderr, "the explorer connects directly to your local daemon; your graph never leaves your machine.")
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "magus graph open: could not open a browser (%v).\n", err)
		fmt.Fprintln(os.Stderr, "Re-run with --print to get the URL, or open it yourself.")
		return errSilent{exitCode: 1}
	}
	return nil
}
