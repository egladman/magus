// Command magus-configdocs generates the magus.yaml configuration reference from
// the code-generated schema inventory. Run it manually to refresh the committed
// page:
//
//	go run ./cmd/magus-configdocs -out ./docs/config.md
//
// docs/config.md is committed; TestConfigDocsUpToDate keeps it in lockstep with
// schema.Fields (which is itself generated from internal/config/config.go). The
// site renders the committed file at /config/.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/egladman/magus/internal/docs"
	"github.com/egladman/magus/schema"
)

func main() {
	out := flag.String("out", "docs/config.md", "output path for the config reference")
	flag.Parse()

	if err := os.WriteFile(*out, []byte(render()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "magus-configdocs:", err)
		os.Exit(1)
	}
	fmt.Printf("magus-configdocs: wrote %d config fields to %s\n", len(schema.Fields), *out)
}

// render builds the full Markdown page: frontmatter, intro, then one table per
// top-level config section (cache, output, ...), sorted for byte-stable output.
func render() string {
	var b strings.Builder
	docs.WriteFrontmatter(&b, docs.Frontmatter{
		Title:       "magus.yaml configuration",
		Description: "Every magus.yaml config key with its MAGUS_* environment variable, CLI flag, and type. Generated from the config schema.",
		Tags:        []string{"config", "magus.yaml", "configuration", "environment variables", "flags", "reference"},
	})

	fmt.Fprintf(&b, "# Configuration\n\n")
	fmt.Fprintf(&b, "magus resolves configuration from three layers, highest precedence first: a CLI flag, a `MAGUS_*` environment variable, then the `magus.yaml` file at the workspace root. This page is the complete inventory of config keys, each with its `magus.yaml` path, environment variable, CLI flag, and value type.\n\n")

	// Group fields by their top-level yaml section (the segment before the first
	// "."), so the reference reads section by section.
	bySection := map[string][]schema.Field{}
	for _, f := range schema.Fields {
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
		fmt.Fprintf(&b, "| Config key | Environment variable | Flag | Type |\n")
		fmt.Fprintf(&b, "|------------|----------------------|------|------|\n")
		for _, f := range fields {
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n",
				f.YamlPath, f.EnvVar, flagCell(f.Flag), kindLabel(f.Kind))
		}
		fmt.Fprintf(&b, "\n")
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
func flagCell(f schema.FlagNames) string {
	if f.Long == "" {
		return "_(env only)_"
	}
	if f.Short != "" {
		return fmt.Sprintf("`-%s`, `--%s`", f.Short, f.Long)
	}
	return fmt.Sprintf("`--%s`", f.Long)
}

// kindLabel maps a schema.Kind to a reader-facing type name.
func kindLabel(k schema.Kind) string {
	switch k {
	case schema.KindString:
		return "string"
	case schema.KindInt:
		return "int"
	case schema.KindBool:
		return "bool"
	case schema.KindFloat64:
		return "float"
	case schema.KindBoolPtr:
		return "bool _(env only)_"
	case schema.KindDuration:
		return "duration"
	case schema.KindStringSlice:
		return "list _(comma-separated, env only)_"
	default:
		return "string"
	}
}
