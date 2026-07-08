package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
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
	)
	_, err := cmdParse("graph open", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild before opening")
		fs.BoolVar(&globalScope, "global", false, "union the workspaces registered in config (knowledge.workspaces) into one graph")
		fs.StringVar(&base, "url", defaultExploreURL, "base URL of the Graph Explorer page (override for a self-hosted mirror)")
		fs.BoolVar(&printOnly, "print", false, "print the explorer URL to stdout instead of opening a browser (fragment mode only)")
		fs.BoolVar(&serve, "serve", false, "hand the graph to the page from an ephemeral loopback server instead of a URL fragment (no size limit; the server serves once and stops)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph open [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Open this workspace's knowledge graph in the hosted, interactive Graph")
			fmt.Fprintln(os.Stderr, "Explorer. The graph is delivered privately and never leaves your machine:")
			fmt.Fprintln(os.Stderr, "by default it rides in the link's URL fragment (#data=...), which browsers")
			fmt.Fprintln(os.Stderr, "never transmit; with --serve it is fetched from an ephemeral 127.0.0.1")
			fmt.Fprintln(os.Stderr, "loopback server (no size limit). The page is static; it decodes or fetches")
			fmt.Fprintln(os.Stderr, "the graph locally.")
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

	g, err := loadKnowledgeGraph(ctx, root, refresh, globalScope)
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

	encoded, err := encodeFragment(raw)
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

// serveMaxWait bounds how long the loopback server waits for the browser to
// request the graph before giving up (e.g. no browser opened). serveGrace is a
// short window kept open AFTER the first fetch so a quick reload still succeeds.
const (
	serveMaxWait = 2 * time.Minute
	serveGrace   = 1500 * time.Millisecond
)

// graphOpenServe hands the graph to the hosted page over an ephemeral 127.0.0.1
// server, then STOPS - it is a one-shot handoff, not a standing service. The
// server binds loopback only and answers with Access-Control-Allow-Origin scoped
// to the site origin (so only the explorer page can read it), serves the graph
// once, waits a brief grace window for a possible reload, and shuts down. It also
// exits on Ctrl-C or if the page never asks within serveMaxWait. The graph is
// delivered browser <-> loopback and never leaves the machine.
func graphOpenServe(ctx context.Context, base string, raw []byte, nodes, edges int) error {
	origin, err := siteOrigin(base)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start loopback server: %w", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	srcURL := fmt.Sprintf("http://127.0.0.1:%d/graph.json", addr.Port)

	served := make(chan struct{})
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/graph.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
		once.Do(func() { close(served) }) // the page has the graph; begin teardown
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	openURL := strings.TrimRight(base, "/") + "/#src=" + url.QueryEscape(srcURL)

	fmt.Fprintf(os.Stderr, "handing this workspace's graph (%d nodes, %d edges) to your browser over loopback (%s).\n", nodes, edges, srcURL)
	fmt.Fprintf(os.Stderr, "it is served once, CORS-locked to %s, and never leaves your machine; the server stops as soon as the page has it.\n", origin)
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "magus graph open: could not open a browser (%v). Open this yourself (the server is waiting):\n  %s\n", err, openURL)
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	shutdown := func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}

	select {
	case <-served:
		// Keep serving briefly so a fast reload re-fetches, then stop.
		select {
		case <-time.After(serveGrace):
		case <-ctx.Done():
		}
		shutdown()
		fmt.Fprintln(os.Stderr, "graph loaded; loopback server stopped.")
		return nil
	case <-time.After(serveMaxWait):
		shutdown()
		fmt.Fprintln(os.Stderr, "the page never requested the graph; loopback server stopped. Re-run if your browser did not open.")
		return nil
	case <-ctx.Done():
		shutdown()
		fmt.Fprintln(os.Stderr, "\ncanceled; loopback server stopped.")
		return nil
	case err := <-errc:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("loopback server: %w", err)
		}
		return nil
	}
}

// siteOrigin extracts the scheme://host[:port] origin from a page URL, for the
// loopback server's CORS Allow-Origin. A "null"-safe default is deliberately not
// used: an unparseable --url is a user error worth surfacing.
func siteOrigin(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("--url %q is not a valid absolute URL", base)
	}
	return u.Scheme + "://" + u.Host, nil
}

// encodeFragment gzips then base64url-encodes (no padding) a JSON payload for the
// `#data=` fragment. The browser reverses it with DecompressionStream('gzip') and
// a base64url decode (see website/js/graph-explorer.js decodeFragment).
func encodeFragment(raw []byte) (string, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := zw.Write(raw); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}

// openBrowser launches the OS default handler for a URL. It hands the URL off to
// the platform opener (macOS `open`, Windows FileProtocolHandler, else
// `xdg-open`) and does not wait - the browser owns the tab from there.
func openBrowser(url string) error {
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
