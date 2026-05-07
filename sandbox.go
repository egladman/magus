package magus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/sandbox"
	sandboxapply "github.com/egladman/magus/internal/sandbox/apply"
	"github.com/egladman/magus/types"
)

// applySandbox applies the process-wide landlock sandbox and attaches the Policy to ctx.
func (m *Magus) applySandbox(ctx context.Context) (context.Context, error) {
	p := sandboxapply.FromConfig(m.ws.Root, m.cfg)
	return sandboxapply.Apply(ctx, p, m.ws.Root)
}

// ApplyUnionSandbox unions the landlock policies of every workspace root and
// applies the combined ruleset to the current process exactly once. Roots whose
// config disables the sandbox still contribute filesystem rules but no
// binding-layer policy (MGS2011). It is a no-op (returns nil) when no root
// requests kernel sandboxing.
//
// This is the multi-workspace (daemon) counterpart to the per-workspace sandbox
// that Run applies. It lives in the library so callers — the CLI daemon in
// particular — never import internal/sandbox directly: policy assembly and
// application stay behind one seam, so the two paths cannot drift.
func ApplyUnionSandbox(ctx context.Context, roots []string) error {
	if len(roots) == 0 {
		return nil
	}

	policies := make([]*sandbox.Policy, 0, len(roots))
	anyEnabled := false
	for _, root := range roots {
		cfg := loadWorkspaceConfig(root)
		if cfg.Sandbox.Enabled {
			anyEnabled = true
		}
		policies = append(policies, sandboxapply.FromConfig(root, cfg))
	}

	if !anyEnabled {
		return nil // no workspace requested kernel sandboxing
	}

	union := sandbox.UnionPolicies(policies...)
	if err := sandbox.Apply(union); err != nil {
		if errors.Is(err, sandbox.ErrUnsupported) {
			slog.WarnContext(ctx, types.FormatDiagnostic(types.SandboxUnsupported,
				"kernel landlock unavailable; multi-workspace daemon running with interpreter-level checks only"),
				"reason", err.Error())
			sandboxapply.MarkAppliedExternally(union.Fingerprint())
			return nil
		}
		return fmt.Errorf("magus: apply union sandbox: %w", err)
	}
	sandboxapply.MarkAppliedExternally(union.Fingerprint())
	slog.InfoContext(ctx, "magus: applied union landlock ruleset",
		"workspaces", len(roots),
		"fingerprint", union.Fingerprint())
	return nil
}

// loadWorkspaceConfig loads root's magus.yaml with env overrides applied, falling
// back to defaults when the file is absent — the resolution used for sandbox union.
func loadWorkspaceConfig(root string) config.Config {
	cfg, err := config.LoadFile(filepath.Join(root, "magus.yaml"), false)
	if err != nil {
		cfg = config.Defaults()
	}
	configgen.ApplyEnv(&cfg, os.Getenv)
	return cfg
}
