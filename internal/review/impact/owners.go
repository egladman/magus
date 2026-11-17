package impact

import (
	"fmt"

	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/impact"
)

// projectOwner is one project in reach and who has been changing it.
type projectOwner struct {
	Project      string `json:"project"                yaml:"project"`
	Primary      string `json:"primary"                yaml:"primary"`
	PrimaryShare int    `json:"primary_share"          yaml:"primary_share"` // percent
	Authors      int    `json:"authors"                yaml:"authors"`
	BusFactor1   bool   `json:"bus_factor_1,omitempty" yaml:"bus_factor_1,omitempty"`
}

// listOwners joins the ownership lens onto the reach, in reach order.
//
// Projects the lens has nothing to say about are DROPPED rather than listed with an empty
// author: a project with no commits in the window has no owner to name, and a blank name
// beside a path reads as a lookup that failed.
func listOwners(own types.Ownership, affected []impact.Project) []projectOwner {
	byPath := make(map[string]types.OwnershipEntry, len(own.Projects))
	for _, e := range own.Projects {
		byPath[e.Path] = e
	}
	var out []projectOwner
	for _, p := range affected {
		e, ok := byPath[p.Path]
		if !ok || e.Primary == "" {
			continue
		}
		out = append(out, projectOwner{
			Project:      p.Path,
			Primary:      e.Primary,
			PrimaryShare: e.PrimaryShare,
			Authors:      e.Authors,
			BusFactor1:   e.BusFactor1,
		})
	}
	return out
}

func ownershipLines(owners []projectOwner) []string {
	if len(owners) == 0 {
		return []string{"OWNERSHIP: no commit history in the window, so no owner is named"}
	}
	out := []string{"OWNERSHIP: who has been changing the projects in reach"}
	for _, o := range capList(owners) {
		line := fmt.Sprintf("      %s mostly %s (%d%%), %d author%s",
			o.Project, o.Primary, o.PrimaryShare, o.Authors, plural.Word(o.Authors, "", "s"))
		if o.BusFactor1 {
			// The bus factor is the whole point of the lens, so it is stated rather than left
			// to be inferred from "1 author".
			line += " - BUS FACTOR 1"
		}
		out = append(out, line)
	}
	return append(out, moreLine(len(owners))...)
}
