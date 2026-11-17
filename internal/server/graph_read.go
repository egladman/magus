package server

import (
	"context"
	"log/slog"
	"os"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
)

// AskGraphRead has a running server answer verb into reply and reports whether it did. A
// false return is never a failure: no server, another build, another configuration and a
// broken socket all mean the caller reads locally, at debug level only. wsRoot is the
// resolved workspace root, "" when none resolved; version is the client's build.
func AskGraphRead(ctx context.Context, cfg config.Config, version, wsRoot, verb string, read *knowledge.GraphRead, reply any) bool {
	if !cfg.Server.Enabled {
		return false
	}
	sock := os.Getenv(proc.SocketEnv)
	if sock == "" {
		var ok bool
		if sock, ok = proc.LookupServerSocket(ctx); !ok {
			return false
		}
	}
	read.Config = config.ReadDigest(cfg)
	read.Env = config.EnvDigests()
	if wsRoot == "" || read.Config == "" {
		return false
	}
	if err := proc.Read(ctx, sock, version, wsRoot, verb, read, reply); err != nil {
		slog.With(attr.Component("magus")).DebugContext(ctx, "the server did not answer this read; reading locally", attr.Error(err))
		return false
	}
	return true
}
