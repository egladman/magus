package eventstream

import (
	"fmt"
	"strings"
	"sync"

	eventstypes "github.com/egladman/magus/types/events"
)

// RecordEmitter renders each event alone as it arrives. The stream never
// closes, so no document could wrap it. A -o name line is the event's type.
// emit renders one record under those names; the caller owns the format.
func RecordEmitter(filter eventstypes.Filter, emit func(v any, names []string) error) func(eventstypes.Event) error {
	var mu sync.Mutex
	return func(e eventstypes.Event) error {
		if e.Body == nil || !filter.Allows(e.Body.StreamType()) {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		return emit(e, []string{string(e.Body.StreamType())})
	}
}

// ParseFilter turns the --type list into a filter, refusing an unknown
// name.
//
// A typo has to be an error rather than an empty match: `--type target.reslut`
// would otherwise present as a workspace where nothing ever happens, which is
// indistinguishable from a broken integration and costs an hour to diagnose.
func ParseFilter(csv string) (eventstypes.Filter, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return eventstypes.Filter{}, nil
	}
	var out eventstypes.Filter
	for _, name := range strings.Split(csv, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		t, err := eventstypes.ParseStreamEventType(name)
		if err != nil {
			return eventstypes.Filter{}, fmt.Errorf("magus events (known types: %s): %w", strings.Join(typeNames(), ", "), err)
		}
		out.Types = append(out.Types, t)
	}
	return out, nil
}

// typeNames renders the taxonomy for an error message.
func typeNames() []string {
	all := eventstypes.EventTypes()
	names := make([]string, len(all))
	for i, t := range all {
		names[i] = string(t)
	}
	return names
}
