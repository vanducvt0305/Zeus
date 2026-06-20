package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/vanducvt0305/zeus/internal/model"
)

// File is a Source backed by a local JSON file containing an array of MCP
// records. It serves two purposes: declaring MCPs by hand, and providing a
// fixed, reproducible catalog for the evaluation harness (so metrics don't
// drift with the live registry).
type File struct {
	path string
}

// NewFile returns a File source reading from path.
func NewFile(path string) *File { return &File{path: path} }

func (f *File) Name() string { return "file" }

func (f *File) Fetch(_ context.Context, limit int) ([]model.MCP, error) {
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("reading source file: %w", err)
	}
	var mcps []model.MCP
	if err := json.Unmarshal(raw, &mcps); err != nil {
		return nil, fmt.Errorf("decoding source file %s: %w", f.path, err)
	}
	for i := range mcps {
		if mcps[i].Source == "" {
			mcps[i].Source = "file"
		}
		if mcps[i].ID == "" {
			mcps[i].ID = mcps[i].Name
		}
	}
	if limit > 0 && len(mcps) > limit {
		mcps = mcps[:limit]
	}
	return mcps, nil
}
