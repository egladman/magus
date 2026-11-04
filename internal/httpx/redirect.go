package httpx

import (
	"fmt"
	"net/http"
	"strings"
)

// maxRedirects is how many redirects GuardRedirect follows before stopping.
const maxRedirects = 10

// GuardRedirect is an http.Client CheckRedirect. It refuses a redirect from https to any
// other scheme, stops after maxRedirects, and strips credential headers when the target
// host differs from the first request's.
//
// Go itself strips only Authorization and Cookie across hosts, and a caller may carry its
// credential in any header.
func GuardRedirect(next *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if via[len(via)-1].URL.Scheme == "https" && next.URL.Scheme != "https" {
		return fmt.Errorf("refused a redirect from https to %s", next.URL.Redacted())
	}
	if next.URL.Host != via[0].URL.Host {
		for name := range next.Header {
			if isCredentialHeader(name) {
				next.Header.Del(name)
			}
		}
	}
	return nil
}

// isCredentialHeader reports whether a header name looks like it carries a credential.
func isCredentialHeader(name string) bool {
	lower := strings.ToLower(name)
	for _, word := range []string{"authorization", "cookie", "token", "secret", "key", "auth", "password", "credential"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}
