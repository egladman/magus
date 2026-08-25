package main

import (
	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/internal/notes"
)

// doctorExplanations reads every declared notes store and reports which files it explains,
// for the unexplained-hotspots check.
//
// Only FILE anchors count. A symbol anchor names code in some file, but resolving which one
// needs the knowledge graph, and a check that silently reported fewer explained files
// whenever the graph was cold would be measuring the index rather than the notes.
//
// ok is false when no store could be read at all, which is what separates "this workspace
// keeps no notes" from "magus could not tell". The check renders the second as unknown
// rather than as a gap.
func doctorExplanations(root string) (doctor.Explanations, bool) {
	stores, err := notesStores(root, "")
	if err != nil {
		return doctor.Explanations{}, false
	}
	var out doctor.Explanations
	read := false
	for _, st := range stores {
		list, lerr := notes.List(st.dir)
		if lerr != nil {
			continue
		}
		read = true
		out.Notes += len(list)
		for _, n := range list {
			for _, a := range n.Anchors {
				if a.Kind == notes.AnchorFile {
					out.Files = append(out.Files, a.Target)
				}
			}
		}
	}
	return out, read
}
