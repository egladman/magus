package bindings

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// withLifecycleSpell registers a stand-in provider whose list_lifecycles answers with
// answer, and returns its name and a pointer to the request it last saw.
func withLifecycleSpell(t *testing.T, answer func() (any, error)) (string, *spells.InvokeRequest) {
	t.Helper()
	fakeSpellSeq++
	name := fmt.Sprintf("fake-lifecycle-%d", fakeSpellSeq)
	var seen spells.InvokeRequest
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name,
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			seen = req
			return answer()
		})))
	return name, &seen
}

func goRecord() map[string]any {
	return map[string]any{
		"key": "go", "source": "https://endoflife.date/api/v1/products/go", "asOf": "2026-09-24T07:44:41+00:00",
		"cycles": []any{
			map[string]any{"cycle": "1.25", "released": "2025-08-12", "eol": "2026-08-19", "lts": false, "latest": "1.25.14"},
			map[string]any{"cycle": "1.27", "released": "2026-08-19", "eol": "", "lts": false, "latest": "1.27.1"},
		},
	}
}

func TestRunLifecycleProviderDecodesTheAnswer(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "")
	name, seen := withLifecycleSpell(t, func() (any, error) { return []any{goRecord()}, nil })

	got, err := runLifecycleProvider(t.Context(), name, "/ws", []string{"go", "nodejs"})
	require.NoError(t, err)
	assert.Equal(t, []spells.Lifecycle{{
		Key: "go", Source: "https://endoflife.date/api/v1/products/go", AsOf: "2026-09-24T07:44:41+00:00",
		Cycles: []spells.ReleaseCycle{
			{Cycle: "1.25", Released: "2025-08-12", EOL: "2026-08-19", Latest: "1.25.14"},
			{Cycle: "1.27", Released: "2026-08-19", Latest: "1.27.1"},
		},
	}}, got, "a key the provider did not answer is simply absent")
	assert.Equal(t, spells.InvokeRequest{
		Target: spells.ListLifecyclesContract,
		Dir:    "/ws",
		Params: map[string]any{"keys": []string{"go", "nodejs"}},
	}, *seen, "one batch call carrying every key")
}

// MAGUS_OFFLINE is honored before the VM runs, because std/http does not honor it.
func TestRunLifecycleProviderNeverEntersTheSpellOffline(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "1")
	called := false
	name, _ := withLifecycleSpell(t, func() (any, error) { called = true; return []any{}, nil })

	_, err := runLifecycleProvider(t.Context(), name, "/ws", []string{"go"})
	require.ErrorIs(t, err, workspace.ErrLifecycleOffline)
	assert.False(t, called)
}

// A throw is the source being unreachable, which is a state and not a failure.
func TestRunLifecycleProviderReadsAThrowAsUnreached(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "")
	name, _ := withLifecycleSpell(t, func() (any, error) { return nil, errors.New("dial tcp: no route to host") })

	_, err := runLifecycleProvider(t.Context(), name, "/ws", []string{"go"})
	require.ErrorIs(t, err, workspace.ErrLifecycleUnreached)
	assert.Contains(t, err.Error(), "no route to host", "the reason travels with the state")
}

// A malformed record is an error naming the spell, the key and the field.
func TestRunLifecycleProviderRejectsAMalformedRecord(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "")
	with := func(mutate func(map[string]any)) any {
		r := goRecord()
		mutate(r)
		return []any{r}
	}
	cycle := func(r map[string]any) map[string]any { return r["cycles"].([]any)[0].(map[string]any) }
	for _, tc := range []struct {
		name   string
		answer any
		want   []string
	}{
		{"nothing returned", nil, []string{"returned nothing"}},
		{"not a list", map[string]any{}, []string{"want a list of Lifecycle"}},
		{"a key nobody asked for", with(func(r map[string]any) { r["key"] = "python" }), []string{`"python" was not asked for`}},
		{"no source", with(func(r map[string]any) { r["source"] = "" }), []string{`key "go"`, `"source"`}},
		{"no as-of", with(func(r map[string]any) { r["asOf"] = "" }), []string{`key "go"`, `"asOf"`, "RFC 3339"}},
		{"as-of that is a date only", with(func(r map[string]any) { r["asOf"] = "2026-09-24" }), []string{`key "go"`, `"asOf"`}},
		{"a bad end date", with(func(r map[string]any) { cycle(r)["eol"] = "soon" }), []string{`key "go"`, "cycles[0]", `"eol"`}},
		{"a bad release date", with(func(r map[string]any) { cycle(r)["released"] = "Aug 2025" }), []string{"cycles[0]", `"released"`}},
		{"an empty cycle", with(func(r map[string]any) { cycle(r)["cycle"] = "" }), []string{"cycles[0]", `"cycle"`}},
		{"lts that is not a bool", with(func(r map[string]any) { cycle(r)["lts"] = "yes" }), []string{"cycles[0]", `"lts"`, "want bool"}},
		{"cycles that are not a list", with(func(r map[string]any) { r["cycles"] = "1.25" }), []string{`"cycles"`}},
		{"the same key twice", []any{goRecord(), goRecord()}, []string{`key "go" twice`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, _ := withLifecycleSpell(t, func() (any, error) { return tc.answer, nil })
			_, err := runLifecycleProvider(t.Context(), name, "/ws", []string{"go"})
			require.Error(t, err)
			assert.NotErrorIs(t, err, workspace.ErrLifecycleUnreached, "a bad answer is not an unreachable host")
			assert.Contains(t, err.Error(), fmt.Sprintf("spell %q", name), "the error names the provider")
			for _, w := range tc.want {
				assert.Contains(t, err.Error(), w)
			}
		})
	}
}

func TestRunLifecycleProviderUnregisteredSpell(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "")
	_, err := runLifecycleProvider(t.Context(), "not-a-spell", "/ws", []string{"go"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not registered")
}

func TestBuildLifecycle(t *testing.T) {
	handle := func(name string) vm.Value {
		h := vm.NewMap()
		h.MapSet("name", vm.StrValue(name))
		return h
	}

	t.Run("a spell handle records the provider, and a repeat is harmless", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		provider := requireDirect(t, buildLifecycle(workspace.ContextWithRegistry(t.Context(), reg), nil), "provider")
		require.NoError(t, callVoidDirect(t, provider, handle("endoflife-date")))
		require.NoError(t, callVoidDirect(t, provider, handle("endoflife-date")))
		assert.Equal(t, "endoflife-date", reg.LifecycleProvider())
	})

	t.Run("a second provider is refused", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		provider := requireDirect(t, buildLifecycle(workspace.ContextWithRegistry(t.Context(), reg), nil), "provider")
		require.NoError(t, callVoidDirect(t, provider, handle("endoflife-date")))
		err := callVoidDirect(t, provider, handle("other"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "one lifecycle provider")
		assert.Equal(t, "endoflife-date", reg.LifecycleProvider(), "the first wiring stands")
	})

	t.Run("a non-handle is rejected", func(t *testing.T) {
		provider := requireDirect(t, buildLifecycle(t.Context(), nil), "provider")
		err := callVoidDirect(t, provider, vm.StrValue("endoflife-date"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "imported spell handle")
		err = callVoidDirect(t, provider, vm.NewMap())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no name")
	})
}
