package mcp

import (
	"context"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

type statusResult struct {
	Pool      *proc.StatusReply `json:"pool,omitempty"`
	PoolError string            `json:"pool_error,omitempty"`
}

type statusTool struct {
	opts ServerOptions
}

func (t *statusTool) Name() string { return "magus_status" }

func (t *statusTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	addr, err := resolveStatusAddr(ctx, t.opts)
	out := statusResult{}
	if err != nil {
		out.PoolError = err.Error()
		return types.InvokeResponse{Data: out}, nil
	}
	reply, err := proc.QueryStatus(ctx, addr)
	if err != nil {
		out.PoolError = fmt.Sprintf("mcp: query %s: %v", addr, err)
		return types.InvokeResponse{Data: out}, nil
	}
	out.Pool = reply
	return types.InvokeResponse{Data: out}, nil
}

var _ types.SpellDriver = (*statusTool)(nil)

func resolveStatusAddr(ctx context.Context, opts ServerOptions) (string, error) {
	if v := opts.Config.Daemon.Address; v != "" {
		return v, nil
	}
	if v := os.Getenv("MAGUS_DAEMON_SOCKET"); v != "" {
		return v, nil
	}
	return proc.DiscoverSocket(ctx)
}
