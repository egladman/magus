package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// writeFindings writes out to w in format.
func writeFindings(w io.Writer, format string, out []finding) error {
	if format != "json" {
		return fmt.Errorf("unknown -format %q: want json", format)
	}

	if err := json.NewEncoder(w).Encode(out); err != nil {
		return fmt.Errorf("write findings: %w", err)
	}

	return nil
}
