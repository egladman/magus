package guard

import "context"

type envRefusalKey struct{}

// WithEnvRefusal hands RunHook the MGS1046 error startup found, for it to answer as the
// verdict instead of judging the input.
func WithEnvRefusal(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, envRefusalKey{}, err)
}

type staleConfigKey struct{}

// WithStaleConfig hands RunHook a magus.yaml failure this build
// predates. The guard judges it as a stale binary's load failure.
func WithStaleConfig(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, staleConfigKey{}, err)
}
