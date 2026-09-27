// Package blank imports net/http blank, inside a governed dir. There is no
// selector to resolve to it, so nothing is reported; the case exists to prove
// httpLocalName does not panic on a blank import.
package blank

import _ "net/http"
