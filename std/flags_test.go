package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// TestFlagsParse covers every shape the five hook templates hand-rolled separately, which
// is where this module's rules came from: each had its own answer to an argument nobody
// declared, a repeated flag, and a flag given no value.
func TestFlagsParse(t *testing.T) {
	switches := []string{"--observes-skill-loads"}
	valued := []string{"--session"}

	for name, tc := range map[string]struct {
		argv []string
		want types.FlagParse
	}{
		"a declared switch records true": {
			argv: []string{"--observes-skill-loads"},
			want: types.FlagParse{
				Values:      map[string]string{"--observes-skill-loads": "true"},
				Positionals: []string{},
				Unknown:     []string{},
			},
		},
		"a valued flag takes the next word": {
			argv: []string{"--session", "s1"},
			want: types.FlagParse{
				Values:      map[string]string{"--session": "s1"},
				Positionals: []string{},
				Unknown:     []string{},
			},
		},
		"the equals form is the same flag": {
			argv: []string{"--session=s1"},
			want: types.FlagParse{
				Values:      map[string]string{"--session": "s1"},
				Positionals: []string{},
				Unknown:     []string{},
			},
		},
		// Split on the FIRST =, so a session id carrying one survives.
		"a value may contain an equals": {
			argv: []string{"--session=a=b"},
			want: types.FlagParse{
				Values:      map[string]string{"--session": "a=b"},
				Positionals: []string{},
				Unknown:     []string{},
			},
		},
		// Left to right: an argv built by concatenation puts the override nearer the end.
		"a repeated flag keeps the last": {
			argv: []string{"--session", "first", "--session", "second"},
			want: types.FlagParse{
				Values:      map[string]string{"--session": "second"},
				Positionals: []string{},
				Unknown:     []string{},
			},
		},
		// The separator ends parsing: a word after it is data even when it is spelled
		// like a flag this call declares.
		"the separator makes everything after it positional": {
			argv: []string{"--observes-skill-loads", "--", "--session", "raw"},
			want: types.FlagParse{
				Values:      map[string]string{"--observes-skill-loads": "true"},
				Positionals: []string{"--session", "raw"},
				Unknown:     []string{},
			},
		},
		"an undeclared argument is returned, not guessed at": {
			argv: []string{"--not-a-flag"},
			want: types.FlagParse{
				Values:      map[string]string{},
				Positionals: []string{},
				Unknown:     []string{"--not-a-flag"},
			},
		},
		// A leading dash does not make a word a flag: only declaring it does. A bare word
		// is as unrecognized as a dashed one, and both come back the same way.
		"a word with no dash is unknown too": {
			argv: []string{"bare"},
			want: types.FlagParse{
				Values:      map[string]string{},
				Positionals: []string{},
				Unknown:     []string{"bare"},
			},
		},
		// The case a host config produces by expanding a variable that is unset. It is
		// REPORTED rather than skipped: a caller that never hears about it cannot say so.
		"an empty argument is kept": {
			argv: []string{""},
			want: types.FlagParse{
				Values:      map[string]string{},
				Positionals: []string{},
				Unknown:     []string{""},
			},
		},
		"no argv at all": {
			argv: nil,
			want: types.FlagParse{
				Values:      map[string]string{},
				Positionals: []string{},
				Unknown:     []string{},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := FlagsParse(context.Background(), tc.argv, switches, valued, nil, nil, false)
			require.NoError(t, err)
			if tc.want.Lists == nil {
				tc.want.Lists = map[string][]string{}
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// A valued flag with nothing after it ERRORS rather than recording "". A flag whose value
// silently became empty is what makes a misconfigured call look like a configured one,
// which is the failure this module exists to stop repeating.
func TestFlagsParseRefusesAValuedFlagWithNoValue(t *testing.T) {
	_, err := FlagsParse(context.Background(), []string{"--session"}, nil, []string{"--session"}, nil, nil, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--session takes a value and none followed it")
}

// A required flag refuses both absence and an empty value: `--issue "$ISSUE"` with the
// variable unset is a misconfigured call, not a decision to edit no issue.
func TestFlagsParseRefusesAnAbsentOrEmptyRequiredFlag(t *testing.T) {
	valued := []string{"--issue", "--out"}
	required := []string{"--issue"}
	for name, argv := range map[string][]string{
		"absent": {"--out", "d.md"},
		"empty":  {"--issue", "", "--out", "d.md"},
		"=empty": {"--issue=", "--out", "d.md"},
	} {
		_, err := FlagsParse(context.Background(), argv, nil, valued, required, nil, false)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "--issue required and absent or empty", name)
	}

	got, err := FlagsParse(context.Background(), []string{"--issue", "12"}, nil, valued, required, nil, false)
	require.NoError(t, err)
	assert.Equal(t, "12", got.Values["--issue"])

	_, err = FlagsParse(context.Background(), nil, []string{"--all"}, valued, []string{"--all"}, nil, false)
	require.ErrorContains(t, err, "a switch cannot be required")
}

// The empty groups come back as empty lists rather than nil, so a script reads an absent
// group without a null check every caller would otherwise write.
func TestFlagsParseReturnsEmptyGroupsNotNull(t *testing.T) {
	got, err := FlagsParse(context.Background(), nil, nil, nil, nil, nil, false)

	require.NoError(t, err)
	assert.NotNil(t, got.Values)
	assert.NotNil(t, got.Lists)
	assert.NotNil(t, got.Positionals)
	assert.NotNil(t, got.Unknown)
}

// A repeated flag collects every value in order, both spellings, and never lands in
// Values; required holds for it when one non-empty value arrived.
func TestFlagsParseCollectsARepeatedFlag(t *testing.T) {
	got, err := FlagsParse(context.Background(), []string{"--env", "A", "--env=B", "--image", "x"}, nil,
		[]string{"--image"}, []string{"--env"}, []string{"--env"}, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "B"}, got.Lists["--env"])
	assert.Equal(t, map[string]string{"--image": "x"}, got.Values)

	_, err = FlagsParse(context.Background(), []string{"--image", "x"}, nil, []string{"--image"}, []string{"--env"}, []string{"--env"}, false)
	require.ErrorContains(t, err, "--env required and absent or empty")
}

// Command mode reads argv the way sudo and Go's flag package do: the first bare word
// starts the command, which keeps its own flags verbatim, and the script's flags after
// it are the command's.
func TestFlagsParseCommandModeKeepsTheCommandVerbatim(t *testing.T) {
	argv := []string{"--keep", "--arch", "arm64", "magus", "run", "test", "--keep", "-s"}
	got, err := FlagsParse(context.Background(), argv, []string{"--keep"}, []string{"--arch"}, nil, nil, true)
	require.NoError(t, err)
	assert.Equal(t, types.FlagParse{
		Values:      map[string]string{"--keep": "true", "--arch": "arm64"},
		Lists:       map[string][]string{},
		Positionals: []string{"magus", "run", "test", "--keep", "-s"},
		Unknown:     []string{},
	}, got)

	got, err = FlagsParse(context.Background(), []string{"--bogus", "ls"}, nil, nil, nil, nil, true)
	require.NoError(t, err)
	// An undeclared flag before the command is still unknown.
	assert.Equal(t, types.FlagParse{
		Values:      map[string]string{},
		Lists:       map[string][]string{},
		Positionals: []string{"ls"},
		Unknown:     []string{"--bogus"},
	}, got)
}
