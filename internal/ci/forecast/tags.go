package forecast

import (
	"path"
	"slices"
	"strings"
)

// Tags derives workload-bucket tags from changed files: "transitive" when none changed directly,
// "direct" + "direct.<subdir>" tags otherwise. Tags are sorted for determinism.
func Tags(projectPath string, filesInProject []string) []string {
	if len(filesInProject) == 0 {
		return []string{"transitive"}
	}

	subdirs := map[string]struct{}{}
	for _, f := range filesInProject {
		// f is repo-relative; strip the project prefix to get the
		// path relative to the project root.
		rel := strings.TrimPrefix(f, projectPath)
		rel = strings.TrimPrefix(rel, "/")
		sub := path.Dir(rel)
		// path.Dir returns "." for a file directly in the project root.
		if sub != "" && sub != "." {
			// Take only the first component (top-level subdir).
			parts := strings.SplitN(sub, "/", 2)
			subdirs[parts[0]] = struct{}{}
		}
	}

	tags := []string{"direct"}
	for sub := range subdirs {
		tags = append(tags, "direct."+sub)
	}
	slices.Sort(tags)
	return tags
}
