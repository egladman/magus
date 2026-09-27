// Package queue is a stand-in for a governed directory ("queue" governed by the
// test's Options.Dirs), exercising every violation and pass case a real
// internal/queue file would meet.
package queue

import (
	"context"
	"net/http"
)

// bareClient is the plain construction case.
var bareClient = &http.Client{Timeout: 0} // want `constructs an HTTP client or request`

// clientField carries the type reference alone, as an injected dependency does.
type follower struct {
	Client *http.Client // want `constructs an HTTP client or request`
}

func fetch(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) // want `constructs an HTTP client or request`
	if err != nil {
		return err
	}
	_, err = http.DefaultClient.Do(req) // want `constructs an HTTP client or request`
	return err
}

// serverSide is the shape a governed handler legitimately takes: net/http types
// that serve rather than call out. None of these are in httpNames, so nothing
// here is reported.
func serverSide(w http.ResponseWriter, r *http.Request) {
	_ = w
	_ = r
}
