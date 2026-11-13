// Command magus-configdocs generates the magus.yaml configuration reference from
// the code-generated schema inventory. Run it manually to refresh the committed
// page:
//
//	go run ./cmd/magus-configdocs -out ./docs/config.md
//
// docs/config.md is committed; docs' generate target rewrites it and its drift gate
// keeps it in lockstep with schema.Fields (which is itself generated from
// internal/config/config.go). The site renders the committed file at /config/.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/egladman/magus/internal/config/gen/settings"
	"github.com/egladman/magus/internal/docs"
	"github.com/egladman/magus/internal/render/md"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/configuration"
)

func main() {
	out := flag.String("out", "docs/config.md", "output path for the config reference")
	flag.Parse()

	if err := os.WriteFile(*out, []byte(render()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "magus-configdocs:", err)
		os.Exit(1)
	}
	fmt.Printf("magus-configdocs: wrote %d config fields to %s\n", len(settings.Settings), *out)
}

// render builds the full Markdown page: frontmatter, intro, then one table per
// top-level config section (cache, output, ...), sorted for byte-stable output.
func render() string {
	var b strings.Builder
	docs.WriteFrontmatter(&b, docs.Frontmatter{
		Title:         "magus.yaml configuration",
		Description:   "Every magus.yaml config key with its MAGUS_* environment variable, CLI flag, and type. Generated from the config schema.",
		GeneratedFrom: "internal/config/config.go",
		Tags:          []string{"config", "magus.yaml", "configuration", "environment variables", "flags", "reference"},
	})

	fmt.Fprintf(&b, "# Configuration\n\n")
	fmt.Fprintf(&b, "magus resolves configuration from these layers, highest precedence first: a CLI flag, a `MAGUS_*` environment variable, a `magus.yaml` in the working directory below the workspace root, the `magus.yaml` at the workspace root, then the user config at `$XDG_CONFIG_HOME/magus/magus.yaml`. This page is the inventory of config keys a flag or an environment variable can set, each with its `magus.yaml` path, environment variable, CLI flag, and value type. Keys that only a file sets, such as `knowledge.workspaces` in the user config, are documented where they are used.\n\n")

	// Group fields by their top-level yaml section (the segment before the first
	// "."), so the reference reads section by section.
	bySection := map[string][]configuration.Setting{}
	for _, f := range settings.Settings {
		bySection[topSection(f.YamlPath)] = append(bySection[topSection(f.YamlPath)], f)
	}
	sections := make([]string, 0, len(bySection))
	for s := range bySection {
		sections = append(sections, s)
	}
	sort.Strings(sections)

	for _, s := range sections {
		fields := bySection[s]
		sort.Slice(fields, func(i, j int) bool { return fields[i].YamlPath < fields[j].YamlPath })
		fmt.Fprintf(&b, "## %s\n\n", s)
		rows := make([][]string, 0, len(fields))
		for _, f := range fields {
			rows = append(rows, []string{md.Code(f.YamlPath), md.Code(f.EnvVar), flagCell(f.Flag), kindLabel(f.Kind)})
		}
		b.WriteString(md.Table([]string{"Config key", "Environment variable", "Flag", "Type"}, nil, rows))
	}
	return b.String()
}

// topSection returns the config section a yaml path belongs to: the first dotted
// segment ("cache.remote.dir" -> "cache"), or "general" for a bare top-level key
// so scalar top-level settings share one table instead of each getting a one-row
// section.
func topSection(yamlPath string) string {
	if i := strings.IndexByte(yamlPath, '.'); i >= 0 {
		return yamlPath[:i]
	}
	return "general"
}

// flagCell renders the CLI flag for a table cell: the long form (plus short when
// present), or an italic "(env only)" for fields with no flag.
func flagCell(f configuration.FlagNames) string {
	if f.Long == "" {
		return "_(env only)_"
	}
	if f.Short != "" {
		return fmt.Sprintf("`-%s`, `--%s`", f.Short, f.Long)
	}
	return fmt.Sprintf("`--%s`", f.Long)
}

// kindLabel maps a setting's command.Kind to a reader-facing type name.
func kindLabel(k command.Kind) string {
	switch k {
	case command.String:
		return "string"
	case command.Int:
		return "int"
	case command.Bool:
		return "bool"
	case command.Float:
		return "float"
	case command.OptionalBool:
		return "bool _(env only)_"
	case command.Duration:
		return "duration"
	case command.StringList:
		return "list _(comma-separated, env only)_"
	}
	panic(fmt.Sprintf("magus-configdocs: no label for setting kind %q", k))
}
