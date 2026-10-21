package bindings

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interp/bindings/gen/providerops"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/spells/gen/spellstest"
	"github.com/egladman/magus/types"
)

// buzzProvider is a provider spell backed by src, as an imported spell is, and registered
// nowhere: a test hands it straight to the adapter under test.
func buzzProvider(t *testing.T, src string) spells.Driver {
	t.Helper()
	return spells.NewSpell("test-provider", spells.WithFuncSource(`import "magus/spell";
`+src))
}

func TestCallOpHandsTheTargetAndRequestAndDecodesTheAnswer(t *testing.T) {
	drv := buzzProvider(t, `
export fun resolve_secret(target: Target, req: SecretRef) > Secret {
    return Secret{ value = "{target.name}@{target.projectPath}:{req.ref}" };
}`)
	op := providerops.ResolveSecret
	got, exported, err := CallOp(context.Background(), drv, op,
		types.Target{Name: op.Name, Path: "/ws"}, types.SecretRef{Ref: "vault/token"})
	require.NoError(t, err)
	assert.True(t, exported)
	assert.Equal(t, spells.Secret{Value: "resolve_secret@/ws:vault/token"}, got)
}

// A missing export is a capability the provider lacks, not a failure: the caller says what
// that means for its family.
func TestCallOpReportsAMissingExportWithoutFailing(t *testing.T) {
	drv := buzzProvider(t, `export fun mgs_getName() > str { return "x"; }`)
	got, exported, err := CallOp(context.Background(), drv, providerops.CIEnabled, types.Target{}, types.Empty{})
	require.NoError(t, err)
	assert.False(t, exported)
	assert.Equal(t, types.Ack{}, got)
}

// The answer decodes strictly: a member the response does not declare names itself rather
// than being dropped.
func TestCallOpRefusesAnAnswerTheResponseDoesNotDeclare(t *testing.T) {
	drv := buzzProvider(t, `
export fun last_green_run(target: Target, req: Empty) > any {
    return {"run": "7", "commit": "abc", "branch": "main"};
}`)
	_, exported, err := CallOp(context.Background(), drv, providerops.LastGreenRun, types.Target{}, types.Empty{})
	require.Error(t, err)
	assert.True(t, exported)
	assert.ErrorContains(t, err, "last_green_run")
	assert.ErrorContains(t, err, `has no member "branch"`)
}

func TestCallOpRefusesADriverWithNoBuzzSource(t *testing.T) {
	drv := spellstest.NewMockDriver(t)
	drv.EXPECT().Name().Return("go-driver")
	_, _, err := CallOp(context.Background(), drv, providerops.CIEnabled, types.Target{}, types.Empty{})
	require.Error(t, err)
	assert.ErrorContains(t, err, "no Buzz source")
}

func TestCallOpRefusesAnExportThatIsNotAFunction(t *testing.T) {
	drv := buzzProvider(t, `export final enabled = true;`)
	_, exported, err := CallOp(context.Background(), drv, providerops.CIEnabled, types.Target{}, types.Empty{})
	require.Error(t, err)
	assert.True(t, exported)
	assert.ErrorContains(t, err, "not a function")
}
