package main

import (
	"context"
	"flag"
	"os"

	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/libs/clikit/exit"
)

// mcpAddrString returns the configured MCP address as a host:port string, falling back
// to the default. Used by doctor.BuildServerInfo so the bridge doctor check knows which
// address to probe.
func mcpAddrString() string {
	return server.MCPAddress(globalCfg.MCP)
}

// mcpCommand serves MCP over stdin and stdout for the agent host that launched it, against the
// workspace it was launched in. It serves whoever runs it, a person at a terminal included:
// the notice server.ServeMCPStdio logs is what tells that person what they started.
func mcpCommand(ctx context.Context, root string, args []string) error {
	rest, err := commandParse("mcp", args, func(fs *flag.FlagSet) {
		fs.Usage = mcpUsage
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return exit.Usagef("magus mcp: takes no arguments (got %q)", rest[0])
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	return server.ServeMCPStdio(ctx, m, os.Stdin, os.Stdout, server.StdioIdentity{Config: globalCfg, Version: version, Build: selfBuild})
}

// mcpUsage is `magus mcp --help`: the stdio registration a host needs, then the server's
// HTTP endpoint for a client that wants one long-lived server. It names no host: each
// client's config dialect belongs in docs/guides/integrations/mcp.md, where a change to one
// is not a magus release.
func mcpUsage() { server.WriteMCPUsage(os.Stderr, globalCfg.MCP) }
