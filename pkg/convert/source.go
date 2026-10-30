package convert

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Mimic890/hypr-encode/pkg/probe"
	"github.com/Mimic890/hypr-encode/pkg/tools"
)

// rawExts are camera RAW formats, decoded with LibRaw's dcraw_emu because
// ffmpeg reads them only as thumbnails or not at all. DNG is included: ffmpeg
// decodes some DNGs, but without white balance or tone.
var rawExts = []string{
	".3fr", ".arw", ".cr2", ".cr3", ".crw", ".dcr", ".dng", ".erf", ".iiq", ".kdc", ".mef", ".mos",
	".mrw", ".nef", ".nrw", ".orf", ".pef", ".raf", ".raw", ".rw2", ".rwl", ".sr2", ".srf", ".srw", ".x3f",
}

// IsRaw reports whether path has a camera RAW extension.
func IsRaw(path string) bool { return slices.Contains(rawExts, strings.ToLower(filepath.Ext(path))) }

// Source is an input prepared for ffmpeg.
type Source struct {
	Path    string          // what ffmpeg reads: the file, or pipe:0 for Decoder
	Decoder []string        // command writing a 16-bit PPM to stdout (camera RAW)
	Grid    *probe.TileGrid // primary HEIF/AVIF tile grid to assemble
}

// Open prepares path for ffmpeg: camera RAW goes through dcraw_emu, and the
// tiles of a HEIF/AVIF image are found so BuildArgs can stack them.
func Open(ctx context.Context, path string) (Source, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".heic", ".heif", ".hif", ".avif":
		grid, err := probe.PrimaryTileGrid(ctx, path)
		return Source{Path: path, Grid: grid}, err
	}
	if !IsRaw(path) {
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

// gridFilter stacks the tiles of g, crops them to the image size and applies
// its rotation, the way ffmpeg rotates other inputs.
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
	f += fmt.Sprintf(",crop=%d:%d:%d:%d", g.Width, g.Height, g.CropX, g.CropY)
	switch ((-g.Rotation)%360 + 360) % 360 { // clockwise turn needed for display
	case 90:
		f += ",transpose=clock"
	case 180:
		f += ",hflip,vflip"
	case 270:
		f += ",transpose=cclock"
	}
	return f + "[img]"
}
