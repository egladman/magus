package eventstream

import (
	"errors"
	"testing"

	eventstypes "github.com/egladman/magus/types/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFilterEmptyAllowsEverything(t *testing.T) {
	f, err := ParseFilter("  ")
	require.NoError(t, err)
	assert.Empty(t, f.Types)
}

func TestParseFilterTrimsAndSkipsEmptyNames(t *testing.T) {
	f, err := ParseFilter(" target.result , ,run.started,")
	require.NoError(t, err)
	assert.Equal(t, []eventstypes.EventType{eventstypes.TargetResult, eventstypes.RunStarted}, f.Types)
}

func TestParseFilterRefusesATypoAndListsTheTaxonomy(t *testing.T) {
	_, err := ParseFilter("target.reslut")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "magus events (known types: ")
	for _, name := range typeNames() {
		assert.Contains(t, err.Error(), name)
	}
}

func TestRecordEmitterNamesEachEventByItsType(t *testing.T) {
	var gotNames [][]string
	emit := RecordEmitter(eventstypes.Filter{}, func(_ any, names []string) error {
		gotNames = append(gotNames, names)
		return nil
	})
	require.NoError(t, emit(eventstypes.Event{Body: eventstypes.Target{Project: "api"}}))
	assert.Equal(t, [][]string{{string(eventstypes.TargetResult)}}, gotNames)
}

func TestRecordEmitterDropsFilteredAndBodylessEvents(t *testing.T) {
	calls := 0
	emit := RecordEmitter(eventstypes.Filter{Types: []eventstypes.EventType{eventstypes.TargetOutput}}, func(any, []string) error {
		calls++
		return nil
	})
	require.NoError(t, emit(eventstypes.Event{Body: eventstypes.Target{}}))
	require.NoError(t, emit(eventstypes.Event{}))
	assert.Zero(t, calls)
}

func TestRecordEmitterReturnsTheRendererError(t *testing.T) {
	want := errors.New("closed pipe")
	emit := RecordEmitter(eventstypes.Filter{}, func(any, []string) error { return want })
	assert.ErrorIs(t, emit(eventstypes.Event{Body: eventstypes.Target{}}), want)
}
