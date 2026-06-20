package source

import "github.com/vanducvt0305/zeus/internal/model"

// serverJSON is the official server.json shape (the subset of fields we use).
// It is shared by every source that can produce one: the registry returns it
// directly, and the GitHub crawler fetches it from a repository root.
type serverJSON struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Version     string `json:"version"`
	WebsiteURL  string `json:"websiteUrl"`
	Repository  struct {
		URL string `json:"url"`
	} `json:"repository"`
	Remotes []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"remotes"`
	Packages []struct {
		RegistryType string `json:"registryType"`
		Identifier   string `json:"identifier"`
		Version      string `json:"version"`
		Transport    struct {
			Type string `json:"type"`
		} `json:"transport"`
	} `json:"packages"`
}

// toMCP maps a server.json document into the normalized model, tagging it with
// the given source. The caller fills source-specific fields (UpdatedAt, etc.).
func (s serverJSON) toMCP(src string) model.MCP {
	m := model.MCP{
		ID:          s.Name,
		Name:        s.Name,
		Title:       s.Title,
		Description: s.Description,
		Version:     s.Version,
		Repository:  s.Repository.URL,
		Homepage:    s.WebsiteURL,
		Source:      src,
	}
	for _, rm := range s.Remotes {
		m.Transports = append(m.Transports, model.Transport{Type: rm.Type, URL: rm.URL})
	}
	for _, p := range s.Packages {
		m.Packages = append(m.Packages, model.Package{
			RegistryType: p.RegistryType,
			Identifier:   p.Identifier,
			Version:      p.Version,
			Transport:    p.Transport.Type,
		})
	}
	return m
}
