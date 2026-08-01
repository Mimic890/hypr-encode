// Terminal rendering for hypr-encode.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/Mimic890/hypr-encode/pkg/probe"
)

// ── Colours ───────────────────────────────────────────────────────────────────
// We use RGB ANSI escape codes for precise colours.
// Set NO_COLOR=1 (or any non-empty value) to disable them — see no-color.org.

var (
	anTeal    = "\x1b[38;2;45;200;176m"
	anGreen   = "\x1b[38;2;100;220;100m"
	anYellow  = "\x1b[38;2;219;180;50m"
	anOrange  = "\x1b[38;2;255;160;60m"
	anMagenta = "\x1b[38;2;200;100;224m"
	anCyan    = "\x1b[38;2;80;210;230m"
	anGray    = "\x1b[38;2;144;144;144m"
	anDim     = "\x1b[38;2;80;88;96m"
	anWhite   = "\x1b[38;2;220;220;220m"
	anRed     = "\x1b[38;2;224;80;80m"
	anBold    = "\x1b[1m"
	anReset   = "\x1b[0m"
)

func init() {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" || !term.IsTerminal(int(os.Stdout.Fd())) {
		anTeal, anGreen, anYellow, anOrange = "", "", "", ""
		anMagenta, anCyan, anGray, anDim = "", "", "", ""
		anWhite, anRed, anBold, anReset = "", "", "", ""
	}
}

// langColor returns an ANSI colour for a given ISO 639 language code.
// Returns empty string when colours are disabled (NO_COLOR).
func langColor(lang string) string {
	if anReset == "" {
		return ""
	}
	switch strings.ToLower(lang) {
	case "rus", "ru":
		return "\x1b[38;2;220;80;80m" // red
	case "eng", "en":
		return "\x1b[38;2;80;150;230m" // blue
	case "jpn", "ja":
		return "\x1b[38;2;255;150;190m" // pink
	case "kor", "ko":
		return "\x1b[38;2;255;160;60m" // orange
	case "chi", "zho", "zh":
		return "\x1b[38;2;219;180;50m" // gold
	case "fra", "fr":
		return "\x1b[38;2;100;170;255m" // sky-blue
	case "deu", "ger", "de":
		return "\x1b[38;2;190;190;190m" // gray
	case "spa", "es":
		return "\x1b[38;2;255;130;60m" // warm orange
	case "ita", "it":
		return "\x1b[38;2;80;200;100m" // green
	case "por", "pt":
		return "\x1b[38;2;60;200;140m" // teal-green
	case "ara", "ar":
		return "\x1b[38;2;200;100;224m" // purple
	default:
		return anWhite
	}
}

// ── ANSI-aware string helpers ─────────────────────────────────────────────────
// Terminal display width ≠ byte length when ANSI codes are present.
// These helpers measure and pad using *visible* character count.

// ansiEscRe matches ANSI colour escape sequences so we can strip them.
var ansiEscRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visLen returns the visible (on-screen) length of s, ignoring ANSI codes.
func visLen(s string) int {
	return runewidth.StringWidth(ansiEscRe.ReplaceAllString(s, ""))
}

// visPad right-pads s with spaces until its visible width reaches w.
func visPad(s string, w int) string {
	if n := visLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// visTrunc truncates s to maxVis visible characters, appending "…" if needed.
func visTrunc(s string, maxVis int) string {
	if maxVis <= 0 {
		return ""
	}
	plain := ansiEscRe.ReplaceAllString(s, "")
	if runewidth.StringWidth(plain) > maxVis {
		return runewidth.Truncate(plain, maxVis, "…")
	}
	return s
}

// safeText prevents filenames and media metadata from emitting terminal controls.
func safeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 0x20 || r >= 0x7f && r <= 0x9f || r == 0x2028 || r == 0x2029 || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return '?'
		}
		return r
	}, s)
}

// ── Formatting helpers ────────────────────────────────────────────────────────

func fmtSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	default:
		return fmt.Sprintf("%d KB", n>>10)
	}
}

func fmtDurMs(ms int64) string {
	s := ms / 1000
	h, m, sec := s/3600, (s%3600)/60, s%60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm %02ds", h, m, sec)
	}
	return fmt.Sprintf("%dm %02ds", m, sec)
}

func fmtKbps(n int64) string { return fmt.Sprintf("%d kb/s", n/1000) }

func fmtFrames(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM frames", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK frames", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d frames", n)
	}
}

// chanStr turns an ffprobe channel count + layout into a short label like "5.1".
func chanStr(count int, layout string) string {
	switch strings.ToLower(layout) {
	case "mono":
		return "1.0"
	case "stereo":
		return "2.0"
	case "5.1", "5.1(side)":
		return "5.1"
	case "7.1", "7.1(wide)", "7.1(wide-side)":
		return "7.1"
	}
	switch count {
	case 1:
		return "1.0"
	case 2:
		return "2.0"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	if count > 0 {
		return fmt.Sprintf("%dch", count)
	}
	return ""
}

// codecLabel maps ffprobe codec names to human-readable labels.
func codecLabel(c string) string {
	c = safeText(c)
	switch c {
	case "hevc":
		return "H.265"
	case "h264", "avc":
		return "H.264"
	case "av1":
		return "AV1"
	case "vp9":
		return "VP9"
	case "truehd":
		return "TrueHD"
	case "eac3":
		return "E-AC-3"
	case "ac3":
		return "AC-3"
	case "dts":
		return "DTS"
	case "aac":
		return "AAC"
	case "flac":
		return "FLAC"
	case "opus":
		return "Opus"
	case "mjpeg":
		return "JPEG"
	case "png":
		return "PNG"
	case "webp":
		return "WebP"
	case "gif", "bmp", "tiff", "avif":
		return strings.ToUpper(c)
	case "hdmv_pgs_subtitle":
		return "PGS"
	case "subrip", "srt":
		return "SRT"
	case "ass", "ssa":
		return "ASS"
	case "dvd_subtitle":
		return "VOBSUB"
	}
	return strings.ToUpper(c)
}

// shortenPath replaces the home directory prefix with ~.
func shortenPath(p string) string {
	if home, _ := os.UserHomeDir(); home != "" {
		if p == home {
			return "~"
		}
		if prefix := home + string(filepath.Separator); strings.HasPrefix(p, prefix) {
			return safeText("~" + p[len(home):])
		}
	}
	return safeText(p)
}

// probeLangTag formats a language code as a coloured bracket tag: [rus] in red, [eng] in blue, etc.
func probeLangTag(lang string) string {
	lang = safeText(lang)
	if lang == "" || lang == "und" {
		return anDim + "[?]" + anReset
	}
	return langColor(lang) + anBold + "[" + lang + "]" + anReset
}

// ── File type detection ───────────────────────────────────────────────────────

// isImageCodec returns true for codecs used by standalone image files.
func isImageCodec(codec string) bool {
	switch codec {
	case "mjpeg", "png", "webp", "gif", "bmp", "tiff", "av1", "hevc":
		return true
	}
	return false
}

// isImageFile distinguishes image containers from videos using image codecs.
func isImageFile(info *probe.MediaInfo) bool {
	if len(info.Audio) > 0 || len(info.Subtitle) > 0 {
		return false
	}
	switch strings.ToLower(filepath.Ext(info.FileName)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".tif", ".tiff", ".avif", ".heic", ".heif":
	default:
		return false
	}
	streams := 0
	for _, v := range info.Video {
		if v.IsAttachedPic {
			continue
		}
		if !isImageCodec(v.Codec) || v.Width <= 0 || v.Height <= 0 || v.NbFrames > 1 {
			return false
		}
		streams++
	}
	return streams == 1
}

// ── Terminal width ────────────────────────────────────────────────────────────

// termWidth returns the current terminal width, clamped to a minimum of 40.
func termWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 80
	}
	return max(w, 40)
}

// hasRealVideo reports whether the file contains at least one non-cover-art video stream.
func hasRealVideo(info *probe.MediaInfo) bool {
	for _, v := range info.Video {
		if !v.IsAttachedPic {
			return true
		}
	}
	return false
}

// ── Shared helpers ────────────────────────────────────────────────────────────

// buildMeta assembles a short metadata line: size · duration · bitrate · chapters.
// For image files, duration and bitrate are meaningless (ffprobe derives them
// from a fake 1-frame duration), so we skip them.
func buildMeta(info *probe.MediaInfo) string {
	parts := []string{fmtSize(info.FileSize)}
	if !isImageFile(info) {
		if info.DurationMs > 0 {
			parts = append(parts, fmtDurMs(info.DurationMs))
		}
		if info.BitrateBps > 0 {
			parts = append(parts, fmtKbps(info.BitrateBps))
		}
	}
	if info.Chapters > 0 {
		parts = append(parts, fmt.Sprintf("%d chap", info.Chapters))
	}
	return strings.Join(parts, "  ·  ")
}

// ── Metadata tables ───────────────────────────────────────────────────────────

// metaKey maps a display label to one or more ffprobe tag key variants.
// The first variant that has a non-empty value wins.
type metaKey struct {
	label string
	keys  []string
}

// Common metadata key sets per file type.
var (
	audioMetaKeys = []metaKey{
		{"title", []string{"title"}},
		{"artist", []string{"artist", "album_artist"}},
		{"album", []string{"album"}},
		{"year", []string{"date", "year"}},
		{"track", []string{"track", "tracknumber"}},
		{"disc", []string{"disc", "discnumber"}},
		{"genre", []string{"genre"}},
		{"composer", []string{"composer"}},
		{"comment", []string{"comment"}},
	}
	videoMetaKeys = []metaKey{
		{"title", []string{"title"}},
		{"date", []string{"date", "creation_time"}},
		{"encoder", []string{"encoder", "writing_application"}},
		{"comment", []string{"comment", "description"}},
	}
	imageMetaKeys = []metaKey{
		{"camera", []string{"make"}},
		{"model", []string{"model"}},
		{"date", []string{"datetimeoriginal", "datetime", "creation_time", "date"}},
		{"software", []string{"software"}},
		{"ISO", []string{"isospeedratings", "iso_speed_ratings"}},
		{"aperture", []string{"fnumber", "f_number"}},
		{"shutter", []string{"exposuretime", "exposure_time"}},
		{"focal", []string{"focallength", "focal_length"}},
		{"artist", []string{"artist"}},
	}
)

// resolveMeta extracts key-value rows from a tag map using the given key set.
func resolveMeta(tags map[string]string, keys []metaKey) [][2]string {
	var rows [][2]string
	tagKeys := make([]string, 0, len(tags))
	for key := range tags {
		tagKeys = append(tagKeys, key)
	}
	sort.Strings(tagKeys)
	for _, mk := range keys {
		for _, k := range mk.keys {
			for _, actual := range tagKeys {
				if strings.EqualFold(actual, k) {
					if v := strings.TrimSpace(tags[actual]); v != "" {
						rows = append(rows, [2]string{mk.label, safeText(v)})
						break
					}
				}
			}
			if len(rows) > 0 && rows[len(rows)-1][0] == mk.label {
				break
			}
		}
	}
	return rows
}

// printMetaRows renders a metadata table inside an open box.
// It prints a ├────┤ separator followed by aligned label  value rows.
func printMetaRows(rows [][2]string, boxW int, borderColor string) {
	if len(rows) == 0 {
		return
	}
	maxLabel := 0
	for _, r := range rows {
		if n := visLen(r[0]); n > maxLabel {
			maxLabel = n
		}
	}
	fmt.Printf("  %s├%s┤%s\n", borderColor, strings.Repeat("─", boxW+2), anReset)
	for _, r := range rows {
		label := visPad(anGray+r[0]+anReset, maxLabel+visLen(anGray+anReset))
		valW := max(boxW-maxLabel-2, 1)
		content := label + "  " + anWhite + visTrunc(r[1], valW) + anReset
		fmt.Printf("  %s│%s %s %s│%s\n",
			borderColor, anReset, visPad(content, boxW), borderColor, anReset)
	}
}

// ── File card (directory listing) ─────────────────────────────────────────────

func printFileCard(info *probe.MediaInfo, tw int, num int) {
	cardOuter := max(min(tw-2, 110), 32)
	innerW := cardOuter - 2
	contentW := innerW - 2
	border := strings.Repeat("─", innerW)

	// line prints one row of content inside the card border.
	line := func(content string) {
		fmt.Printf(" %s│%s %s %s│%s\n",
			anMagenta, anReset, visPad(content, contentW), anMagenta, anReset)
	}

	fmt.Printf(" %s╭%s╮%s\n", anMagenta, border, anReset)
	numTag := anMagenta + fmt.Sprintf("#%d", num) + anReset + "  "
	line(numTag + anBold + visTrunc(safeText(info.FileName), contentW-visLen(numTag)) + anReset)
	line(anGray + buildMeta(info) + anReset)

	image := isImageFile(info)
	for _, v := range info.Video {
		if v.IsAttachedPic {
			continue // skip cover art (e.g. album art in MP3 files)
		}
		if image {
			// For still images, show stream info inline — no nested box needed.
			for _, l := range videoBoxContent(v, 0, true) {
				line(l)
			}
		} else {
			for _, l := range videoBoxLines(v, info.DurationMs, contentW-2, anWhite, false) {
				line(l)
			}
		}
	}

	if len(info.Audio) > 0 {
		line(anGreen + anBold + "AUDIO" + anReset)
		var cards []trackCard
		for _, a := range info.Audio {
			cards = append(cards, makeAudioCard(a))
		}
		for _, l := range trackCardRowLines(cards, contentW) {
			line(l)
		}
	}

	if len(info.Subtitle) > 0 {
		line(anYellow + anBold + "SUBS" + anReset)
		var cards []trackCard
		for _, s := range info.Subtitle {
			cards = append(cards, makeSubCard(s))
		}
		for _, l := range trackCardRowLines(cards, contentW) {
			line(l)
		}
	}

	fmt.Printf(" %s╰%s╯%s\n", anMagenta, border, anReset)
}

// ── File info (single file, verbose) ──────────────────────────────────────────

func printFileInfo(info *probe.MediaInfo) {
	tw := termWidth()

	// ── Image files: one compact combined box ──────────────────────────────────
	if isImageFile(info) {
		metaStr := buildMeta(info)
		tagRows := resolveMeta(info.Tags, imageMetaKeys)

		var imgLines [][]string
		for _, v := range info.Video {
			imgLines = append(imgLines, videoBoxContent(v, 0, true))
		}

		// Box width = widest of: filename, meta, stream lines, tag rows.
		maxW := max(visLen(safeText(info.FileName)), visLen(metaStr))
		for _, ls := range imgLines {
			for _, l := range ls {
				if w := visLen(l); w > maxW {
					maxW = w
				}
			}
		}
		for _, r := range tagRows {
			if w := visLen(r[0]) + 2 + visLen(r[1]); w > maxW {
				maxW = w
			}
		}
		boxW := max(min(maxW+2, tw-6), 16)
		border := strings.Repeat("─", boxW+2)

		row := func(content string) {
			fmt.Printf("  %s│%s %s %s│%s\n",
				anTeal, anReset, visPad(visTrunc(content, boxW), boxW), anTeal, anReset)
		}

		fmt.Printf("\n  %s╭%s╮%s\n", anTeal, border, anReset)
		row(anBold + visTrunc(safeText(info.FileName), boxW) + anReset)
		row(anGray + metaStr + anReset)
		fmt.Printf("  %s├%s┤%s\n", anTeal, border, anReset)
		for _, ls := range imgLines {
			for _, l := range ls {
				row(l)
			}
		}
		printMetaRows(tagRows, boxW, anTeal)
		fmt.Printf("  %s╰%s╯%s\n\n", anTeal, border, anReset)
		return
	}

	// ── Header box ────────────────────────────────────────────────────────────
	audioOnly := !hasRealVideo(info)
	metaStr := buildMeta(info)

	metaKeys := videoMetaKeys
	if audioOnly {
		metaKeys = audioMetaKeys
	}
	tagRows := resolveMeta(info.Tags, metaKeys)

	naturalW := max(visLen(safeText(info.FileName)), visLen(metaStr))
	for _, r := range tagRows {
		if w := visLen(r[0]) + 2 + visLen(r[1]); w > naturalW {
			naturalW = w
		}
	}
	boxW := max(min(naturalW+2, tw-6), 16)
	border := strings.Repeat("─", boxW+2)

	row := func(color, content string) {
		fmt.Printf("  %s│%s %s%s%s %s│%s\n",
			anTeal, anReset, color, visPad(visTrunc(content, boxW), boxW), anReset, anTeal, anReset)
	}

	fmt.Printf("\n  %s╭%s╮%s\n", anTeal, border, anReset)
	row(anBold, safeText(info.FileName))
	row(anGray, metaStr)
	printMetaRows(tagRows, boxW, anTeal)
	fmt.Printf("  %s╰%s╯%s\n\n", anTeal, border, anReset)

	printed := false
	for _, v := range info.Video {
		if !v.IsAttachedPic {
			printVideoBox(v, info.DurationMs, tw)
			printed = true
		}
	}
	if printed {
		fmt.Println()
	}

	if len(info.Audio) > 0 {
		fmt.Printf("  %s%sAUDIO%s\n", anGreen, anBold, anReset)
		var cards []trackCard
		for _, a := range info.Audio {
			cards = append(cards, makeAudioCard(a))
		}
		printTrackCards(cards, tw)
	}

	if len(info.Subtitle) > 0 {
		fmt.Printf("  %s%sSUBS%s\n", anYellow, anBold, anReset)
		var cards []trackCard
		for _, s := range info.Subtitle {
			cards = append(cards, makeSubCard(s))
		}
		printTrackCards(cards, tw)
	}
}

// ── Track cards ────────────────────────────────────────────────────────────────

// trackCard is a small box that gets rendered side-by-side with other cards.
type trackCard struct {
	title string // shown in the top border, e.g. "#0"
	color string // ANSI border colour
	lines []string
}

// videoBoxContent returns 1-3 text lines for a video or image stream.
// Still images skip fps/frame/bitrate/profile — those fields are meaningless for them.
func videoBoxContent(v probe.VideoStream, durationMs int64, image bool) []string {

	// ── Line 1 ────────────────────────────────────────────────────────────────
	parts1 := []string{anTeal + anBold + codecLabel(v.Codec) + anReset}
	if v.Width > 0 && v.Height > 0 {
		parts1 = append(parts1, anWhite+anBold+fmt.Sprintf("%d×%d", v.Width, v.Height)+anReset)
	}
	if !image {
		// FPS and frame count are meaningless for still images.
		if v.FPS > 0 {
			parts1 = append(parts1, anYellow+fmt.Sprintf("%.4g fps", v.FPS)+anReset)
		}
		nbf := v.NbFrames
		if nbf == 0 && v.FPS > 0 && durationMs > 0 {
			nbf = int64(float64(durationMs)/1000*v.FPS + 0.5)
		}
		if nbf > 0 {
			parts1 = append(parts1, anGray+fmtFrames(nbf)+anReset)
		}
		if v.SizeBytes > 0 {
			parts1 = append(parts1, anDim+fmtSize(v.SizeBytes)+anReset)
		}
	}

	// ── Line 2 ────────────────────────────────────────────────────────────────
	var parts2 []string
	if v.BitDepth > 0 {
		parts2 = append(parts2, anCyan+fmt.Sprintf("%d-bit", v.BitDepth)+anReset)
	}
	if v.PixFmt != "" {
		parts2 = append(parts2, anDim+safeText(v.PixFmt)+anReset)
	}
	if !image && v.ColorRange != "" {
		// Color range ("tv"/"pc") is noise for still images.
		parts2 = append(parts2, anDim+safeText(v.ColorRange)+anReset)
	}
	if !image && v.BitrateBps > 0 {
		// Bitrate for images is derived from a fake 1-frame duration — skip it.
		parts2 = append(parts2, anGray+fmtKbps(v.BitrateBps)+anReset)
	}

	// ── Line 3: profile + HDR/colour info ─────────────────────────────────────
	var parts3 []string
	if !image && v.Profile != "" {
		// Profile ("Baseline", "Main") is noise for still images.
		parts3 = append(parts3, anOrange+safeText(v.Profile)+anReset)
	}

	h := v.HDR
	if h.IsDolbyVision {
		dv := anMagenta + anBold + "◆ Dolby Vision" + anReset
		if h.DVProfile > 0 {
			dv += anMagenta + fmt.Sprintf(" P%d.%d", h.DVProfile, h.DVLevel) + anReset
		}
		if h.DVCompatibility != "" {
			dv += anGray + " (" + h.DVCompatibility + " compat)" + anReset
		}
		parts3 = append(parts3, dv)
	}
	if h.IsHDR10 {
		hdr := anYellow + anBold + "▸ HDR10" + anReset
		if h.MaxCLL > 0 {
			hdr += anGray + fmt.Sprintf(" MaxCLL %d", h.MaxCLL) + anReset
		}
		if h.MaxFALL > 0 {
			hdr += anGray + fmt.Sprintf(" MaxFALL %d", h.MaxFALL) + anReset
		}
		if h.MaxLuminance > 0 {
			hdr += anGray + fmt.Sprintf(" Peak %.0f nits", h.MaxLuminance) + anReset
		}
		parts3 = append(parts3, hdr)
	}
	if h.IsHDR10Plus {
		parts3 = append(parts3, anYellow+anBold+"▸ HDR10+"+anReset+anGray+" dynamic"+anReset)
	}
	if h.IsHLG {
		parts3 = append(parts3, anGreen+anBold+"▸ HLG"+anReset+anGray+" Hybrid Log-Gamma"+anReset)
	}
	if !h.IsHDR10 && !h.IsHDR10Plus && !h.IsHLG && !h.IsDolbyVision {
		xfr := h.ColorTransfer
		if xfr == "" {
			if image {
				xfr = "" // don't show "SDR" for plain images — it's just noise
			} else {
				xfr = "SDR"
			}
		}
		if xfr != "" {
			parts3 = append(parts3, anGray+safeText(xfr)+anReset)
		}
	}

	lines := []string{strings.Join(parts1, "   ")}
	if len(parts2) > 0 {
		lines = append(lines, strings.Join(parts2, "  ·  "))
	}
	if len(parts3) > 0 {
		lines = append(lines, strings.Join(parts3, "   "))
	}
	return lines
}

// videoBoxLines wraps videoBoxContent in a rounded box and returns the lines.
// The box header says "IMAGE" for still-image codecs and "VIDEO" for everything else.
func videoBoxLines(v probe.VideoStream, durationMs int64, boxInner int, color string, image bool) []string {
	cw := max(boxInner-2, 4) // content width inside the box walls
	label := "VIDEO"
	if image {
		label = "IMAGE"
	}
	title := fmt.Sprintf("─ %s #%d ", label, v.Index)
	topRest := max(boxInner-visLen(title), 0)

	out := []string{color + "╭" + title + strings.Repeat("─", topRest) + "╮" + anReset}
	for _, l := range videoBoxContent(v, durationMs, image) {
		out = append(out, color+"│"+anReset+" "+visPad(visTrunc(l, cw), cw)+" "+color+"│"+anReset)
	}
	return append(out, color+"╰"+strings.Repeat("─", boxInner)+"╯"+anReset)
}

// printVideoBox prints a video box, indented by 2 spaces.
func printVideoBox(v probe.VideoStream, durationMs int64, tw int) {
	for _, line := range videoBoxLines(v, durationMs, tw-4, anWhite, false) {
		fmt.Printf("  %s\n", line)
	}
}

// trackCardRowLines lays out track cards side-by-side in rows.
// Cards share a uniform width and fill the available totalW evenly.
func trackCardRowLines(cards []trackCard, totalW int) []string {
	if len(cards) == 0 {
		return nil
	}

	// Find the minimum width that fits all card content without truncation.
	minCw := 20
	for _, c := range cards {
		if w := visLen(c.title) + 3; w > minCw {
			minCw = w
		}
		for _, l := range c.lines {
			if w := visLen(l) + 2; w > minCw {
				minCw = w
			}
		}
	}

	const gap = 2 // space between side-by-side cards

	// Cards stay as wide as their content needs — no artificial stretching.
	cw := min(minCw, max(totalW-2, 4))

	// Fit as many columns as possible at the natural card width,
	// capped by actual card count (no empty columns).
	cols := min(len(cards), max(1, (totalW+gap)/(cw+2+gap)))

	var out []string
	for i := 0; i < len(cards); i += cols {
		row := cards[i:min(i+cols, len(cards))]

		// Find the tallest card so all cards in the row have the same height.
		maxLines := 0
		for _, c := range row {
			if len(c.lines) > maxLines {
				maxLines = len(c.lines)
			}
		}

		// Top borders
		var top strings.Builder
		for j, c := range row {
			if j > 0 {
				top.WriteString(strings.Repeat(" ", gap))
			}
			tp := "─ " + c.title + " "
			top.WriteString(c.color + "╭" + tp + strings.Repeat("─", max(cw-visLen(tp), 0)) + "╮" + anReset)
		}
		out = append(out, top.String())

		// Content lines
		for l := 0; l < maxLines; l++ {
			var mid strings.Builder
			for j, c := range row {
				if j > 0 {
					mid.WriteString(strings.Repeat(" ", gap))
				}
				content := ""
				if l < len(c.lines) {
					content = c.lines[l]
				}
				inner := cw - 2
				mid.WriteString(c.color + "│" + anReset + " " +
					visPad(visTrunc(content, inner), inner) + " " +
					c.color + "│" + anReset)
			}
			out = append(out, mid.String())
		}

		// Bottom borders
		var bot strings.Builder
		for j, c := range row {
			if j > 0 {
				bot.WriteString(strings.Repeat(" ", gap))
			}
			bot.WriteString(c.color + "╰" + strings.Repeat("─", cw) + "╯" + anReset)
		}
		out = append(out, bot.String())
	}
	return out
}

// printTrackCards prints track card rows indented by 2 spaces.
func printTrackCards(cards []trackCard, tw int) {
	for _, line := range trackCardRowLines(cards, tw-2) {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
}

// makeAudioCard builds a trackCard for one audio stream.
func makeAudioCard(a probe.AudioStream) trackCard {
	parts := []string{
		probeLangTag(a.Language),
		anTeal + anBold + codecLabel(a.Codec) + anReset,
	}
	if ch := chanStr(a.Channels, a.ChannelLayout); ch != "" {
		parts = append(parts, anWhite+ch+anReset)
	}
	if a.SampleRate > 0 {
		parts = append(parts, anYellow+fmt.Sprintf("%.4g kHz", float64(a.SampleRate)/1000)+anReset)
	}
	if a.BitrateBps > 0 {
		parts = append(parts, anGray+fmtKbps(a.BitrateBps)+anReset)
	}
	if a.SizeBytes > 0 {
		parts = append(parts, anDim+fmtSize(a.SizeBytes)+anReset)
	}
	if a.IsAtmos {
		parts = append(parts, anMagenta+anBold+"✦ ATMOS"+anReset)
	} else if a.IsDTSX {
		parts = append(parts, anMagenta+anBold+"✦ DTS:X"+anReset)
	}
	return trackCard{
		title: fmt.Sprintf("#%d", a.Index),
		color: anTeal,
		lines: []string{strings.Join(parts, "  ")},
	}
}

// subCodecColor picks a colour for a subtitle codec to visually distinguish formats.
func subCodecColor(codec string) string {
	switch codec {
	case "hdmv_pgs_subtitle":
		return anTeal // PGS
	case "subrip", "srt":
		return anGreen // SRT
	case "ass", "ssa":
		return anYellow // ASS
	default:
		return anGray
	}
}

// makeSubCard builds a trackCard for one subtitle stream.
func makeSubCard(s probe.SubStream) trackCard {
	parts := []string{
		probeLangTag(s.Language),
		subCodecColor(s.Codec) + anBold + codecLabel(s.Codec) + anReset,
	}
	if s.Forced {
		parts = append(parts, anOrange+anBold+"FORCED"+anReset)
	}
	if s.Title != "" {
		parts = append(parts, anWhite+safeText(s.Title)+anReset)
	}
	if s.SizeBytes > 0 {
		parts = append(parts, anDim+fmtSize(s.SizeBytes)+anReset)
	}
	return trackCard{
		title: fmt.Sprintf("#%d", s.Index),
		color: anYellow,
		lines: []string{strings.Join(parts, "  ")},
	}
}
