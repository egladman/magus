package knowledge

import (
	"sort"
	"strings"

	"github.com/egladman/magus/types"
)

// packagesShardName is the singleton shard holding third-party package nodes and the
// depends_on edges from the projects that declare them.
//
// A singleton rather than one shard per project, which is the opposite of the choice
// @symbols made, and for the opposite reason. A package node is SHARED: two Go modules
// in one workspace requiring the same version produce one node, and putting it in a
// per-project shard would mean the node's existence depended on which shards happened
// to be loaded. Symbols had no such sharing and had size (they dwarf the domain graph)
// forcing the split. Packages are small - a few hundred nodes for a workspace this
// size - so the coarser invalidation this costs (any manifest change rebuilds the
// shard) is cheaper than the alternative it avoids.
//
// It is DETERMINISTIC - the same manifests always yield the same shard - so it is
// remote-shareable like the other extracted shards, not isolated the way @runtime and
// @coverage are.
const packagesShardName = "@packages"

// attrPackageManager, AttrPackageVersion, attrPackageIndirect and attrPackageReplaced
// are the attrs a package node carries. Named rather than spelled inline because a
// mistyped literal would silently match nothing on query instead of failing.
const (
	attrPackageManager  = "manager"
	AttrPackageVersion  = "version"
	attrPackageIndirect = "indirect"
	attrPackageReplaced = "replaced"
)

// packageID keys a package node by MANAGER and name, never name alone: the npm package
// `foo` and the Go module `foo` are different things that would otherwise share a node.
// internal/symbols/scip.go's parseMoniker folds the manager into its key for the same
// reason, so the two surfaces agree on what counts as one dependency.
//
// The VERSION is deliberately excluded, which is the same call parseMoniker makes. A
// node is the dependency, not one release of it, so a bump edits an attr instead of
// renaming the node and orphaning everything pointing at it. It also means two projects
// pinned to DIFFERENT versions of one package share a node whose version attr can only
// report one of them - see assemblePackages, which keeps the lowest and flags the split
// rather than letting the last writer win silently.
func packageID(manager, name string) string {
	return types.KindPackage + ":" + manager + " " + name
}

// attrPackageVersionConflict marks a package two projects in one workspace pin to
// different versions. The value is the full sorted set, comma-separated, because the
// interesting thing about a conflict is every version involved - the version attr can
// only hold one, and picking it is not this layer's judgment to make.
//
// Write-only: nothing reads it back yet (no query filter, no console column). It rides
// on the node for when a consumer wants it.
const attrPackageVersionConflict = "version_conflict"

// assemblePackages builds the singleton package shard from each project's declared
// dependencies: one node per (manager, name) plus a depends_on edge from every project
// declaring it.
//
// Provenance names the manifest the record came from, so `magus explain package:...`
// answers "why does the graph think we depend on this" with a file rather than an
// assertion.
//
// Empty input yields an empty shard, which the caller drops.
func assemblePackages(packages map[string][]types.KnowledgePackage) Shard {
	s := Shard{Name: packagesShardName}
	if len(packages) == 0 {
		return s
	}

	// Sorted so the shard is deterministic despite the map input - the property the
	// whole shard's remote-shareability rests on.
	projects := make([]string, 0, len(packages))
	for p := range packages {
		projects = append(projects, p)
	}
	sort.Strings(projects)

	type node struct {
		index     int
		versions  map[string]bool
		anyDirect bool
		replaced  bool
	}
	seen := map[string]*node{}

	for _, project := range projects {
		for _, pkg := range packages[project] {
			if pkg.Manager == "" || pkg.Name == "" {
				continue
			}
			id := packageID(pkg.Manager, pkg.Name)
			n, ok := seen[id]
			if !ok {
				s.Nodes = append(s.Nodes, types.KnowledgeNode{
					ID:    id,
					Kind:  types.KindPackage,
					Label: pkg.Name,
					Attrs: map[string]string{attrPackageManager: pkg.Manager},
				})
				n = &node{index: len(s.Nodes) - 1, versions: map[string]bool{}}
				seen[id] = n
			}
			n.versions[pkg.Version] = true
			// A package DIRECT to any project is direct. The attr answers "is this ours
			// to bump", and one project choosing it deliberately settles that even if
			// three others merely inherited it - so this folds with OR over direct, not
			// over indirect.
			n.anyDirect = n.anyDirect || !pkg.Indirect
			n.replaced = n.replaced || pkg.Replaced
			s.Edges = append(s.Edges, extractedEdge(projectID(project), id, types.RelationDependsOn, project))
		}
	}

	for _, n := range seen {
		attrs := s.Nodes[n.index].Attrs
		versions := make([]string, 0, len(n.versions))
		for v := range n.versions {
			versions = append(versions, v)
		}
		sort.Strings(versions)
		attrs[AttrPackageVersion] = versions[0]
		if len(versions) > 1 {
			attrs[attrPackageVersionConflict] = strings.Join(versions, ",")
		}
		// Only the interesting half of each flag is recorded. A direct, unreplaced
		// dependency is the overwhelming default, and writing `indirect=false` on every
		// node would cost more to read than it explains.
		if !n.anyDirect {
			attrs[attrPackageIndirect] = "true"
		}
		if n.replaced {
			attrs[attrPackageReplaced] = "true"
		}
	}
	return s
}
