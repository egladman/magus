package mcp

import (
	"context"
	"time"

	"github.com/egladman/magus/internal/handler/mcp/origin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/mcpclient"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

type clientTool struct {
	root string
	// timeout bounds a direct call. A call the host runs as an MCP task is not
	// bounded; the host cancels it instead.
	timeout time.Duration
}

func (t *clientTool) Name() string { return hint.ToolClient.String() }

func (t *clientTool) Invoke(ctx context.Context, call spells.InvokeRequest) (spells.InvokeResponse, error) {
	req, err := t.request(call.Params)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	w := worker{
		tool:          hint.ToolClient,
		code:          types.MCPClientFailed,
		root:          t.root,
		env:           clientWorkerEnv(ctx),
		maxRequest:    mcpclient.MaxRequestBytes,
		shorten:       "shorten the script or args",
		timeout:       t.timeout,
		timeoutRemedy: "run it as an MCP task, or through the Buzz CLI",
	}
	if runsAsTask(ctx) {
		w.timeout = 0
	}
	result, err := runWorker[mcpclient.Result](ctx, w, req)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	return spells.InvokeResponse{Data: result}, nil
}

// clientWorkerEnv is the forked worker's confinement plus the caller's identity.
// Over stdio the process is the caller, so the inherited environment is enough.
// Over HTTP the server's own lease is nobody's caller: the request's lease is
// passed explicitly, and an empty one is a stamped absence.
func clientWorkerEnv(ctx context.Context) []string {
	env := []string{mcpclient.WorkerEnv + "=1"}
	if o, ok := origin.FromContext(ctx); ok && o.Name != "" {
		env = append(env, mcpclient.HostEnv+"="+o.Name)
	}
	if actor, ok := callerActor(ctx); ok {
		env = append(env, mcpclient.LeaseEnv+"="+actor.Lease)
	}
	return env
}

func (t *clientTool) request(params map[string]any) (mcpclient.Request, error) {
	script, err := scriptSource(params, t.root, hint.ToolClient, types.MCPClientFailed, mcpclient.MaxSourceBytes)
	if err != nil {
		return mcpclient.Request{}, err
	}
	args, err := stringArgs(params["args"], hint.ToolClient, types.MCPClientFailed, mcpclient.MaxArgs, mcpclient.MaxArgBytes)
	if err != nil {
		return mcpclient.Request{}, err
	}
	return mcpclient.Request{Script: script, Args: args}, nil
}

var _ spells.Driver = (*clientTool)(nil)
