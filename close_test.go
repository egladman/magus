package magus

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/observability"
)

// recordingProvider is a minimal observability.Provider fake: every method except
// Shutdown is a no-op, and Shutdown records whether it ran (and can be told to error).
type recordingProvider struct {
	shutdownCalled bool
	shutdownErr    error
}

func (p *recordingProvider) Enabled() bool                                                       { return true }
func (p *recordingProvider) RecordCacheHit(context.Context, ...observability.Attr)               {}
func (p *recordingProvider) RecordCacheMiss(context.Context, ...observability.Attr)              {}
func (p *recordingProvider) RecordCacheError(context.Context, ...observability.Attr)             {}
func (p *recordingProvider) RecordCacheDuration(context.Context, float64, ...observability.Attr) {}
func (p *recordingProvider) RecordGraphQuery(context.Context, float64, ...observability.Attr)    {}
func (p *recordingProvider) RecordRemoteOp(context.Context, observability.RemoteOp)              {}
func (p *recordingProvider) StartSpan(ctx context.Context, _ string, _ ...observability.Attr) (context.Context, func(error)) {
	return ctx, func(error) {}
}
func (p *recordingProvider) RecordTargetRun(context.Context, float64, ...observability.Attr) {}
func (p *recordingProvider) RecordPoolAcquire(context.Context, float64, int64)               {}
func (p *recordingProvider) RecordPoolRelease(context.Context, int64)                        {}
func (p *recordingProvider) RecordPoolWaiting(context.Context, int64)                        {}
func (p *recordingProvider) RecordMCPCall(context.Context, observability.MCPCall)            {}
func (p *recordingProvider) RecordSandboxApply(context.Context, float64, string, string)     {}
func (p *recordingProvider) RecordSandboxRules(context.Context, observability.SandboxRules)  {}
func (p *recordingProvider) RecordSandboxCheck(context.Context, string, string, string)      {}
func (p *recordingProvider) RecordSandboxEnvDropped(context.Context, string, int64)          {}
func (p *recordingProvider) RecordBuzzExec(context.Context, float64, string, string)         {}
func (p *recordingProvider) RecordBuzzCompile(context.Context, float64, string, string)      {}
func (p *recordingProvider) RecordBuzzHostCall(context.Context, observability.BuzzHostCall)  {}
func (p *recordingProvider) RecordBuzzSessionReuse(context.Context, string)                  {}
func (p *recordingProvider) RecordBuzzSessionIdle(context.Context, int64)                    {}
func (p *recordingProvider) RecordBuzzSessionEviction(context.Context, string)               {}
func (p *recordingProvider) RecordBuzzSessionWarm(context.Context, float64, string)          {}
func (p *recordingProvider) RecordBuzzImport(context.Context, float64, string, string)       {}
func (p *recordingProvider) RecordBuzzSpellResolve(context.Context, float64, string, string) {}
func (p *recordingProvider) RecordBuzzSpellBuiltinsWarm(context.Context, float64, string)    {}
func (p *recordingProvider) RecordBuzzJITRun(context.Context)                                {}
func (p *recordingProvider) RecordBuzzVMFault(context.Context, string)                       {}
func (p *recordingProvider) Snapshot(context.Context) ([]byte, error)                        { return nil, nil }
func (p *recordingProvider) Shutdown(context.Context) error {
	p.shutdownCalled = true
	return p.shutdownErr
}

var _ observability.Provider = (*recordingProvider)(nil)

// TestClose_ShutsDownOwnedProvider is P1-A's regression test: before the fix, Close
// only closed the buzz pool registry, so a provider built by Open (spans/metrics)
// was silently dropped on exit rather than flushed. Pre-fix this failed with
// "shutdownCalled: Expected true, but got false" because nothing called Shutdown.
func TestClose_ShutsDownOwnedProvider(t *testing.T) {
	p := &recordingProvider{}
	m := &Magus{tel: p}

	err := m.Close()

	require.NoError(t, err)
	assert.True(t, p.shutdownCalled, "Close must shut down a provider it owns")
}

// TestClose_JoinsProviderShutdownError verifies a shutdown error is surfaced (not
// swallowed) and that Close does not bail out early - required so a later resource
// (the pool registry) still gets a chance to close even when telemetry shutdown fails.
func TestClose_JoinsProviderShutdownError(t *testing.T) {
	wantErr := errors.New("otlp: flush failed")
	p := &recordingProvider{shutdownErr: wantErr}
	m := &Magus{tel: p}

	err := m.Close()

	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

// TestClose_LeavesInjectedProviderRunning covers the daemon case (WithProvider):
// several workspaces plus the bridge Magus share ONE provider so metrics survive
// workspace eviction (see cmd/magus/registry.go's wsRegistry, which Closes an idle
// workspace while the shared provider keeps serving the others). Close must not
// shut down a provider it does not own.
func TestClose_LeavesInjectedProviderRunning(t *testing.T) {
	p := &recordingProvider{}
	m := &Magus{tel: p, injectedTel: p}

	err := m.Close()

	require.NoError(t, err)
	assert.False(t, p.shutdownCalled, "Close must not shut down a shared/injected provider")
}
