package image

// Secret is a credential value at the Buzz/Go boundary. Callers build it from
// magus\secret.read; the registry client wraps it as secret.Value before Reveal.
type Secret struct {
	Value string
}

// RegistryAuth authenticates pull and publish. Password must be a Secret, not a bare string.
type RegistryAuth struct {
	Username string
	Password Secret
}

// NewSecret wraps a resolved credential string.
func NewSecret(value string) Secret { return Secret{Value: value} }
