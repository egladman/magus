package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/internal/generate/emit"
)

// modulePath is the import prefix .mockery.yaml keys are written against.
const modulePath = "github.com/egladman/magus"

// mockeryConfig is the slice of .mockery.yaml this generator reads: which interfaces
// were mocked, per package. Everything else in that file (template, output layout) is
// mockery's business and is deliberately not modelled here.
type mockeryConfig struct {
	Packages map[string]struct {
		Interfaces map[string]any `yaml:"interfaces"`
	} `yaml:"packages"`
}

// runMockAssert writes, into every generated mocks package, the compile-time assertions
// that each mock still satisfies the interface it was generated from.
//
// mockery's testify template emits a plain struct with methods and no
// `var _ Iface = (*Mock)(nil)`, so a mock that has gone stale - an interface gained a
// method, nobody re-ran `magus run generate` - still COMPILES. It fails only at a use
// site, and these mocks are published for downstream consumers rather than used by our
// own tests, so the first build to break could well be someone else's.
//
// Generated rather than hand-written, and that is the whole point of this file. The
// assertions used to live in four hand-maintained mocks_satisfy_test.go files, which had
// two problems: adding an interface to .mockery.yaml and forgetting to add a line left
// the new mock unverified, exactly the gap the assertions exist to close; and each file
// had to sit in an EXTERNAL test package, because a test in `package types` cannot import
// types/gen/mocks, which imports types back. Emitting into the mocks package instead has
// neither problem - that package already imports the interface's package, so there is no
// cycle, and the list cannot drift from .mockery.yaml because it is derived from it.
//
// Not a fork of mockery's template, deliberately. Vendoring a copy to add one line would
// make a mockery upgrade silently keep rendering our stale copy, which is the same
// second-source-of-truth failure this repo keeps paying for elsewhere. mockery stays the
// only authority on the mock body; this adds a separate file beside it.
func runMockAssert(args []string) error {
	fs := flag.NewFlagSet("mockassert", flag.ExitOnError)
	configPath := fs.String("config", ".mockery.yaml", "path to .mockery.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	raw, err := os.ReadFile(*configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", *configPath, err)
	}
	var cfg mockeryConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", *configPath, err)
	}
	if len(cfg.Packages) == 0 {
		return fmt.Errorf("%s declares no packages", *configPath)
	}

	for pkgPath, entry := range cfg.Packages {
		if len(entry.Interfaces) == 0 || isInternal(pkgPath) {
			continue
		}
		names := make([]string, 0, len(entry.Interfaces))
		for name := range entry.Interfaces {
			names = append(names, name)
		}
		sort.Strings(names) // byte-stable output; map order is not

		dir, err := mocksDir(pkgPath)
		if err != nil {
			return err
		}
		if err := emit.Go(filepath.Join(dir, "assert.go"), assertFile(pkgPath, names)); err != nil {
			return fmt.Errorf("write %s: %w", dir, err)
		}
	}
	return nil
}

// isInternal reports whether a mocked package is unreachable from outside this module,
// which is exactly when these assertions are unnecessary AND impossible.
//
// Unnecessary: .mockery.yaml mocks an internal package for one reason - OUR tests inject
// the fake - so a mock that stops satisfying its interface fails at that use site on the
// next run. The assertion earns its place only for the published mocks, which nothing
// here calls and whose first failing build would be a consumer's.
//
// Impossible: those same tests live in the package being mocked (internal/dry/eval_test.go
// is `package dry`) and import the mocks. An assertion makes mocks import dry, and the
// in-package test importing mocks then closes the loop - `import cycle not allowed in
// test`. Emitting it would trade a compile-time check nobody needs for a build nobody can
// run.
func isInternal(pkgPath string) bool {
	return strings.Contains(pkgPath, "/internal/") || strings.HasSuffix(pkgPath, "/internal")
}

// mocksDir is where mockery puts the mocks for pkgPath, mirroring the dir template in
// .mockery.yaml ("{{.InterfaceDir}}/gen/mocks"). The root package is the one case the
// template's path arithmetic leaves empty.
func mocksDir(pkgPath string) (string, error) {
	switch {
	case pkgPath == modulePath:
		return filepath.Join("gen", "mocks"), nil
	case strings.HasPrefix(pkgPath, modulePath+"/"):
		return filepath.Join(strings.TrimPrefix(pkgPath, modulePath+"/"), "gen", "mocks"), nil
	default:
		return "", fmt.Errorf("package %q is outside %s; this generator only knows this module", pkgPath, modulePath)
	}
}

// assertFile renders the assertions for one package.
//
// The import is aliased to a fixed name rather than spelled with the package's real one,
// so this needs no way to map an import path to a package name - which is not derivable
// from the path (project/impact is package impact, and the module root is package magus).
func assertFile(pkgPath string, interfaces []string) []byte {
	var b bytes.Buffer
	b.WriteString("// Code generated by magus-utils mockassert; DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// Each line fails to compile when a mock stops satisfying the interface it was\n")
	b.WriteString("// generated from, which mockery's own output does not check. Regenerate with\n")
	b.WriteString("// `magus run generate` after changing .mockery.yaml or an interface.\n\n")
	b.WriteString("package mocks\n\n")
	fmt.Fprintf(&b, "import iface %q\n\n", pkgPath)
	b.WriteString("var (\n")
	for _, name := range interfaces {
		fmt.Fprintf(&b, "\t_ iface.%s = (*Mock%s)(nil)\n", name, name)
	}
	b.WriteString(")\n")
	return b.Bytes()
}
