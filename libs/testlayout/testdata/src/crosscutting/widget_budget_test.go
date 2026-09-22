// cross-cutting: the marker excuses a missing pair, not a narrowed name

// widget_test.go is the home for these, so the marker changes nothing.
package crosscutting // want `widget_budget_test.go narrows widget.go; these tests belong in widget_test.go`

import "testing"

func TestWidgetBudget(t *testing.T) { _ = Widget{} }
