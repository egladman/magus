package vcs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
)

var builtin = []types.VCSDriver{gitVCS{}, hgVCS{}, jjVCS{}}

// Resolve picks the active VCS for root: disabled → explicit → auto (claim dir) → default (git).
// Base ref: runtimeBase → opts.BaseRef → MAGUS_VCS_BASE_REF → per-VCS env → built-in default.
func Resolve(_ context.Context, root, runtimeBase string, opts types.VCSOptions) (types.VCSResolution, error) {
	if opts.Enabled != nil && !*opts.Enabled {
		return types.VCSResolution{Source: types.VCSSourceDisabled}, nil
	}
	if opts.Enabled == nil && os.Getenv("MAGUS_VCS_ENABLED") == "false" {
		return types.VCSResolution{Source: types.VCSSourceDisabled}, nil
	}

	name := opts.Name
	if name == "" {
		name = os.Getenv("MAGUS_VCS_NAME")
	}

	var (
		v      types.VCSDriver
		source types.VCSSource
	)

	if name != "" {
		impl, ok := lookupImpl(name)
		if !ok {
			return types.VCSResolution{}, fmt.Errorf("%w: %q", types.ErrVCSUnknown, name)
		}
		v = impl
		source = types.VCSSourceExplicit
	} else {
		for _, e := range builtin {
			if claimsExist(root, e.Claims()) {
				v = e
				name = e.Name()
				source = types.VCSSourceAuto
				break
			}
		}
		if v == nil {
			v = builtin[0]
			name = v.Name()
			source = types.VCSSourceDefault
		}
	}

	globalBaseRef := opts.BaseRef
	if globalBaseRef == "" {
		globalBaseRef = os.Getenv("MAGUS_VCS_BASE_REF")
	}
	perVCSBaseRef := os.Getenv(perVCSEnv(name, "BASE_REF"))

	base := chooseBase(runtimeBase, globalBaseRef, perVCSBaseRef, v.Base())

	return types.VCSResolution{Name: name, Source: source, Base: base, VCS: v}, nil
}

func lookupImpl(name string) (types.VCSDriver, bool) {
	for _, v := range builtin {
		if v.Name() == name {
			return v, true
		}
	}
	return nil, false
}

// InstallableVCSes returns the names of built-in VCS drivers that support
// merge-driver installation.
func InstallableVCSes() []string {
	var names []string
	for _, v := range builtin {
		if _, ok := v.(types.MergeDriverInstaller); ok {
			names = append(names, v.Name())
		}
	}
	return names
}

// Installer returns the merge-driver installer for the named VCS, or (nil, false).
func Installer(name string) (types.MergeDriverInstaller, bool) {
	v, ok := lookupImpl(name)
	if !ok {
		return nil, false
	}
	inst, ok := v.(types.MergeDriverInstaller)
	return inst, ok
}

func chooseBase(runtime, global, perVCS, def string) string {
	if runtime != "" {
		return runtime
	}
	if global != "" {
		return global
	}
	if perVCS != "" {
		return perVCS
	}
	if def != "" {
		return def
	}
	return "origin/main"
}

func perVCSEnv(name, suffix string) string {
	return "MAGUS_VCS_" + strings.ToUpper(name) + "_" + suffix
}

// checkBaseRef rejects a base ref that begins with "-", which a VCS would
// otherwise read as a flag (argument injection) when passed as a standalone token.
func checkBaseRef(base string) error {
	if strings.HasPrefix(base, "-") {
		return fmt.Errorf("vcs: refusing base ref %q that looks like a flag", base)
	}
	return nil
}

func claimsExist(root string, claims []string) bool {
	for _, c := range claims {
		if _, err := os.Stat(filepath.Join(root, c)); err == nil {
			return true
		}
	}
	return false
}
