// Package resolve performs identity resolution: the same MCP server often shows
// up in more than one source (the registry and the GitHub crawl, say), and
// indexing both would return duplicate, half-complete results. Dedup groups
// records that refer to the same logical server and merges them into one
// canonical entity, combining each source's strengths (the registry's
// connection details, GitHub's repo/popularity, whichever extracted tools).
package resolve

import (
	"sort"
	"strings"

	"github.com/vanducvt0305/zeus/internal/model"
)

// Dedup merges records that share an identity key, preserving the first-seen
// order of keys.
func Dedup(mcps []model.MCP) []model.MCP {
	var order []string
	groups := make(map[string][]model.MCP)
	for _, m := range mcps {
		k := Key(m)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], m)
	}
	out := make([]model.MCP, 0, len(order))
	for _, k := range order {
		out = append(out, Merge(groups[k]))
	}
	return out
}

// Key is the identity of an MCP. The canonical name is the primary key: every
// source uses the same reverse-DNS convention (e.g. io.github.<owner>/<repo>),
// so names align across sources. We deliberately do NOT key on repository URL —
// a monorepo can host many distinct servers that share one repo, and keying on
// it would wrongly collapse them. Repo URL is used only as a fallback when a
// record has no name.
func Key(m model.MCP) string {
	if n := strings.ToLower(strings.TrimSpace(m.Name)); n != "" {
		return "name:" + n
	}
	if r := normalizeRepo(m.Repository); r != "" {
		return "repo:" + r
	}
	return "id:" + strings.ToLower(strings.TrimSpace(m.ID))
}

// Merge combines a group of records for the same MCP into one. The
// highest-priority source wins scalar fields; list fields are unioned.
func Merge(group []model.MCP) model.MCP {
	if len(group) == 1 {
		return group[0]
	}
	// Order by source authority so the most trustworthy record is the base.
	sorted := append([]model.MCP(nil), group...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return priority(sorted[i].Source) > priority(sorted[j].Source)
	})

	base := sorted[0]
	srcSet := newOrderedSet()
	for _, m := range sorted {
		srcSet.add(m.Source)

		// Fill scalar fields only when the base (higher priority) lacks them.
		base.Title = firstNonEmpty(base.Title, m.Title)
		base.Description = firstNonEmpty(base.Description, m.Description)
		base.Version = firstNonEmpty(base.Version, m.Version)
		base.Repository = firstNonEmpty(base.Repository, m.Repository)
		base.Homepage = firstNonEmpty(base.Homepage, m.Homepage)
		base.UpdatedAt = firstNonEmpty(base.UpdatedAt, m.UpdatedAt)

		base.Transports = unionTransports(base.Transports, m.Transports)
		base.Packages = unionPackages(base.Packages, m.Packages)
		base.Tools = unionTools(base.Tools, m.Tools)
		base.Categories = unionStrings(base.Categories, m.Categories)
	}
	base.Sources = srcSet.slice()
	return base
}

// priority ranks sources by how authoritative they are for connection details.
func priority(src string) int {
	switch src {
	case "registry":
		return 3
	case "github":
		return 2
	case "file":
		return 1
	default:
		return 0
	}
}

// normalizeRepo canonicalizes a repository URL so the same repo compares equal
// across sources, e.g. "https://github.com/Acme/Search.git" -> "github.com/acme/search".
func normalizeRepo(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	for _, p := range []string{"git+https://", "git+http://", "https://", "http://", "ssh://", "git://"} {
		s = strings.TrimPrefix(s, p)
	}
	s = strings.TrimPrefix(s, "git@")
	s = strings.Replace(s, "github.com:", "github.com/", 1) // scp-style remotes
	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	return s
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func unionStrings(a, b []string) []string {
	set := newOrderedSet()
	for _, v := range a {
		set.add(v)
	}
	for _, v := range b {
		set.add(v)
	}
	return set.slice()
}

func unionTransports(a, b []model.Transport) []model.Transport {
	seen := make(map[string]struct{})
	var out []model.Transport
	for _, t := range append(append([]model.Transport(nil), a...), b...) {
		k := strings.ToLower(t.Type + "|" + t.URL)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, t)
	}
	return out
}

func unionPackages(a, b []model.Package) []model.Package {
	seen := make(map[string]struct{})
	var out []model.Package
	for _, p := range append(append([]model.Package(nil), a...), b...) {
		k := strings.ToLower(p.RegistryType + "|" + p.Identifier)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, p)
	}
	return out
}

func unionTools(a, b []model.Tool) []model.Tool {
	seen := make(map[string]struct{})
	var out []model.Tool
	for _, t := range append(append([]model.Tool(nil), a...), b...) {
		if _, ok := seen[t.Name]; ok {
			continue
		}
		seen[t.Name] = struct{}{}
		out = append(out, t)
	}
	return out
}

// orderedSet preserves insertion order while de-duplicating non-empty strings.
type orderedSet struct {
	seen map[string]struct{}
	vals []string
}

func newOrderedSet() *orderedSet { return &orderedSet{seen: map[string]struct{}{}} }

func (o *orderedSet) add(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if _, ok := o.seen[s]; ok {
		return
	}
	o.seen[s] = struct{}{}
	o.vals = append(o.vals, s)
}

func (o *orderedSet) slice() []string { return o.vals }
