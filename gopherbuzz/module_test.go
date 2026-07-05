package buzz_test

import (
	"context"
	"errors"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvide_BindsInOrder(t *testing.T) {
	sess := buzz.NewSession(context.Background())
	var order []string
	mk := func(name string) buzz.Module {
		return buzz.Module{Name: name, Bind: func(_ *buzz.Session, _ buzz.ModuleEnv) error {
			order = append(order, name)
			return nil
		}}
	}
	require.NoError(t, sess.Provide(buzz.ModuleEnv{}, mk("a"), mk("b"), mk("c")))
	assert.Equal(t, []string{"a", "b", "c"}, order, "modules bind in listed order (bases before overlays)")
}

func TestProvide_StopsAtFirstError(t *testing.T) {
	sess := buzz.NewSession(context.Background())
	var order []string
	rec := func(name string, e error) buzz.Module {
		return buzz.Module{Name: name, Bind: func(_ *buzz.Session, _ buzz.ModuleEnv) error {
			order = append(order, name)
			return e
		}}
	}
	boom := errors.New("boom")
	err := sess.Provide(buzz.ModuleEnv{}, rec("a", nil), rec("b", boom), rec("c", nil))
	require.Error(t, err)
	assert.ErrorIs(t, err, boom, "the underlying Bind error is wrapped, not swallowed")
	assert.Contains(t, err.Error(), `module "b"`, "error names the failing module")
	assert.Equal(t, []string{"a", "b"}, order, "binding stops at the first failure")
}

func TestProvide_SkipsNilBind(t *testing.T) {
	sess := buzz.NewSession(context.Background())
	require.NoError(t, sess.Provide(buzz.ModuleEnv{}, buzz.Module{Name: "noop"}))
}

func TestModule_HasLabel(t *testing.T) {
	m := buzz.Module{Name: "x", Labels: []string{buzz.LabelUpstream, "wasm"}}
	assert.True(t, m.HasLabel(buzz.LabelUpstream))
	assert.True(t, m.HasLabel("wasm"))
	assert.False(t, m.HasLabel(buzz.LabelGopherbuzz))
	assert.False(t, buzz.Module{Name: "y"}.HasLabel("anything"))
}
