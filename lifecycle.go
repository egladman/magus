package magus

import (
	"context"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// LifecycleProvider returns the spell the root magusfile wired via
// magus\lifecycle.provider, or "" when none is.
func (m *Magus) LifecycleProvider() string {
	if m.wsReg == nil {
		return ""
	}
	return m.wsReg.LifecycleProvider()
}

// Lifecycles asks the wired lifecycle provider when each release cycle of this workspace's
// tools reaches end of life, for every product the resolved spells name, and stores the
// answer under the cache directory for doctor to read. installed are the rows the caller
// probed; their versions are stored beside the answer, so doctor can place them without
// forking a probe.
//
// It reaches the network: the provider fetches on every call. The status says whether the
// answer is live, replayed (offline or unreached), or absent (unwired). The error is
// non-nil only for a malformed answer, which names the spell, key and field.
func (m *Magus) Lifecycles(ctx context.Context, installed ...types.ToolRow) (types.LifecycleStatus, []spells.Lifecycle, error) {
	var seen []workspace.InstalledTool
	for _, r := range installed {
		if r.Lifecycle != "" && r.InstalledVersion != "" {
			seen = append(seen, workspace.InstalledTool{Project: r.Project, Bin: r.Bin, Lifecycle: r.Lifecycle, Version: r.InstalledVersion})
		}
	}
	a, err := workspace.AskLifecycles(ctx,
		workspace.ProviderCache{Dir: m.CacheDir(), Immutable: cacheImmutable(m.cfg)},
		m.ws.Root, m.LifecycleProvider(), workspace.LifecycleKeys(m.ws.All()), seen)
	return a.Status, a.Lifecycles, err
}
