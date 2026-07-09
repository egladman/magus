package main

import (
	"testing"
)

// TestEncodeFragmentDeterminism confirms that encodeFragment produces byte-for-byte
// identical output for the same input across two calls. This relies on gzip.NewWriter
// leaving the header ModTime at its zero value by default, so the compressed stream
// is deterministic - a necessary property for stable #data= URL fragments in MAGUS.md.
func TestEncodeFragmentDeterminism(t *testing.T) {
	payload := []byte(`{"projects":[{"path":"pkg/foo","engine":"buzz","nodes":[{"name":"build","dependencies":["fmt"]},{"name":"fmt"}]}]}`)

	first, err := encodeFragment(payload)
	if err != nil {
		t.Fatalf("first encodeFragment: %v", err)
	}
	second, err := encodeFragment(payload)
	if err != nil {
		t.Fatalf("second encodeFragment: %v", err)
	}
	if first != second {
		t.Errorf("encodeFragment is not deterministic:\n  first:  %s\n  second: %s", first, second)
	}
}
