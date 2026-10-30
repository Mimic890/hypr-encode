package convert

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Mimic890/hypr-encode/pkg/probe"
	"github.com/Mimic890/hypr-encode/pkg/tools"
)

// Source is an input prepared for ffmpeg.
type Source struct {
	Path    string          // what ffmpeg reads: the file, or pipe:0 for Decoder
	Decoder []string        // command writing a 16-bit PPM to stdout (camera RAW)
	Grid    *probe.TileGrid // primary HEIF/AVIF tile grid to assemble
	Turn    string          // HEIF/AVIF: ffmpeg filters that rotate and mirror the image for display
}

// size is the displayed size of a tile grid after Turn.
func (s Source) size() (w, h int) {
	if strings.Contains(s.Turn, "transpose") {
		return s.Grid.Height, s.Grid.Width
	}
	return s.Grid.Width, s.Grid.Height
}

// Open prepares path for ffmpeg: camera RAW goes through dcraw_emu, and for
// HEIF/AVIF images the tiles and the rotation are found so BuildArgs can
// stack and turn them.
func Open(ctx context.Context, path string) (Source, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".heic", ".heif", ".hif", ".avif":
		turn, err := heifTurn(ctx, path)
		if err != nil {
			return Source{}, err
		}
		grid, err := probe.PrimaryTileGrid(ctx, path)
		return Source{Path: path, Grid: grid, Turn: turn}, err
	}
	if !probe.IsRaw(path) {
		return Source{Path: path}, nil
	}
	dcraw, err := tools.Path("dcraw_emu")
	if err != nil {
		return Source{}, fmt.Errorf("%s is a camera RAW file: %w", filepath.Base(path), err)
	}
	abs, err := filepath.Abs(path) // never mistaken for an option
	// -w: camera white balance, -6: 16 bits per channel, -Z -: write to stdout.
	return Source{Path: "pipe:0", Decoder: []string{dcraw, "-w", "-6", "-Z", "-", abs}}, err
}

// heifTurn reads how the primary image of a HEIF/AVIF file is displayed and
// returns the ffmpeg filters for it: irot (counterclockwise quarter turns)
// then imir, in the order MIAF requires, or the EXIF Orientation when the file
// has neither, never both. ffmpeg 8.0 applies none of them reliably.
func heifTurn(ctx context.Context, path string) (string, error) {
	exiftool, err := tools.Path("exiftool")
	if err != nil {
		return "", fmt.Errorf("%s: the rotation of HEIF/AVIF images is read with exiftool: %w", filepath.Base(path), err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// Main: the primary image; other items are reported as Doc1, Doc2, ….
	out, err := exec.CommandContext(ctx, exiftool, "-j", "-n", "-Main:Rotation", "-Main:Mirroring", "-Main:Orientation", abs).Output()
	if err != nil {
		return "", fmt.Errorf("exiftool failed on %q: %w", filepath.Base(path), err)
	}
	var tags []struct{ Rotation, Mirroring, Orientation *int }
	if err := json.Unmarshal(out, &tags); err != nil || len(tags) != 1 {
		return "", fmt.Errorf("parse exiftool output for %q: %v", filepath.Base(path), err)
	}
	t := tags[0]
	var f []string
	if t.Rotation != nil {
		f = append(f, map[int]string{1: "transpose=cclock", 2: "hflip,vflip", 3: "transpose=clock"}[*t.Rotation%4])
	}
	if t.Mirroring != nil {
		f = append(f, map[int]string{0: "vflip", 1: "hflip"}[*t.Mirroring]) // axis 0 swaps top and bottom, as in libheif and ffmpeg
	}
	if t.Rotation == nil && t.Mirroring == nil && t.Orientation != nil {
		f = append(f, map[int]string{
			2: "hflip", 3: "hflip,vflip", 4: "vflip", 5: "hflip,transpose=cclock",
			6: "transpose=clock", 7: "hflip,transpose=clock", 8: "transpose=cclock",
		}[*t.Orientation])
	}
	return strings.Trim(strings.Join(f, ","), ","), nil
}

// gridFilter stacks the tiles of g and crops them to the image size.
func gridFilter(g *probe.TileGrid) string {
	var in, layout []string
	for _, t := range g.Tiles {
		in = append(in, fmt.Sprintf("[0:%d]", t.Stream))
		layout = append(layout, fmt.Sprintf("%d_%d", t.X, t.Y))
	}
	f := strings.Join(in, "") + fmt.Sprintf("xstack=inputs=%d:layout=%s", len(in), strings.Join(layout, "|"))
	if len(in) == 1 {
		f = in[0] + "null" // xstack needs two inputs
	}
	return f + fmt.Sprintf(",crop=%d:%d:%d:%d", g.Width, g.Height, g.CropX, g.CropY)
}
