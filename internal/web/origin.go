package web

import (
	"fmt"
	"net/url"
)

// Origin extracts the scheme://host[:port] origin from a page's base URL, for the
// loopback server's CORS Allow-Origin. An unparseable or non-absolute base is a user
// error worth surfacing rather than defaulting to a permissive value.
func Origin(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%q is not a valid absolute URL", base)
	}
	return u.Scheme + "://" + u.Host, nil
}
