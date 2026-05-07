package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// listProject is the structured view of a project emitted under -o
// json|yaml. Empty slices/strings are omitted to keep the payload tight.
type listProject struct {
	Path      string   `json:"path"                yaml:"path"`
	Dir       string   `json:"dir"                 yaml:"dir"`
	Spell     string   `json:"spell,omitempty"     yaml:"spell,omitempty"`
	Sources   []string `json:"sources,omitempty"    yaml:"sources,omitempty"`
	Outputs   []string `json:"outputs,omitempty"    yaml:"outputs,omitempty"`
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	Exclusive bool     `json:"exclusive,omitempty"  yaml:"exclusive,omitempty"`
}

type listOutput struct {
	Workspace string        `json:"workspace" yaml:"workspace"`
	Count     int           `json:"count"     yaml:"count"`
	Projects  []listProject `json:"projects"  yaml:"projects"`
}

func ls(root string, args []string) error {
	_, err := cmdParse("ls", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ls [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print every discovered project with its spell, sources, outputs, and depends_on.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	spec, err := outputSpecOrDefault()
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(context.Background(), root)
	if err != nil {
		return err
	}

	projects := ws.All()
	out := listOutput{
		Workspace: ws.Root(),
		Count:     len(projects),
		Projects:  make([]listProject, 0, len(projects)),
	}
	for _, p := range projects {
		out.Projects = append(out.Projects, listProject{
			Path:      p.Path,
			Dir:       p.Dir,
			Spell:     p.Spell,
			Sources:   p.Sources,
			Outputs:   p.Outputs,
			DependsOn: p.DependsOn,
			Exclusive: p.Exclusive,
		})
	}

	switch spec.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(spec, out)
	case outputName:
		for _, p := range out.Projects {
			fmt.Println(p.Path)
		}
		return nil
	}

	// text and wide share the human-readable layout.
	fmt.Printf("workspace: %s (%d projects)\n\n", out.Workspace, out.Count)
	for _, p := range out.Projects {
		fmt.Printf("project: %s\n", p.Path)
		fmt.Printf("  dir:  %s\n", p.Dir)
		if p.Spell != "" {
			fmt.Printf("  spell: %s\n", p.Spell)
		}
		if len(p.Sources) > 0 {
			fmt.Printf("  sources: %v\n", p.Sources)
		}
		if len(p.Outputs) > 0 {
			fmt.Printf("  outputs: %v\n", p.Outputs)
		}
		if len(p.DependsOn) > 0 {
			fmt.Printf("  depends_on: %v\n", p.DependsOn)
		}
		if p.Exclusive {
			fmt.Printf("  exclusive: true\n")
		}
		fmt.Println()
	}
	return nil
}
