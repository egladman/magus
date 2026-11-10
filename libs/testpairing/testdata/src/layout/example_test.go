package layout_test

import "layout"

// ExampleWidget is alone in its file, so the file keeps its external package:
// an example shows the package as a caller imports it.
func ExampleWidget() { _ = layout.Widget{} }
