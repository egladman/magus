// Package queue also holds allowed.go, the file the test's Options.Allow names:
// the same construction queue.go reports, exempted here by path.
package queue

import "net/http"

var exemptClient = &http.Client{}
