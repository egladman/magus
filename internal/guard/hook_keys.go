package guard

import "context"

type envRefusalKey struct{}

// WithEnvRefusal hands RunHook the MGS1046 error startup found, for it to answer as a
// call nothing judged.
func WithEnvRefusal(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, envRefusalKey{}, err)
}

type staleConfigKey struct{}

// WithStaleConfig hands RunHook a magus.yaml failure this build
// predates, for it to answer as a call nothing judged.
func WithStaleConfig(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, staleConfigKey{}, err)
}
