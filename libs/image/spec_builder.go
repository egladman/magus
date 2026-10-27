package image

import (
	"context"
	"fmt"
	"maps"
)

// NewSpec starts a Spec for one platform. Chain With* methods, then
// AssembleLayout. This is not a Dockerfile: there is no FROM, COPY, or RUN.
func NewSpec(platform Platform) *Spec {
	return &Spec{Platform: platform}
}

// AssembleLayout builds the OCI Image Layout described by s into dest.
func (s *Spec) AssembleLayout(ctx context.Context, dest string) (Layout, error) {
	if s == nil {
		return Layout{}, fmt.Errorf("image.assemble: nil spec")
	}
	return AssembleLayout(ctx, *s, dest)
}

// WithBase sets the base Image Layout path (digest-pinned pull or prior assemble).
// Empty clears to scratch. Not Dockerfile FROM: no tags, no build stages.
func (s *Spec) WithBase(layoutPath string) *Spec {
	s.Base = layoutPath
	return s
}

// WithFile appends one host→image file placement (the Layer content unit).
func (s *Spec) WithFile(f File) *Spec {
	s.Files = append(s.Files, f)
	return s
}

// WithDir appends one directory expansion into Layer entries.
func (s *Spec) WithDir(d DirPlacement) *Spec {
	s.Dirs = append(s.Dirs, d)
	return s
}

// WithSymlink appends one symlink placement.
func (s *Spec) WithSymlink(l Symlink) *Spec {
	s.Symlinks = append(s.Symlinks, l)
	return s
}

// WithEnv merges key into the image config Env (over base by key).
func (s *Spec) WithEnv(key, value string) *Spec {
	if s.Env == nil {
		s.Env = map[string]string{}
	}
	s.Env[key] = value
	return s
}

// WithEnvMap merges every entry into config Env.
func (s *Spec) WithEnvMap(env map[string]string) *Spec {
	if len(env) == 0 {
		return s
	}
	if s.Env == nil {
		s.Env = maps.Clone(env)
		return s
	}
	maps.Copy(s.Env, env)
	return s
}

// WithUnsetEnv removes keys inherited from the base config Env.
func (s *Spec) WithUnsetEnv(keys ...string) *Spec {
	s.UnsetEnv = append(s.UnsetEnv, keys...)
	return s
}

// WithUser sets the image config User.
func (s *Spec) WithUser(user string) *Spec {
	s.User = user
	return s
}

// WithWorkingDir sets the image config WorkingDir.
func (s *Spec) WithWorkingDir(dir string) *Spec {
	s.WorkingDir = dir
	return s
}

// WithEntrypoint sets the image config Entrypoint (OCI Config, not a shell form).
func (s *Spec) WithEntrypoint(args ...string) *Spec {
	s.Entrypoint = append([]string(nil), args...)
	return s
}

// WithResetEntrypoint clears the base Entrypoint before WithEntrypoint applies.
func (s *Spec) WithResetEntrypoint() *Spec {
	s.ResetEntrypoint = true
	return s
}

// WithCommand sets the image config process argv (OCI Config.Cmd).
func (s *Spec) WithCommand(args ...string) *Spec {
	s.Command = append([]string(nil), args...)
	return s
}

// WithResetCommand clears the base command before WithCommand applies.
func (s *Spec) WithResetCommand() *Spec {
	s.ResetCommand = true
	return s
}

// WithLabel merges a config label.
func (s *Spec) WithLabel(key, value string) *Spec {
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	s.Labels[key] = value
	return s
}

// WithAnnotation merges a manifest annotation.
func (s *Spec) WithAnnotation(key, value string) *Spec {
	if s.Annotations == nil {
		s.Annotations = map[string]string{}
	}
	s.Annotations[key] = value
	return s
}

// WithCreated sets the image config created timestamp (RFC 3339).
func (s *Spec) WithCreated(rfc3339 string) *Spec {
	s.Created = rfc3339
	return s
}
