// Without the allow glob this narrows widget.go and is reported.
package allowed

import "testing"

func TestWidgetConformance(t *testing.T) { _ = Widget{} }
