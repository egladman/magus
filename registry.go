package magus

import (
	"context"

	"github.com/egladman/magus/internal/wire"
)

// WorkspaceRegistry holds project-option overrides and target policies for a single Open.
type WorkspaceRegistry = wire.WorkspaceRegistry

// NewWorkspaceRegistry returns an empty WorkspaceRegistry.
func NewWorkspaceRegistry() *WorkspaceRegistry { return wire.NewWorkspaceRegistry() }

// WithWorkspaceRegistryContext installs reg in ctx so interpreters can retrieve it.
func WithWorkspaceRegistryContext(ctx context.Context, reg *WorkspaceRegistry) context.Context {
	return wire.ContextWithRegistry(ctx, reg)
}

// WorkspaceRegistryFromContext returns the WorkspaceRegistry from ctx, or nil.
func WorkspaceRegistryFromContext(ctx context.Context) *WorkspaceRegistry {
	return wire.WorkspaceRegistryFromContext(ctx)
}

func installWorkspaceRegistry(ctx context.Context, reg *WorkspaceRegistry) context.Context {
	return wire.ContextWithRegistry(ctx, reg)
}
