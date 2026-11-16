package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/langservice"
)

// lspCommand implements `magus buzz lsp`: a stdio Language Server that exposes the
// completion, hover, and signature-help analysis in internal/langservice to any
// editor with a generic LSP client. The analysis engine already exists and is
// tested; this is the transport that lets an editor reach it, so magusfile
// authoring gets the same explicitness at edit time that MAGUS.md gives at read
// time. It lives under `buzz` (the Buzz-language tooling group, not a top-level
// `magus lsp`) so serving other languages later needs no new subcommand contract.
// It speaks JSON-RPC 2.0 over the LSP base protocol (Content-Length framed) on
// stdin/stdout and needs no workspace, config, or server: every request is
// answered from the document text the editor sends.
func lspCommand(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("buzz lsp", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus buzz lsp")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Run the magusfile language server over stdio (JSON-RPC 2.0 / LSP).")
		fmt.Fprintln(os.Stderr, "Point a generic LSP client at `magus buzz lsp` for the *.buzz language")
		fmt.Fprintln(os.Stderr, "id; it serves completion, hover, and signature help. See docs/editor.md.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	return langservice.NewServer(version).Serve(os.Stdin, os.Stdout)
}
