package describe

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/json"
)

// ParseSchema decodes an embedded JSON Schema document and returns it with the sorted
// names of its top-level properties, the two shapes a --schema flag renders besides the
// raw text.
func ParseSchema(raw string) (doc map[string]any, names []string, err error) {
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, nil, fmt.Errorf("parse embedded schema: %w", err)
	}
	props, _ := doc["properties"].(map[string]any)
	for k := range props {
		names = append(names, k)
	}
	slices.Sort(names)
	return doc, names, nil
}
