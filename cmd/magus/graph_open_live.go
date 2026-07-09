//go:build mcp

package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/egladman/magus/internal/mcp/auth"
)

// graphOpenLive opens the Graph Explorer connected to the running daemon via a
// #live= fragment. The host in the fragment is the daemon's loopback address;
// the page enforces that the host is literally 127.0.0.1 or [::1] before any
// network request is made client-side.
//
// The token is loaded from the on-disk token file written by auth.Save/SaveNew.
// It is embedded in the URL fragment (which browsers do not transmit in HTTP
// requests) and is stripped from the fragment by the page on first load.
func graphOpenLive(ctx context.Context, root, base string, printOnly, useTargets bool) error {
	// Probe daemon so we can give a clear error when it is not running.
	if _, err := daemonStatus("")(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "magus graph open --live: daemon is not running.")
		fmt.Fprintln(os.Stderr, "Start it with: magus server start")
		return errSilent{exitCode: 1}
	}

	token, err := auth.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus graph open --live: could not load the MCP token: %v\n", err)
		fmt.Fprintln(os.Stderr, "If no token exists yet, run: magus config mcp token generate")
		return errSilent{exitCode: 1}
	}

	hostPort := mcpAddrString()

	openURL := strings.TrimRight(base, "/") + "/#live=" + hostPort + "&token=" + url.QueryEscape(token)
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
