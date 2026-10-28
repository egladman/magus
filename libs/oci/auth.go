package oci

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// challenge is what a registry's unauthenticated /v2/ asks for, per the distribution
// spec's token authentication: scheme "bearer" with the realm that issues tokens and
// the service they are for, "basic", or "" when the registry answers without asking.
type challenge struct {
	scheme  string
	realm   string
	service string
}

// tokenKey is one bearer token's identity. A token is scoped to one repository and
// one set of actions, so a pull token is never replayed for a push.
type tokenKey struct {
	realm, service, scope string
}

// authorize returns the Authorization value for actions ("pull" or "push,pull") on
// ref's repository, or "" when the registry asks for none.
//
// Anonymous pull of a PUBLIC repository still goes through the token flow: the
// registry answers an unauthenticated /v2/ with 401 and expects the client to come
// back with a token it hands to anybody. So "no credentials" is not "no token".
//
// The challenge is cached per registry and each token per (realm, service, scope), so
// a push that uploads several blobs asks once. An expired token is dropped by forget when
// a request answers 401.
func (c *Client) authorize(ctx context.Context, ref Reference, actions string) (string, error) {
	ch, err := c.challenge(ctx, ref.endpoint())
	if err != nil {
		return "", err
	}
	switch ch.scheme {
	case "":
		return "", nil
	case "basic":
		if c.Credentials.isZero() {
			return "", fmt.Errorf("oci: %s asks for Basic credentials and none are configured", ref.Registry)
		}
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", c.Credentials.Username, c.Credentials.Password.Reveal()))), nil
	}
	key := tokenKey{realm: ch.realm, service: ch.service, scope: fmt.Sprintf("repository:%s:%s", ref.Repository, actions)}
	c.mu.Lock()
	tok, ok := c.tokens[key]
	c.mu.Unlock()
	if !ok {
		tok, err = c.fetchToken(ctx, key)
		if err != nil {
			return "", fmt.Errorf("token for %s: %w", ref.Repository, err)
		}
		c.mu.Lock()
		if c.tokens == nil {
			c.tokens = map[tokenKey]types.Secret{}
		}
		c.tokens[key] = tok
		c.mu.Unlock()
	}
	return "Bearer " + tok.Reveal(), nil
}

// forget drops the cached bearer token that auth names and reports whether a retry can
// help: only a bearer token can be refreshed. A token already replaced by another request
// is left in place.
func (c *Client) forget(ref Reference, actions, auth string) bool {
	bearer, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, cached := range c.tokens {
		if cached.Reveal() == bearer && key.scope == fmt.Sprintf("repository:%s:%s", ref.Repository, actions) {
			delete(c.tokens, key)
		}
	}
	return true
}

// challenge pings host's /v2/ without credentials and reads what it asks for.
func (c *Client) challenge(ctx context.Context, host string) (challenge, error) {
	c.mu.Lock()
	cached, ok := c.challenges[host]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s/v2/", host), nil)
	if err != nil {
		return challenge{}, err
	}
	resp, err := c.do(req)
	if err != nil {
		return challenge{}, fmt.Errorf("reach %s: %w", host, err)
	}
	defer resp.Body.Close()
	var ch challenge
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		if ch, err = readChallenge(resp.Header.Values("WWW-Authenticate")); err != nil {
			return challenge{}, fmt.Errorf("%s: %w", host, err)
		}
	default:
		return challenge{}, fmt.Errorf("oci: reach %s: %w", host, statusError(resp))
	}
	c.mu.Lock()
	if c.challenges == nil {
		c.challenges = map[string]challenge{}
	}
	c.challenges[host] = ch
	c.mu.Unlock()
	return ch, nil
}

// readChallenge picks the challenge to answer from a 401's WWW-Authenticate values,
// preferring Bearer. A Bearer realm must be https: the client sends its credentials
// there.
func readChallenge(headers []string) (challenge, error) {
	var basic bool
	for _, h := range headers {
		scheme, params := parseChallenge(h)
		switch {
		case strings.EqualFold(scheme, "bearer"):
			realm, err := url.Parse(params["realm"])
			if err != nil || realm.Scheme != "https" || realm.Host == "" {
				return challenge{}, fmt.Errorf("bearer challenge names no https realm (%q)", params["realm"])
			}
			return challenge{scheme: "bearer", realm: realm.String(), service: params["service"]}, nil
		case strings.EqualFold(scheme, "basic"):
			basic = true
		}
	}
	if basic {
		return challenge{scheme: "basic"}, nil
	}
	return challenge{}, fmt.Errorf("401 with no Bearer or Basic challenge (WWW-Authenticate: %q)", headers)
}

// parseChallenge splits one RFC 7235 challenge into its scheme and lowercased
// parameters. A quoted value may hold commas and backslash escapes.
func parseChallenge(h string) (string, map[string]string) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(h), " ")
	params := map[string]string{}
	for rest = strings.TrimSpace(rest); rest != ""; {
		key, after, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		after = strings.TrimLeft(after, " \t")
		var val string
		if strings.HasPrefix(after, `"`) {
			var b strings.Builder
			i := 1
			for ; i < len(after) && after[i] != '"'; i++ {
				if after[i] == '\\' && i+1 < len(after) {
					i++
				}
				b.WriteByte(after[i])
			}
			val, after = b.String(), after[min(i+1, len(after)):]
		} else {
			end := strings.IndexByte(after, ',')
			if end < 0 {
				end = len(after)
			}
			val, after = strings.TrimSpace(after[:end]), after[end:]
		}
		params[strings.ToLower(strings.TrimSpace(key))] = val
		rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(after), ","))
	}
	return scheme, params
}

// fetchToken asks key's realm for a token, with the client's credentials when it has
// any.
func (c *Client) fetchToken(ctx context.Context, key tokenKey) (types.Secret, error) {
	u, err := url.Parse(key.realm)
	if err != nil {
		return types.Secret{}, err
	}
	q := u.Query()
	if key.service != "" {
		q.Set("service", key.service)
	}
	q.Set("scope", key.scope)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return types.Secret{}, err
	}
	if !c.Credentials.isZero() {
		req.SetBasicAuth(c.Credentials.Username, c.Credentials.Password.Reveal())
	}
	resp, err := c.do(req)
	if err != nil {
		return types.Secret{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return types.Secret{}, statusError(resp)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, registryReplyLimit))
	if err != nil {
		return types.Secret{}, err
	}
	var body struct {
		Token       issuedToken `json:"token"`
		AccessToken issuedToken `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return types.Secret{}, err
	}
	if !body.Token.IsZero() {
		return body.Token.Secret, nil
	}
	if !body.AccessToken.IsZero() {
		return body.AccessToken.Secret, nil
	}
	return types.Secret{}, errors.New("the realm returned no token")
}

// issuedToken decodes a token reply member straight into a [types.Secret], which has no
// decoder of its own so that plaintext cannot become one by accident. This is the one
// place a registry hands magus a bearer, and it is wrapped before anything can print it.
type issuedToken struct{ types.Secret }

func (t *issuedToken) UnmarshalText(b []byte) error {
	t.Secret = types.NewSecret(string(b))
	return nil
}

// setAuth attaches auth, a whole Authorization value, when there is one.
func setAuth(req *http.Request, auth string) {
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
}
