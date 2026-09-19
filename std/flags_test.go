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
			got, err := FlagsParse(context.Background(), tc.argv, switches, valued)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// A valued flag with nothing after it ERRORS rather than recording "". A flag whose value
// silently became empty is what makes a misconfigured call look like a configured one,
// which is the failure this module exists to stop repeating.
func TestFlagsParseRefusesAValuedFlagWithNoValue(t *testing.T) {
	_, err := FlagsParse(context.Background(), []string{"--session"}, nil, []string{"--session"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--session takes a value and none followed it")
}

// The empty groups come back as empty lists rather than nil, so a script reads an absent
// group without a null check every caller would otherwise write.
func TestFlagsParseReturnsEmptyGroupsNotNull(t *testing.T) {
	got, err := FlagsParse(context.Background(), nil, nil, nil)

	require.NoError(t, err)
	assert.NotNil(t, got.Values)
	assert.NotNil(t, got.Positionals)
	assert.NotNil(t, got.Unknown)
}
