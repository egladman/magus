package spellruntime

import (
	"fmt"
	"sort"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mapObj is a test-only implementation of spellruntime.Obj backed by map[string]any.
type mapObj map[string]any

func (m mapObj) Str(key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func (m mapObj) Bool(key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func (m mapObj) Strs(key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	ss, _ := v.([]string)
	return ss
}

func (m mapObj) StrMap(key string) (map[string]string, error) {
	// Returns the map RAW, empty included: absent-vs-empty normalization is
	// decodeCommand's job, and a double that pre-normalized would test itself
	// rather than production.
	v, ok := m[key]
	if !ok {
		return nil, nil
	}
	sm, ok := v.(map[string]string)
	if !ok {
		return nil, fmt.Errorf("%q must be a map[string]string", key)
	}
	return sm, nil
}

func (m mapObj) Obj(key string) (Obj, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return mapObj(sub), true
}

func (m mapObj) Objs(key string) []Obj {
	v, ok := m[key]
	if !ok {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []Obj
	for _, it := range list {
		if sub, ok := it.(map[string]any); ok {
			out = append(out, mapObj(sub))
		}
	}
	return out
}

func (m mapObj) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (m mapObj) CallStrs(key string, args ...string) ([]string, error) {
	v, ok := m[key]
	if !ok {
		return nil, nil
	}
	if ss, ok := v.([]string); ok {
		return ss, nil
	}
	if fn, ok := v.(func([]string) ([]string, error)); ok {
		return fn(args)
	}
	return nil, nil
}

// TestDecode_NoName ensures a missing name field returns an error.
func TestDecode_NoName(t *testing.T) {
	_, err := Decode(mapObj{})
	require.Error(t, err, "Decode with no name: want error, got nil")
	assert.Contains(t, err.Error(), "name is required")
}

// TestDecode_NameOnly checks that a spell with only a name and no ops is decoded correctly.
func TestDecode_NameOnly(t *testing.T) {
	src := mapObj{"name": "myspell"}
	m, err := Decode(src)
	require.NoError(t, err)
	assert.Equal(t, "myspell", m.Name)
	assert.Nil(t, m.Ops)
}

// TestDecode_CommandOp verifies a fork op (cmd and args, no fn) populates the Target correctly.
func TestDecode_CommandOp(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"build": map[string]any{
				"bin":  "go",
				"args": []string{"build", "./..."},
			},
		},
	}
	m, err := Decode(src)
	require.NoError(t, err)
	tgt, ok := m.Ops["build"]
	require.True(t, ok, `Targets["build"] missing`)
	assert.Equal(t, "go", tgt.Bin)
	assert.Equal(t, []string{"build", "./..."}, tgt.Args)
}

// TestDecode_CommandSecrets verifies a record op's `secrets` map (env var name ->
// provider reference) decodes onto Op.Secrets untouched  -  no resolution happens at
// decode time, only at spawn.
func TestDecode_CommandSecrets(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"publish": map[string]any{
				"bin":     "npm",
				"args":    []string{"publish"},
				"secrets": map[string]string{"NPM_TOKEN": "NPM_TOKEN"},
			},
		},
	}
	m, err := Decode(src)
	require.NoError(t, err)
	tgt, ok := m.Ops["publish"]
	require.True(t, ok, `Ops["publish"] missing`)
	assert.Equal(t, map[string]string{"NPM_TOKEN": "NPM_TOKEN"}, tgt.Secrets)
}

// TestDecode_CommandSecretsAbsentOrEmptyIsNil pins that a command with no `secrets`
// key, and one with an explicitly empty map, both decode Op.Secrets to nil - not an
// empty non-nil map - so a command with nothing to inject looks identical either way.
func TestDecode_CommandSecretsAbsentOrEmptyIsNil(t *testing.T) {
	absent := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"build": map[string]any{"bin": "go", "args": []string{"build"}},
		},
	}
	m, err := Decode(absent)
	require.NoError(t, err)
	assert.Nil(t, m.Ops["build"].Secrets)

	empty := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"build": map[string]any{"bin": "go", "args": []string{"build"}, "secrets": map[string]string{}},
		},
	}
	m, err = Decode(empty)
	require.NoError(t, err)
	assert.Nil(t, m.Ops["build"].Secrets)
}

// TestDecode_CommandSecretsWrongTypeErrorsLoudly pins that a `secrets` field of the
// wrong shape is a load-time error naming the spell and op, matching decodeCommand's
// existing charm error style - not a silently dropped declaration that would leave a
// spawn with no secret and no signal why.
func TestDecode_CommandSecretsWrongTypeErrorsLoudly(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"publish": map[string]any{
				"bin":     "npm",
				"args":    []string{"publish"},
				"secrets": "not-a-map",
			},
		},
	}
	_, err := Decode(src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `spell "myspell"`)
	assert.Contains(t, err.Error(), `op "publish"`)
}

// TestDecode_CharmReplaceOp checks that a charm carrying a replace patch op is
// decoded into the canonical spells.PatchOp.
func TestDecode_CharmReplaceOp(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"fmt": map[string]any{
				"bin":  "gofmt",
				"args": []string{"-l", "."},
				"charms": map[string]any{
					"write": map[string]any{
						"ops": []any{
							map[string]any{"op": "replace", "path": "/0", "value": "-w"},
						},
					},
				},
			},
		},
	}
	m, err := Decode(src)
	require.NoError(t, err)
	tgt, ok := m.Ops["fmt"]
	require.True(t, ok, `Targets["fmt"] missing`)
	charm, ok := tgt.Charms["write"]
	require.True(t, ok, `Charms["write"] missing`)
	assert.Equal(t, []spells.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}, charm.Ops)
}

// TestDecode_CharmAddOp checks that a charm carrying an append patch op (add /-)
// is decoded into the canonical spells.PatchOp.
func TestDecode_CharmAddOp(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"test": map[string]any{
				"bin":  "go",
				"args": []string{"test", "./..."},
				"charms": map[string]any{
					"debug": map[string]any{
						"ops": []any{
							map[string]any{"op": "add", "path": "/-", "value": "-v"},
						},
					},
				},
			},
		},
	}
	m, err := Decode(src)
	require.NoError(t, err)
	charm, ok := m.Ops["test"].Charms["debug"]
	require.True(t, ok, `Charms["debug"] missing`)
	assert.Equal(t, []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}, charm.Ops)
}

// TestDecode_CharmRootRejected checks that a root-path op (whole-argv replace)
// is rejected  -  element-level only.
func TestDecode_CharmRootRejected(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"fmt": map[string]any{
				"bin": "gofmt",
				"charms": map[string]any{
					"write": map[string]any{
						"ops": []any{
							map[string]any{"op": "replace", "path": "", "value": "x"},
						},
					},
				},
			},
		},
	}
	_, err := Decode(src)
	assert.Error(t, err, "Decode with root-path charm op: want error, got nil")
}

// TestDecode_InvalidTargetName ensures ops with invalid names (e.g. containing spaces) are rejected.
func TestDecode_InvalidTargetName(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"has space": map[string]any{
				"bin": "echo",
			},
		},
	}
	_, err := Decode(src)
	assert.Error(t, err, "Decode with invalid target name: want error, got nil")
}

// TestDecode_NeedsResolved verifies that CallStrs("needs") is called and stored.
func TestDecode_NeedsResolved(t *testing.T) {
	src := mapObj{
		"name":  "myspell",
		"needs": []string{"**/*.go", "go.mod"},
	}
	m, err := Decode(src)
	require.NoError(t, err)
	assert.Equal(t, []string{"**/*.go", "go.mod"}, m.Needs)
}

// TestDecode_IgnoreDirs verifies the ignore_dirs field (mgs_listIgnoreDirs) is read
// into spells.Descriptor.IgnoreDirs, and that an absent field decodes to nil (not a panic or
// empty-slice surprise) - the path a spell that declares no ignore dirs takes.
func TestDecode_IgnoreDirs(t *testing.T) {
	src := mapObj{
		"name":        "myspell",
		"ignore_dirs": []string{"vendor", "target"},
	}
	m, err := Decode(src)
	require.NoError(t, err)
	assert.Equal(t, []string{"vendor", "target"}, m.IgnoreDirs)

	m, err = Decode(mapObj{"name": "bare"})
	require.NoError(t, err)
	assert.Nil(t, m.IgnoreDirs, "a spell with no ignore_dirs must decode to nil")
}

// TestDecode_OpNameIsNormalized guards a silent-unreachability bug: ValidateTargetName
// admits '_' and uppercase, but every request reaching dispatchOp has already been
// kebab-normalized by ParseTarget and dispatch is a plain map hit. An op authored as
// go_build was therefore stored under go_build, looked up as go-build, missed, and
// swallowed as a fan-out skip at debug level - declared, and reachable by nothing.
func TestDecode_OpNameIsNormalized(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"go_build": map[string]any{"bin": "go", "args": []string{"build"}},
			"goVet":    map[string]any{"bin": "go", "args": []string{"vet"}},
		},
	}
	d, err := Decode(src)
	require.NoError(t, err)

	// Stored canonically, not as authored.
	require.ElementsMatch(t, []string{"go-build", "go-vet"}, d.OpNames())

	// And reachable by what a request actually carries.
	for _, authored := range []string{"go_build", "goVet"} {
		requested, perr := types.ParseTarget(authored)
		require.NoErrorf(t, perr, "ParseTarget(%q)", authored)
		_, ok := d.Ops[requested.Name]
		assert.Truef(t, ok, "op authored %q must be reachable as %q", authored, requested.Name)
	}
}

// TestDecode_OpNameCollisionIsAnError guards the other half of op-name
// normalization. Collapsing keys to canonical form means two spellings can land on
// one, and a plain map assignment would resolve that by last-write-wins: `go-build`
// and `goBuild` in one spell used to be two ops (one unreachable), and silently
// became a single op whose body came from whichever key iteration reached last.
// That is the same silent loss normalization exists to end, moved to the other side
// - so it is a load error naming both spellings.
func TestDecode_OpNameCollisionIsAnError(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"go-build": map[string]any{"bin": "go", "args": []string{"FIRST"}},
			"goBuild":  map[string]any{"bin": "go", "args": []string{"SECOND"}},
		},
	}
	_, err := Decode(src)
	require.Error(t, err, "two spellings of one canonical op must not decode silently")
	assert.Contains(t, err.Error(), "go-build", "the error names the canonical op")
	assert.Contains(t, err.Error(), "goBuild", "and both authored spellings")

	// A spell that declares each op once still decodes, whatever the spelling.
	ok := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"go-build": map[string]any{"bin": "go", "args": []string{"build"}},
			"goVet":    map[string]any{"bin": "go", "args": []string{"vet"}},
		},
	}
	d, err := Decode(ok)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"go-build", "go-vet"}, d.OpNames())
}

// A spell declaring no tools keeps the conservative default, so every spell that says
// nothing about its toolchain decodes exactly as before.
func TestDecodeToolsAbsent(t *testing.T) {
	m, err := Decode(mapObj{"name": "go"})
	require.NoError(t, err)
	assert.Empty(t, m.Tools)
}

// Everything magus knows about one binary arrives in one entry, keyed by the bin an op
// already names - which is what replaced five parallel declarations.
func TestDecodeToolsCarriesProbeKeyAndReady(t *testing.T) {
	m, err := Decode(mapObj{
		"name": "docker",
		"tools": map[string]any{
			"docker": map[string]any{
				"probe": map[string]any{"bin": "docker", "args": []string{"--version"}},
				"key":   map[string]any{"upTo": "patch"},
				"ready": map[string]any{"bin": "docker", "args": []string{"info"}},
			},
			"hadolint": map[string]any{
				"probe": map[string]any{"bin": "hadolint", "args": []string{"--version"}},
			},
		},
	})
	require.NoError(t, err)

	d := m.Tools["docker"]
	assert.Equal(t, "docker", d.Probe.Bin)
	assert.Equal(t, spells.VersionPatch, d.Key.UpTo)
	assert.Equal(t, "info", d.Ready.Args[0])

	// The asymmetry that motivated per-tool scoping: hadolint is gated by nothing.
	h := m.Tools["hadolint"]
	assert.Equal(t, "hadolint", h.Probe.Bin)
	assert.Empty(t, h.Ready.Bin, "linting a Dockerfile must not wait on the docker daemon")
}

// A malformed probe or ready command must be a load error, not a silently dropped
// declaration: decodeTools used to swallow decodeCommand's error with `err == nil`,
// which left a typo'd tool command decoding as "no command at all" with nothing to
// tell the author why the tool never gets probed.
func TestDecodeToolsPropagatesMalformedProbeError(t *testing.T) {
	_, err := Decode(mapObj{
		"name": "docker",
		"tools": map[string]any{
			"docker": map[string]any{
				"probe": map[string]any{"bin": "docker", "args": []string{"--version"}, "secrets": "not-a-map"},
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `spell "docker"`)
	assert.Contains(t, err.Error(), `tools["docker"].probe`)
}

func TestDecodeToolsPropagatesMalformedReadyError(t *testing.T) {
	_, err := Decode(mapObj{
		"name": "docker",
		"tools": map[string]any{
			"docker": map[string]any{
				"ready": map[string]any{"bin": "docker", "args": []string{"info"}, "secrets": "not-a-map"},
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `spell "docker"`)
	assert.Contains(t, err.Error(), `tools["docker"].ready`)
}

// A constant stands in for a tool that cannot report a version, and needs no probe.
func TestDecodeToolsConstNeedsNoProbe(t *testing.T) {
	m, err := Decode(mapObj{
		"name":  "x",
		"tools": map[string]any{"protoc-gen-go": map[string]any{"key": map[string]any{"const": "protoc-gen-go-1"}}},
	})
	require.NoError(t, err)
	tool := m.Tools["protoc-gen-go"]
	assert.Equal(t, "protoc-gen-go-1", tool.Key.Const)
	assert.True(t, tool.HasProbe(), "a constant is a version magus can key on")
}

// The whole point of validating at decode: a typo'd component would otherwise leave a
// spell claiming "major" while the cache silently kept keying on the whole output.
func TestDecodeToolsRejectsUnknownComponent(t *testing.T) {
	_, err := Decode(mapObj{
		"name":  "go",
		"tools": map[string]any{"go": map[string]any{"key": map[string]any{"upTo": "mayor"}}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `tools["go"].key.upTo`)
	assert.Contains(t, err.Error(), "mayor")
}

// An entry declaring nothing at all is dropped rather than kept as a tool magus knows
// nothing about.
func TestDecodeToolsDropsEmptyEntries(t *testing.T) {
	m, err := Decode(mapObj{"name": "go", "tools": map[string]any{"gofmt": map[string]any{}}})
	require.NoError(t, err)
	assert.Empty(t, m.Tools)
}

// A tool declaring only a diagnostics format is kept: the entry says something magus
// acts on, so the drop-empty-entries rule must not treat it as saying nothing.
func TestDecodeToolsCarriesDiagnosticsFormat(t *testing.T) {
	m, err := Decode(mapObj{
		"name": "docker",
		"tools": map[string]any{
			"hadolint": map[string]any{"diagnostics": "gnu"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, spells.DiagnosticGNU, m.Tools["hadolint"].Diagnostics)
}

// An unrecognized format is a spell-authoring bug caught at decode, the same place a
// bad key.upTo lands - not a silent fall back to scraping prose.
func TestDecodeToolsRejectsUnknownDiagnosticsFormat(t *testing.T) {
	_, err := Decode(mapObj{
		"name": "docker",
		"tools": map[string]any{
			"hadolint": map[string]any{"diagnostics": "sarif"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "diagnostics is sarif")
	assert.Contains(t, err.Error(), "gnu", "the message names the formats magus accepts")
}

// TestDecodeCommandHints pins that failure hints survive decode in DECLARATION order (that
// order is the precedence) and that a half-written rule is refused rather than dropped.
func TestDecodeCommandHints(t *testing.T) {
	t.Run("hints decode in declaration order", func(t *testing.T) {
		cmd, err := decodeCommand("docker", "docker-buildx", mapObj{
			"bin": "docker",
			"hints": []any{
				map[string]any{"contains": "denied: requested access", "advise": "specific"},
				map[string]any{"contains": "denied", "advise": "general"},
			},
		})
		require.NoError(t, err)
		require.Len(t, cmd.Hints, 2)
		assert.Equal(t, "specific", cmd.Hints[0].Advise, "order must not be sorted away - it is the precedence")
		assert.Equal(t, "denied", cmd.Hints[1].Contains)
	})
	t.Run("no hints decodes to nil", func(t *testing.T) {
		cmd, err := decodeCommand("docker", "docker-build", mapObj{"bin": "docker"})
		require.NoError(t, err)
		assert.Nil(t, cmd.Hints)
	})
	t.Run("a hint with no contains is refused", func(t *testing.T) {
		// Would otherwise fire on EVERY failure of this command.
		_, err := decodeCommand("s", "o", mapObj{"hints": []any{map[string]any{"advise": "do the thing"}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both contains and advise are required")
	})
	t.Run("a contains with no advise is refused", func(t *testing.T) {
		// Would otherwise consume the match and print nothing, reading as "no idea".
		_, err := decodeCommand("s", "o", mapObj{"hints": []any{map[string]any{"contains": "denied"}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both contains and advise are required")
	})
}
