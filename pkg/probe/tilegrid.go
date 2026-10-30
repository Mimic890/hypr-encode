package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Mimic890/hypr-encode/pkg/tools"
)

// TileGrid is a HEIF or AVIF image stored as tiles that must be stacked and
// cropped, like every photo an iPhone takes.
type TileGrid struct {
	Width, Height int // image size after cropping
	CropX, CropY  int
	Tiles         []Tile
}

// Tile is one tile stream and its position in the stacked image.
type Tile struct{ Stream, X, Y int }

// PrimaryTileGrid returns the tile grid ffprobe marks as the default image of
// path, or nil when the primary image is not tiled.
func PrimaryTileGrid(ctx context.Context, path string) (*TileGrid, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-print_format", "json", "-show_stream_groups", "-i", abs).Output()
	var exitErr *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return nil, &tools.MissingError{Name: "ffprobe"}
	case errors.As(err, &exitErr) && strings.Contains(string(exitErr.Stderr), "show_stream_groups"):
		return nil, fmt.Errorf("%s: tiled HEIF/AVIF images need ffprobe 7.1 or newer", filepath.Base(path))
	case errors.As(err, &exitErr):
		return nil, fmt.Errorf("ffprobe failed on %q: %s", filepath.Base(path), strings.TrimSpace(string(exitErr.Stderr)))
	case err != nil:
		return nil, err
	}

	var doc struct {
		StreamGroups []struct {
			Type        string `json:"type"`
			Disposition struct {
				Default int `json:"default"`
			} `json:"disposition"`
			Components []struct {
				Width         int `json:"width"`
				Height        int `json:"height"`
				CropX         int `json:"horizontal_offset"`
				CropY         int `json:"vertical_offset"`
				Subcomponents []struct {
					Stream int `json:"stream_index"`
					X      int `json:"tile_horizontal_offset"`
					Y      int `json:"tile_vertical_offset"`
				} `json:"subcomponents"`
			} `json:"components"`
		} `json:"stream_groups"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("parse ffprobe stream groups: %w", err)
	}
	for _, g := range doc.StreamGroups {
		if g.Type != "Tile Grid" || g.Disposition.Default == 0 || len(g.Components) == 0 {
			continue
		}
		c := g.Components[0]
		grid := &TileGrid{Width: c.Width, Height: c.Height, CropX: c.CropX, CropY: c.CropY}
		for _, t := range c.Subcomponents {
			grid.Tiles = append(grid.Tiles, Tile{t.Stream, t.X, t.Y})
		}
		if len(grid.Tiles) == 0 || grid.Width <= 0 || grid.Height <= 0 {
			return nil, fmt.Errorf("%s: ffprobe reports an empty tile grid", filepath.Base(path))
		}
		return grid, nil
	}
	return nil, nil
}
