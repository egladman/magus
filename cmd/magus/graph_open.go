package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/types"
)

// defaultExploreURL is the hosted, data-agnostic Graph Explorer. `open` points a
// browser at this page with the workspace's graph delivered PRIVATELY: either in a
// URL fragment (default) or fetched from an ephemeral loopback server (--serve).
// Either way the graph stays on the machine - the site only serves static assets.
const defaultExploreURL = "https://eli.gladman.cc/magus/graph/"

// fragmentWarnBytes is a conservative ceiling on the encoded fragment. The whole
// URL rides on the command line to the browser and into the address bar; Chrome
// handles multi-megabyte URLs, but Safari (~80 KB) and older Firefox (~64 KB)
// cap shorter. Past this we point at --serve, which has no size limit.
const fragmentWarnBytes = 48 * 1024

// graphOpen opens this workspace's knowledge graph in the hosted Graph Explorer.
// Two privacy-first delivery modes:
//   - default: gzip+base64url the graph into a `#data=` URL fragment. A fragment
//     is never sent in an HTTP request, so the graph never leaves the machine.
//     Simple and serverless, but bounded by browser URL limits.
//   - --serve: run an ephemeral loopback HTTP server (127.0.0.1) that serves the
//     graph to the page via `#src=`. No size limit; the data stays on the local
//     network (loopback), never reaching the hosted site. CORS is locked to the
//     site origin so no other page can read it.
func graphOpen(ctx context.Context, root string, args []string) error {
	var (
		refresh     bool
		globalScope bool
		base        string
		printOnly   bool
		serve       bool
		useTargets  bool
		useLive     bool
	)
	pos, err := cmdParse("graph open", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild before opening")
		fs.BoolVar(&globalScope, "global", false, "union the workspaces registered in config (knowledge.workspaces) into one graph")
		fs.StringVar(&base, "url", defaultExploreURL, "base URL of the Graph Explorer page (override for a self-hosted mirror)")
		fs.BoolVar(&printOnly, "print", false, "print the explorer URL to stdout instead of opening a browser (fragment mode only)")
		fs.BoolVar(&serve, "serve", false, "hand the graph to the page from an ephemeral loopback server instead of a URL fragment (no size limit; the server serves once and stops)")
		fs.BoolVar(&useTargets, "targets", false, "open the target dependency graph instead of the knowledge graph; pass a project path as a positional argument to scope to one project")
		fs.BoolVar(&useLive, "live", false, "connect the explorer to the running daemon for a live workspace view (requires 'magus server start')")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph open [flags] [project-path]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Open this workspace's knowledge graph in the hosted, interactive Graph")
			fmt.Fprintln(os.Stderr, "Explorer. The graph is delivered privately and never leaves your machine:")
			fmt.Fprintln(os.Stderr, "by default it rides in the link's URL fragment (#data=...), which browsers")
			fmt.Fprintln(os.Stderr, "never transmit; with --serve it is fetched from an ephemeral 127.0.0.1")
			fmt.Fprintln(os.Stderr, "loopback server (no size limit). The page is static; it decodes or fetches")
			fmt.Fprintln(os.Stderr, "the graph locally.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "With --targets, opens the target dependency graph instead of the knowledge")
			fmt.Fprintln(os.Stderr, "graph. An optional project-path positional argument scopes the view to one")
			fmt.Fprintln(os.Stderr, "project. Target graphs are always delivered via the URL fragment (--serve")
			fmt.Fprintln(os.Stderr, "is incompatible with --targets).")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "With --live, the explorer connects to the running daemon (magus server start)")
			fmt.Fprintln(os.Stderr, "and updates automatically as files change. The host must be loopback.")
			fmt.Fprintln(os.Stderr, "Zero-arg default: if the daemon is reachable and no mode flag is given,")
			fmt.Fprintln(os.Stderr, "--live is chosen automatically.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "For a graph to hand to another tool, use `magus graph export -o json`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	if useTargets {
		if serve {
			fmt.Fprintln(os.Stderr, "magus graph open: --targets and --serve cannot be used together.")
			fmt.Fprintln(os.Stderr, "Target graphs are small; they always use the URL fragment.")
			return errSilent{exitCode: 2}
		}
		if globalScope {
			fmt.Fprintln(os.Stderr, "magus graph open: --targets and --global cannot be used together.")
			fmt.Fprintln(os.Stderr, "--targets scopes to this workspace's target graph; use a positional argument to scope to one project.")
			return errSilent{exitCode: 2}
		}
		if refresh {
			fmt.Fprintln(os.Stderr, "magus graph open: --targets and --refresh cannot be used together.")
			fmt.Fprintln(os.Stderr, "--targets reads the target graph directly from the magusfile; there is no knowledge store to refresh.")
			return errSilent{exitCode: 2}
		}
		return graphOpenTargets(ctx, root, base, printOnly, pos)
	}

	// Zero-arg default: when no explicit delivery mode is chosen and no --targets,
	// probe the ACTUAL web bridge first (not just the proc socket - a proc daemon
	// can be up with no bridge running). If it is reachable, use --live for an
	// always-fresh view; otherwise fall through to fragment mode.
	if !useLive && !serve {
		if liveBridgeReachable(ctx) {
			useLive = true
		}
	}
	if useLive {
		return graphOpenLive(ctx, base, printOnly, useTargets)
	}

	// The explorer shows the domain graph; symbol shards would bloat it, so exclude them.
	g, err := loadKnowledgeGraph(ctx, root, refresh, globalScope, false)
	if err != nil {
		return err
	}
	out := g.Output()
	if !globalScope {
		out.SourceBaseURL = deriveSourceBase(ctx, root) // link node sources to the right repo
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode graph: %w", err)
	}

	if serve {
		return graphOpenServe(ctx, base, raw, out.NodeCount, out.EdgeCount)
	}

	encoded, err := render.EncodeFragmentRaw(raw)
	if err != nil {
		return err
	}
	openURL := strings.TrimRight(base, "/") + "/#data=" + encoded

	if len(encoded) > fragmentWarnBytes {
		fmt.Fprintf(os.Stderr, "magus graph open: this graph encodes to %d KB, near or past what Safari and older\n", len(encoded)/1024)
		fmt.Fprintln(os.Stderr, "Firefox accept in a URL (Chrome is fine). If the page does not load, re-run with")
		fmt.Fprintln(os.Stderr, "--serve to deliver it over a loopback server instead (no size limit). Continuing.")
	}

	if printOnly {
		fmt.Println(openURL)
		return nil
	}

	fmt.Fprintf(os.Stderr, "opening the graph explorer for this workspace (%d nodes, %d edges).\n", out.NodeCount, out.EdgeCount)
	fmt.Fprintln(os.Stderr, "your graph rides in the link fragment and is never uploaded - it does not leave your machine.")
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "magus graph open: could not open a browser (%v).\n", err)
		fmt.Fprintln(os.Stderr, "Re-run with --print to get the URL, or open it yourself.")
		return errSilent{exitCode: 1}
	}
	return nil
}

// graphOpenTargets opens the workspace's target dependency graph in the hosted
// Graph Explorer using the #data= fragment path. Target graphs are always
// delivered via the fragment (they are small, so --serve is never needed).
// If args contains a project path, only that project's targets are included.
func graphOpenTargets(ctx context.Context, root, base string, printOnly bool, args []string) error {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	out := ws.DescribeGraph()

	if len(args) > 0 {
		scope := args[0]
		var filtered []types.TargetGraphProject
		for _, p := range out.Projects {
			if p.Path == scope {
				filtered = append(filtered, p)
				break
			}
		}
		if len(filtered) == 0 {
			paths := make([]string, 0, len(out.Projects))
			for _, p := range out.Projects {
				paths = append(paths, p.Path)
			}
			slices.Sort(paths)
			fmt.Fprintf(os.Stderr, "magus graph open --targets: unknown project %q\n", scope)
			fmt.Fprintln(os.Stderr, "valid projects:")
			for _, p := range paths {
				fmt.Fprintf(os.Stderr, "  %s\n", p)
			}
			return errSilent{exitCode: 2}
		}
		out.Projects = filtered
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode target graph: %w", err)
	}
	encoded, err := render.EncodeFragmentRaw(raw)
	if err != nil {
		return err
	}
	openURL := strings.TrimRight(base, "/") + "/#data=" + encoded

	if printOnly {
		fmt.Println(openURL)
		return nil
	}

	fmt.Fprintln(os.Stderr, "opening the graph explorer for this workspace's target graph.")
	fmt.Fprintln(os.Stderr, "your graph rides in the link fragment and is never uploaded - it does not leave your machine.")
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "magus graph open: could not open a browser (%v).\n", err)
		fmt.Fprintln(os.Stderr, "Re-run with --print to get the URL, or open it yourself.")
		return errSilent{exitCode: 1}
	}
	return nil
}

// graphOpenServe hands the graph to the hosted page over an ephemeral 127.0.0.1 server,
// then STOPS - a one-shot handoff, not a standing service. The loopback bind, CORS lock,
// serve-once, and grace-then-shutdown all live in internal/httpx (shared with the live log
// stream); this wraps them with the graph-specific URL (#src=) and the user-facing
// messages. The graph is delivered browser <-> loopback and never leaves the machine.
func graphOpenServe(ctx context.Context, base string, raw []byte, nodes, edges int) error {
	origin, err := httpx.ParseOrigin(base)
	if err != nil {
		return err
	}
	bs, err := httpx.StartBlob(origin, "/graph.json", "application/json", raw)
	if err != nil {
		return err
	}
	openURL := strings.TrimRight(base, "/") + "/#src=" + url.QueryEscape(bs.SourceURL())

	fmt.Fprintf(os.Stderr, "handing this workspace's graph (%d nodes, %d edges) to your browser over loopback (%s).\n", nodes, edges, bs.SourceURL())
	fmt.Fprintf(os.Stderr, "it is served once, CORS-locked to %s, and never leaves your machine; the server stops as soon as the page has it.\n", origin)
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "magus graph open: could not open a browser (%v). Open this yourself (the server is waiting):\n  %s\n", err, openURL)
	}

	switch bs.WaitServed(ctx) {
	case httpx.ServeCompleted:
		fmt.Fprintln(os.Stderr, "graph loaded; loopback server stopped.")
	case httpx.ServeTimedOut:
		fmt.Fprintln(os.Stderr, "the page never requested the graph; loopback server stopped. Re-run if your browser did not open.")
	case httpx.ServeCanceled:
		fmt.Fprintln(os.Stderr, "\ncanceled; loopback server stopped.")
	}
	return nil
}

// openBrowser launches a browser for a URL and does not wait - the browser owns the
// tab from there. It honors the freedesktop/de-facto BROWSER convention first, so a
// user can force a specific browser on any platform (e.g.
// `BROWSER=firefox magus query ref1a2b3c --open`); only when BROWSER is unset or every
// entry fails does it fall back to the OS default handler (macOS `open`, Windows
// FileProtocolHandler, else `xdg-open`, which itself already respects BROWSER and the
// desktop's default-web-browser setting on Linux).
func openBrowser(url string) error {
	if err := openViaBrowserEnv(url); err == nil {
		return nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// openViaBrowserEnv tries the $BROWSER convention: a colon-separated list of commands,
// each either containing "%s" (replaced by the URL) or taking the URL as a trailing
// argument. The first entry that launches wins. Returns an error if BROWSER is unset
// or no entry starts, so the caller falls back to the platform opener.
func openViaBrowserEnv(url string) error {
	env := strings.TrimSpace(os.Getenv("BROWSER"))
	if env == "" {
		return errors.New("BROWSER not set")
	}
	for _, entry := range strings.Split(env, ":") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		var fields []string
		if strings.Contains(entry, "%s") {
			fields = strings.Fields(strings.ReplaceAll(entry, "%s", url))
		} else {
			fields = append(strings.Fields(entry), url)
		}
		if len(fields) == 0 {
			continue
		}
		if err := exec.Command(fields[0], fields[1:]...).Start(); err == nil {
			return nil
		}
	}
	return errors.New("no BROWSER entry launched")
}
