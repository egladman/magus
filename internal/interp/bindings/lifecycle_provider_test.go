package bindings

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
)

// withLifecycleSpell registers a stand-in provider whose list_lifecycles runs body, and
// returns its name. body sees the op's target and req.
func withLifecycleSpell(t *testing.T, body string) string {
	t.Helper()
	return register.Spell(t, "fake-lifecycle", spells.WithFuncSource(fmt.Sprintf(`import "magus/spell";
export fun list_lifecycles(target: Target, req: LifecycleQuery) > any !> any {
%s
}`, body)))
}

// goRecord is the go product's answer as a Buzz map, with fields replaced by over, each a
// Buzz expression. A cycle field is keyed "cycles[0].<field>".
func goRecord(over map[string]string) string {
	field := func(key, def string) string {
		if v, ok := over[key]; ok {
			return v
		}
		return def
	}
	cycle0 := fmt.Sprintf(`{"cycle": %s, "released": %s, "eol": %s, "lts": %s, "latest": "1.25.14"}`,
		field("cycles[0].cycle", `"1.25"`), field("cycles[0].released", `"2025-08-12"`),
		field("cycles[0].eol", `"2026-08-19"`), field("cycles[0].lts", "false"))
	return fmt.Sprintf(`{"key": %s, "source": %s, "asOf": %s, "cycles": %s}`,
		field("key", `"go"`), field("source", `"https://endoflife.date/api/v1/products/go"`),
		field("asOf", `"2026-09-24T07:44:41+00:00"`),
		field("cycles", fmt.Sprintf(`[%s, {"cycle": "1.27", "released": "2026-08-19", "eol": "", "lts": false, "latest": "1.27.1"}]`, cycle0)))
}

func TestRunLifecycleProviderDecodesTheAnswer(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "")
	// One batch call carrying every key, at the workspace root.
	name := withLifecycleSpell(t, fmt.Sprintf(`
    if (req.keys.join(",") != "go,nodejs" or target.projectPath != "/ws") {
        throw "asked {req.keys} at {target.projectPath}";
    }
    return [%s];`, goRecord(nil)))

	got, err := runLifecycleProvider(t.Context(), name, "/ws", []string{"go", "nodejs"})
	require.NoError(t, err)
	assert.Equal(t, []spells.Lifecycle{{
		Key: "go", Source: "https://endoflife.date/api/v1/products/go", AsOf: "2026-09-24T07:44:41+00:00",
		Cycles: []spells.ReleaseCycle{
			{Cycle: "1.25", Released: "2025-08-12", EOL: "2026-08-19", Latest: "1.25.14"},
			{Cycle: "1.27", Released: "2026-08-19", Latest: "1.27.1"},
		},
	}}, got, "a key the provider did not answer is simply absent")
}

// MAGUS_OFFLINE is honored before the VM runs, because std/http does not honor it.
func TestRunLifecycleProviderNeverEntersTheSpellOffline(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "1")
	name := withLifecycleSpell(t, `throw "the provider ran";`)

	_, err := runLifecycleProvider(t.Context(), name, "/ws", []string{"go"})
	require.ErrorIs(t, err, workspace.ErrLifecycleOffline)
}

// A throw is the source being unreachable, which is a state and not a failure.
func TestRunLifecycleProviderReadsAThrowAsUnreached(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "")
	name := withLifecycleSpell(t, `throw "dial tcp: no route to host";`)

	_, err := runLifecycleProvider(t.Context(), name, "/ws", []string{"go"})
	require.ErrorIs(t, err, workspace.ErrLifecycleUnreached)
	assert.Contains(t, err.Error(), "no route to host", "the reason travels with the state")
}

// A malformed record is an error naming the spell, the key and the field.
func TestRunLifecycleProviderRejectsAMalformedRecord(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "")
	with := func(over map[string]string) string { return fmt.Sprintf("return [%s];", goRecord(over)) }
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{"nothing returned", "return null;", []string{"want [Lifecycle]"}},
		{"not a list", "return {};", []string{"want [Lifecycle]"}},
		{"a key nobody asked for", with(map[string]string{"key": `"python"`}), []string{`"python" was not asked for`}},
		{"no source", with(map[string]string{"source": `""`}), []string{`key "go"`, `"source"`}},
		{"no as-of", with(map[string]string{"asOf": `""`}), []string{`key "go"`, `"asOf"`, "RFC 3339"}},
		{"as-of that is a date only", with(map[string]string{"asOf": `"2026-09-24"`}), []string{`key "go"`, `"asOf"`}},
		{"a bad end date", with(map[string]string{"cycles[0].eol": `"soon"`}), []string{`key "go"`, "cycles[0]", `"eol"`}},
		{"a bad release date", with(map[string]string{"cycles[0].released": `"Aug 2025"`}), []string{"cycles[0]", `"released"`}},
		{"an empty cycle", with(map[string]string{"cycles[0].cycle": `""`}), []string{"cycles[0]", `"cycle"`}},
		{"lts that is not a bool", with(map[string]string{"cycles[0].lts": `"yes"`}), []string{"cycles[0].lts", "want bool"}},
		{"cycles that are not a list", with(map[string]string{"cycles": `"1.25"`}), []string{"cycles: want [ReleaseCycle]"}},
		{"a member Lifecycle does not declare", with(map[string]string{"cycles": `[], "eolFrom": "x"`}), []string{`no member "eolFrom"`}},
		{"the same key twice", fmt.Sprintf("return [%s, %s];", goRecord(nil), goRecord(nil)), []string{`key "go" twice`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := withLifecycleSpell(t, tc.body)
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
		h.MapSet("name", vm.StringValue(name))
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
		err := callVoidDirect(t, provider, vm.StringValue("endoflife-date"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "imported spell handle")
		err = callVoidDirect(t, provider, vm.NewMap())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no name")
	})
}
