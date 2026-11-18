package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egladman/magus/libs/safetyscissors/journal/stream"
)

// RecordEmitter renders each event alone as it arrives. The stream never
// closes, so no document could wrap it. A -o name line is the event's type.
// emit renders one record under those names; the caller owns the format.
func RecordEmitter(filter stream.Filter, emit func(v any, names []string) error) func(stream.Event) error {
	var mu sync.Mutex
	return func(e stream.Event) error {
		if e.Body == nil || !filter.Allows(e.Body.EventType()) {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		return emit(e, []string{string(e.Body.EventType())})
	}
}

// ParseFilter turns the --type list into a filter, refusing an unknown
// name.
//
// A typo has to be an error rather than an empty match: `--type target.reslut`
// would otherwise present as a workspace where nothing ever happens, which is
// indistinguishable from a broken integration and costs an hour to diagnose.
func ParseFilter(csv string) (stream.Filter, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return stream.Filter{}, nil
	}
	var out stream.Filter
	for _, name := range strings.Split(csv, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		t, err := stream.ParseEventType(name)
		if err != nil {
			return stream.Filter{}, fmt.Errorf("magus events (known types: %s): %w", strings.Join(typeNames(), ", "), err)
		}
		out.Types = append(out.Types, t)
	}
	return out, nil
}

// typeNames renders the taxonomy for an error message.
func typeNames() []string {
	all := stream.EventTypes()
	names := make([]string, len(all))
	for i, t := range all {
		names[i] = string(t)
	}
	return names
}
