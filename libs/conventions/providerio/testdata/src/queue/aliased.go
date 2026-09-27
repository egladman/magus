// aliased.go proves the rule resolves net/http by its import's local name, not
// the literal word "http".
package queue

import (
	"context"

	stdhttp "net/http"
)

func aliasedFetch(ctx context.Context, url string) error {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, url, nil) // want `constructs an HTTP client or request`
	_ = req
	return err
}
