//go:build mcp

package mcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/egladman/magus/types"
)

type tailResult struct {
	Project string `json:"project"`
	LogPath string `json:"log_path"`
	Content string `json:"content"`
}

type tailLogTool struct {
	opts ServerOptions
}

func (t *tailLogTool) Name() string { return "magus_tail_log" }

func (t *tailLogTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	projectPath := paramString(req.Params, "project", "")
	if projectPath == "" {
		return types.InvokeResponse{}, errors.New("mcp: project is required")
	}

	logPath, err := t.opts.Magus.TailLog(projectPath, "")
	if errors.Is(err, types.ErrNoCache) {
		return types.InvokeResponse{}, errors.New("mcp: workspace cache is not open")
	}
	if errors.Is(err, fs.ErrNotExist) {
		return types.InvokeResponse{}, errors.New("mcp: no cache entries for project " + projectPath)
	}
	if err != nil {
		return types.InvokeResponse{}, fmt.Errorf("mcp: cache lookup: %w", err)
	}

	b, err := os.ReadFile(filepath.Clean(logPath))
	if err != nil {
		toolLogger(ctx).Warn("mcp: read log failed", "path", logPath, "error", err)
		return types.InvokeResponse{}, fmt.Errorf("mcp: read log: %w", err)
	}

	return types.InvokeResponse{Data: tailResult{
		Project: projectPath,
		LogPath: logPath,
		Content: string(b),
	}}, nil
}

var _ types.SpellDriver = (*tailLogTool)(nil)
