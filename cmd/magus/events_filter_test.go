package main

import (
	"errors"
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/journal/stream"
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
	assert.Equal(t, []stream.EventType{stream.TargetResult, stream.RunStarted}, f.Types)
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
	emit := RecordEmitter(stream.Filter{}, func(_ any, names []string) error {
		gotNames = append(gotNames, names)
		return nil
	})
	require.NoError(t, emit(stream.Event{Body: stream.Target{Project: "api"}}))
	assert.Equal(t, [][]string{{string(stream.TargetResult)}}, gotNames)
}

func TestRecordEmitterDropsFilteredAndBodylessEvents(t *testing.T) {
	calls := 0
	emit := RecordEmitter(stream.Filter{Types: []stream.EventType{stream.TargetOutput}}, func(any, []string) error {
		calls++
		return nil
	})
	require.NoError(t, emit(stream.Event{Body: stream.Target{}}))
	require.NoError(t, emit(stream.Event{}))
	assert.Zero(t, calls)
}

func TestRecordEmitterReturnsTheRendererError(t *testing.T) {
	want := errors.New("closed pipe")
	emit := RecordEmitter(stream.Filter{}, func(any, []string) error { return want })
	assert.ErrorIs(t, emit(stream.Event{Body: stream.Target{}}), want)
}
