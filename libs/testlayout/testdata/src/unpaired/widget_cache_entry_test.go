package unpaired // want `widget_cache_entry_test.go narrows widget.go; these tests belong in widget_test.go`

import "testing"

// nearestSource has to iterate twice here: neither widget_cache_entry nor
// widget_cache is a source file, widget is. The narrowing message must win over
// the unpaired one even though Unpaired is set for this package.
func TestWidgetCacheEntry(t *testing.T) { _ = Widget{} }
