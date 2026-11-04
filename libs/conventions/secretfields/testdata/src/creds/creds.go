package creds

// Secret stands in for a self-redacting secret type: a named struct passes.
type Secret struct{ s string }

type Login struct {
	Username      string
	Password      string            // want `Password holds a credential as string`
	APIKey        []byte            // want `APIKey holds a credential as \[\]byte`
	signingKey    string            // want `signingKey holds a credential as string`
	tokens        map[string]string // want `tokens holds a credential as map\[string\]string`
	Bearer        *string           // want `Bearer holds a credential as \*string`
	hmac          []byte            // want `hmac holds a credential as \[\]byte`
	refresh_token string            // want `refresh_token holds a credential as string`

	PasswordRef string // names a reference, not a credential
	TokenCount  int
	Token       Secret
	tokenKind   kind
	Tokenizer   string
}

// kind is an enum over string: a named type is not plaintext.
type kind string

func mint() (secret string, id string, err error) { // want `secret holds a credential as string`
	return "", "", nil
}

func load() (Secret, error) { return Secret{}, nil }

func anonymous() {
	var body struct {
		AccessToken string `json:"access_token"` // want `AccessToken holds a credential as string`
	}
	_ = body
}
