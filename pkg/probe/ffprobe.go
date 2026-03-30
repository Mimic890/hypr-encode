// Package probe provides media file analysis using ffprobe.
// It runs ffprobe as a subprocess and parses its JSON output
// into clean, typed Go structs.
package probe

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ── Output types ──────────────────────────────────────────────────────────────

// MediaInfo holds everything we know about a media file.
type MediaInfo struct {
	FilePath   string        `json:"filePath"`
	FileName   string        `json:"fileName"`
	FileSize   int64         `json:"fileSize"`   // bytes
	DurationMs int64         `json:"durationMs"` // milliseconds
	Format     string        `json:"format"`
	FormatLong string        `json:"formatLong"`
	BitrateBps int64         `json:"bitrateBps"`
	Chapters   int               `json:"chapters"`
	Tags       map[string]string `json:"tags"`  // format-level metadata (ID3, Vorbis, EXIF…)
	Video      []VideoStream     `json:"video"`
	Audio      []AudioStream     `json:"audio"`
	Subtitle   []SubStream       `json:"subtitle"`
}

// HDRInfo describes the HDR/colour characteristics of a video stream.
// Multiple flags can be true at once (e.g. Dolby Vision + HDR10 compat layer).
type HDRInfo struct {
	IsHDR10       bool `json:"isHdr10"`
	IsHDR10Plus   bool `json:"isHdr10Plus"`
	IsHLG         bool `json:"isHlg"`
	IsDolbyVision bool `json:"isDolbyVision"`

	DVProfile int `json:"dvProfile,omitempty"`
	DVLevel   int `json:"dvLevel,omitempty"`
	// DVCompatibility: "HDR10", "SDR", "HLG", or "" (DV-only / unknown)
	DVCompatibility string `json:"dvCompatibility,omitempty"`

	// Content light levels (nits) from HDR SEI metadata
	MaxCLL  int `json:"maxCll,omitempty"`
	MaxFALL int `json:"maxFall,omitempty"`

	// Mastering display luminance (nits) from HDR SEI metadata
	MaxLuminance float64 `json:"maxLuminance,omitempty"`
	MinLuminance float64 `json:"minLuminance,omitempty"`

	// Raw colour encoding strings from ffprobe
	ColorPrimaries string `json:"colorPrimaries,omitempty"`
	ColorTransfer  string `json:"colorTransfer,omitempty"`
	ColorSpace     string `json:"colorSpace,omitempty"`
}

// VideoStream describes one video track inside a file.
type VideoStream struct {
	Index         int     `json:"index"`
	Codec         string  `json:"codec"`
	CodecLong     string  `json:"codecLong"`
	Profile       string  `json:"profile"`
	Level         int     `json:"level"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
	FPS           float64 `json:"fps"`
	NbFrames      int64   `json:"nbFrames"`
	PixFmt        string  `json:"pixFmt"`
	BitDepth      int     `json:"bitDepth"`
	BitrateBps    int64   `json:"bitrateBps"`
	SizeBytes     int64   `json:"sizeBytes"`
	ColorRange    string  `json:"colorRange"`
	Language      string  `json:"language"`
	Title         string  `json:"title"`
	HDR           HDRInfo `json:"hdr"`
	IsAttachedPic bool    `json:"isAttachedPic"` // true for cover art embedded in audio files
}

// AudioStream describes one audio track inside a file.
type AudioStream struct {
	Index         int    `json:"index"`
	Codec         string `json:"codec"`
	CodecLong     string `json:"codecLong"`
	Profile       string `json:"profile"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channelLayout"`
	SampleRate    int    `json:"sampleRate"`
	BitrateBps    int64  `json:"bitrateBps"`
	SizeBytes     int64  `json:"sizeBytes"`
	Language      string `json:"language"`
	Title         string `json:"title"`
	IsAtmos       bool   `json:"isAtmos"`
	IsDTSX        bool   `json:"isDtsX"`
}

// SubStream describes one subtitle track inside a file.
type SubStream struct {
	Index      int    `json:"index"`
	Codec      string `json:"codec"`
	Language   string `json:"language"`
	Title      string `json:"title"`
	Forced     bool   `json:"forced"`
	BitrateBps int64  `json:"bitrateBps"`
	SizeBytes  int64  `json:"sizeBytes"`
}

// ── Public API ────────────────────────────────────────────────────────────────

// ScanFile runs ffprobe on the given file and returns full media information.
// Returns an error if ffprobe is not installed, the file can't be read, or
// the output can't be parsed.
func ScanFile(path string) (*MediaInfo, error) {
	// Check that ffprobe is available before trying to use it.
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return nil, fmt.Errorf("ffprobe not found in PATH — please install ffmpeg: %w", err)
	}

	out, err := exec.Command("ffprobe",
		"-v", "quiet",           // suppress banner and progress noise
		"-print_format", "json", // output as JSON so we can parse it
		"-show_streams",         // include per-track info
		"-show_format",          // include container/file info
		"-show_chapters",        // include chapter list
		path,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed on %q: %w", filepath.Base(path), err)
	}

	var raw rawFFprobe
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	info := &MediaInfo{
		FilePath:   path,
		FileName:   filepath.Base(path),
		Format:     raw.Format.FormatName,
		FormatLong: raw.Format.FormatLongName,
		Chapters:   len(raw.Chapters),
		Tags:       raw.Format.Tags,
	}

	// ffprobe returns size/duration/bitrate as strings, so we parse them manually.
	if sz, err := strconv.ParseInt(raw.Format.Size, 10, 64); err == nil {
		info.FileSize = sz
	}
	if dur, err := strconv.ParseFloat(raw.Format.Duration, 64); err == nil {
		info.DurationMs = int64(dur * 1000)
	}
	if br, err := strconv.ParseInt(raw.Format.BitRate, 10, 64); err == nil {
		info.BitrateBps = br
	}

	for _, s := range raw.Streams {
		switch s.CodecType {
		case "video":
			info.Video = append(info.Video, parseVideoStream(s))
		case "audio":
			info.Audio = append(info.Audio, parseAudioStream(s))
		case "subtitle":
			info.Subtitle = append(info.Subtitle, parseSubStream(s))
		}
	}

	// Sanity check: some MKV muxers write the *decoded* (uncompressed) byte count
	// into NUMBER_OF_BYTES instead of the encoded size. A stream cannot be larger
	// than the whole file, so we zero out any bogus value.
	for i := range info.Video {
		if info.FileSize > 0 && info.Video[i].SizeBytes > info.FileSize {
			info.Video[i].SizeBytes = 0
		}
	}

	return info, nil
}

// ── Stream parsers ────────────────────────────────────────────────────────────

func parseVideoStream(s rawStream) VideoStream {
	vs := VideoStream{
		Index:      s.Index,
		Codec:      s.CodecName,
		CodecLong:  s.CodecLongName,
		Profile:    s.Profile,
		Level:      s.Level,
		Width:      s.Width,
		Height:     s.Height,
		FPS:        parseFPS(s.RFrameRate),
		PixFmt:     s.PixFmt,
		BitDepth:   parseIntStr(s.BitsPerRawSample),
		ColorRange: s.ColorRange,
		Language:   s.Tags["language"], // reading a nil map in Go is safe — returns ""
		Title:      s.Tags["title"],
	}
	if br, err := strconv.ParseInt(s.BitRate, 10, 64); err == nil {
		vs.BitrateBps = br
	}
	if nf, err := strconv.ParseInt(s.NbFrames, 10, 64); err == nil {
		vs.NbFrames = nf
	}
	vs.SizeBytes = tagInt64Prefix(s.Tags, "NUMBER_OF_BYTES")
	// If ffprobe didn't report bit depth directly, infer it from the pixel format name.
	if vs.BitDepth == 0 {
		vs.BitDepth = bitDepthFromPixFmt(s.PixFmt)
	}
	vs.HDR = detectHDR(s)
	vs.IsAttachedPic = s.Disposition.AttachedPic == 1
	return vs
}

func parseAudioStream(s rawStream) AudioStream {
	as := AudioStream{
		Index:         s.Index,
		Codec:         s.CodecName,
		CodecLong:     s.CodecLongName,
		Profile:       s.Profile,
		Channels:      s.Channels,
		ChannelLayout: s.ChannelLayout,
		SampleRate:    parseIntStr(s.SampleRate),
		Language:      s.Tags["language"],
		Title:         s.Tags["title"],
	}
	if br, err := strconv.ParseInt(s.BitRate, 10, 64); err == nil {
		as.BitrateBps = br
	}
	// MKV containers often store per-track bitrate in tags instead of the stream header.
	if as.BitrateBps == 0 {
		as.BitrateBps = tagInt64Prefix(s.Tags, "BPS")
	}
	as.SizeBytes = tagInt64Prefix(s.Tags, "NUMBER_OF_BYTES")

	// Dolby Atmos is embedded inside TrueHD or EAC-3 streams.
	// ffprobe exposes it in the Profile field when it can detect it.
	profileUp := strings.ToUpper(s.Profile)
	if (s.CodecName == "truehd" || s.CodecName == "eac3") && strings.Contains(profileUp, "ATMOS") {
		as.IsAtmos = true
	}

	// DTS:X is a DTS stream with an object-audio extension layer.
	if s.CodecName == "dts" &&
		(strings.Contains(profileUp, "DTS:X") || strings.Contains(profileUp, "DTS-X")) {
		as.IsDTSX = true
	}

	return as
}

func parseSubStream(s rawStream) SubStream {
	ss := SubStream{
		Index:    s.Index,
		Codec:    s.CodecName,
		Language: s.Tags["language"],
		Title:    s.Tags["title"],
		Forced:   s.Disposition.Forced == 1,
	}
	ss.BitrateBps = tagInt64Prefix(s.Tags, "BPS")
	ss.SizeBytes = tagInt64Prefix(s.Tags, "NUMBER_OF_BYTES")
	return ss
}

// ── HDR detection ─────────────────────────────────────────────────────────────

// detectHDR inspects stream colour metadata and side-data records to identify
// the HDR format(s) in use.
func detectHDR(s rawStream) HDRInfo {
	h := HDRInfo{
		ColorPrimaries: s.ColorPrimaries,
		ColorTransfer:  s.ColorTransfer,
		ColorSpace:     s.ColorSpace,
	}

	// HDR10: BT.2020 colour primaries + PQ (SMPTE ST 2084) transfer curve.
	if s.ColorPrimaries == "bt2020" && s.ColorTransfer == "smpte2084" {
		h.IsHDR10 = true
	}
	// HLG: Hybrid Log-Gamma, common in broadcast HDR (ARIB STD-B67).
	if s.ColorTransfer == "arib-std-b67" {
		h.IsHLG = true
	}

	// Side-data records carry Dolby Vision config, HDR10+ metadata, and
	// the mastering display / content light level SEI NALUs.
	for _, sd := range s.SideDataList {
		switch sd.SideDataType {
		case "DOVI configuration record":
			h.IsDolbyVision = true
			h.DVProfile = sd.DVProfile
			h.DVLevel = sd.DVLevel
			h.DVCompatibility = dvCompatibility(sd.DVBLCompatID)

		case "HDR Dynamic Metadata SMPTE2094-40":
			h.IsHDR10Plus = true

		case "Content light level metadata":
			h.MaxCLL = sd.MaxContent
			h.MaxFALL = sd.MaxAverage

		case "Mastering display metadata":
			if sd.MaxLuminance != "" {
				h.MaxLuminance = parseFraction(sd.MaxLuminance)
			}
			if sd.MinLuminance != "" {
				h.MinLuminance = parseFraction(sd.MinLuminance)
			}
		}
	}

	// Some Dolby Vision files signal DV via the codec tag instead of side-data.
	switch strings.ToLower(s.CodecTagString) {
	case "dvhe", "dvav", "dvh1", "dva1":
		h.IsDolbyVision = true
	}

	return h
}

// dvCompatibility converts a Dolby Vision backward-compatibility ID to a label.
// Values are defined in ETSI TS 103 433.
//
//	0 = unspecified / DV-only
//	1 = HDR10 (SMPTE ST 2086 + ST 2094-10)
//	2 = SDR BT.709
//	4 = HLG (ARIB STD-B67)
func dvCompatibility(id int) string {
	switch id {
	case 1:
		return "HDR10"
	case 2:
		return "SDR"
	case 4:
		return "HLG"
	default:
		return "" // DV-only or unrecognised
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// tagInt64Prefix reads an integer from the first tag whose key equals base or
// starts with base+"-" (handles any language suffix: -eng, -rus, -chi, …).
// MKV containers often store track stats this way (e.g. "BPS-eng").
func tagInt64Prefix(tags map[string]string, base string) int64 {
	prefix := base + "-"
	for k, v := range tags {
		if k == base || strings.HasPrefix(k, prefix) {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

// parseFraction parses a value that may be a plain float ("23.976") or a
// fraction string ("24000/1001") and returns it as float64, rounded to 2 dp.
func parseFraction(s string) float64 {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	num, err1 := strconv.ParseFloat(parts[0], 64)
	den, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || den == 0 {
		return 0
	}
	return math.Round(num/den*100) / 100
}

// parseFPS parses an ffprobe frame-rate string like "24000/1001" → 23.976.
func parseFPS(rate string) float64 {
	parts := strings.SplitN(rate, "/", 2)
	if len(parts) != 2 {
		return 0
	}
	num, err1 := strconv.ParseFloat(parts[0], 64)
	den, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || den == 0 {
		return 0
	}
	return math.Round(num/den*1000) / 1000
}

// parseIntStr converts a string to int, returning 0 if empty or invalid.
func parseIntStr(val string) int {
	n, _ := strconv.Atoi(val)
	return n
}

// bitDepthFromPixFmt infers bit depth from a pixel format name like "yuv420p10le".
// Returns 0 for an empty/unrecognised format, 8 for standard formats.
func bitDepthFromPixFmt(pix string) int {
	switch {
	case pix == "":
		return 0
	case strings.Contains(pix, "p10"):
		return 10
	case strings.Contains(pix, "p12"):
		return 12
	case strings.Contains(pix, "p16"):
		return 16
	default:
		return 8
	}
}

// ── Raw ffprobe JSON structs ──────────────────────────────────────────────────
// These mirror the ffprobe JSON output exactly.
// They are only used internally; callers work with the clean types above.

type rawFFprobe struct {
	Streams  []rawStream  `json:"streams"`
	Format   rawFormat    `json:"format"`
	Chapters []rawChapter `json:"chapters"`
}

type rawStream struct {
	Index            int               `json:"index"`
	CodecName        string            `json:"codec_name"`
	CodecLongName    string            `json:"codec_long_name"`
	CodecType        string            `json:"codec_type"`
	CodecTagString   string            `json:"codec_tag_string"`
	Profile          string            `json:"profile"`
	Level            int               `json:"level"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	RFrameRate       string            `json:"r_frame_rate"`
	AvgFrameRate     string            `json:"avg_frame_rate"`
	BitRate          string            `json:"bit_rate"`
	PixFmt           string            `json:"pix_fmt"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	ColorRange       string            `json:"color_range"`
	ColorSpace       string            `json:"color_space"`
	ColorTransfer    string            `json:"color_transfer"`
	ColorPrimaries   string            `json:"color_primaries"`
	Channels         int               `json:"channels"`
	ChannelLayout    string            `json:"channel_layout"`
	SampleRate       string            `json:"sample_rate"`
	NbFrames         string            `json:"nb_frames"`
	Disposition      rawDisposition    `json:"disposition"`
	Tags             map[string]string `json:"tags"`
	SideDataList     []rawSideData     `json:"side_data_list"`
}

type rawDisposition struct {
	Forced      int `json:"forced"`
	AttachedPic int `json:"attached_pic"`
}

type rawSideData struct {
	SideDataType string `json:"side_data_type"`
	// Dolby Vision config record
	DVProfile    int `json:"dv_profile"`
	DVLevel      int `json:"dv_level"`
	DVBLCompatID int `json:"dv_bl_signal_compatibility_id"`
	// Content light level
	MaxContent int `json:"max_content"`
	MaxAverage int `json:"max_average"`
	// Mastering display luminance (stored as fraction strings, e.g. "34000/50000")
	MaxLuminance string `json:"max_luminance"`
	MinLuminance string `json:"min_luminance"`
}

type rawFormat struct {
	FormatName     string            `json:"format_name"`
	FormatLongName string            `json:"format_long_name"`
	Duration       string            `json:"duration"`
	Size           string            `json:"size"`
	BitRate        string            `json:"bit_rate"`
	Tags           map[string]string `json:"tags"`
}

type rawChapter struct {
	ID int64 `json:"id"`
}
