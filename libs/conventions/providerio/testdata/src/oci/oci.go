// Package oci stands in for a magus-own remote service OUTSIDE the test's
// governed dirs (Options.Dirs names only "queue"): the same construction as
// queue.go, but nothing here is reported because the rule does not govern it.
package oci

import "net/http"

var client = &http.Client{}
