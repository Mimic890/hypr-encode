package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", n>>10)
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" && p != "0" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "  ·  ")
}

func kv(label, value string) string {
	return dimStyle.Render(fmt.Sprintf("%-10s", label)) + " " + value
}

// infoLines summarizes a file: format, streams and the main tags.
func infoLines(st *fileState) []string {
	if st.info == nil {
		return []string{dimStyle.Render("ffprobe cannot read this file; see the Metadata tab.")}
	}
	info := st.info
	lines := []string{boldStyle.Render(info.FileName), ""}
	duration := ""
	if info.DurationMs > 0 {
		duration = (time.Duration(info.DurationMs) * time.Millisecond).Truncate(time.Second).String()
	}
	bitrate := ""
	if info.BitrateBps > 0 {
		bitrate = fmt.Sprintf("%d kb/s", info.BitrateBps/1000)
	}
	lines = append(lines,
		kv("format", info.FormatLong),
		kv("size", joinNonEmpty(humanSize(info.FileSize), duration, bitrate)),
	)
	if info.Chapters > 0 {
		lines = append(lines, kv("chapters", fmt.Sprint(info.Chapters)))
	}

	for _, v := range info.Video {
		kind := "VIDEO"
		if v.IsAttachedPic {
			kind = "COVER"
		}
		var hdr []string
		for flag, name := range map[bool]string{v.HDR.IsDolbyVision: "Dolby Vision", v.HDR.IsHDR10: "HDR10", v.HDR.IsHDR10Plus: "HDR10+", v.HDR.IsHLG: "HLG"} {
			if flag {
				hdr = append(hdr, name)
			}
		}
		sort.Strings(hdr)
		fps := ""
		if v.FPS > 0 && !v.IsAttachedPic {
			fps = fmt.Sprintf("%.4g fps", v.FPS)
		}
		depth := ""
		if v.BitDepth > 0 {
			depth = fmt.Sprintf("%d-bit", v.BitDepth)
		}
		lines = append(lines, "", headStyle.Render(fmt.Sprintf("%s #%d", kind, v.Index)),
			joinNonEmpty(strings.ToUpper(v.Codec), fmt.Sprintf("%d×%d", v.Width, v.Height), fps, depth, v.PixFmt, strings.Join(hdr, " ")))
	}
	for _, a := range info.Audio {
		rate := ""
		if a.SampleRate > 0 {
			rate = fmt.Sprintf("%.4g kHz", float64(a.SampleRate)/1000)
		}
		extra := ""
		if a.IsAtmos {
			extra = "Atmos"
		} else if a.IsDTSX {
			extra = "DTS:X"
		}
		lines = append(lines, "", headStyle.Render(fmt.Sprintf("AUDIO #%d", a.Index)),
			joinNonEmpty("["+orUnd(a.Language)+"]", strings.ToUpper(a.Codec), a.ChannelLayout, rate, extra, a.Title))
	}
	for _, s := range info.Subtitle {
		forced := ""
		if s.Forced {
			forced = "forced"
		}
		lines = append(lines, "", headStyle.Render(fmt.Sprintf("SUBS #%d", s.Index)),
			joinNonEmpty("["+orUnd(s.Language)+"]", strings.ToUpper(s.Codec), forced, s.Title))
	}

	if len(info.Tags) > 0 {
		lines = append(lines, "", headStyle.Render("TAGS"))
		lines = append(lines, tagLines(info.Tags)...)
	}
	return lines
}

func orUnd(lang string) string {
	if lang == "" {
		return "und"
	}
	return lang
}

func tagLines(tags map[string]string) []string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = kv(k, tags[k])
	}
	return lines
}

// tracksLines is a table of every track with its selector and flags.
func tracksLines(st *fileState) []string {
	if st.meta == nil || len(st.meta.Tracks) == 0 {
		return []string{dimStyle.Render("No tracks.")}
	}
	lines := []string{dimStyle.Render(fmt.Sprintf("%-3s %-5s %-9s %-10s %-5s %-4s %-4s %s", "#", "SEL", "TYPE", "CODEC", "LANG", "DEF", "FRC", "TITLE"))}
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "-"
	}
	for _, t := range st.meta.Tracks {
		lines = append(lines, fmt.Sprintf("%-3d %-5s %-9s %-10s %-5s %-4s %-4s %s",
			t.Index, t.Selector, t.Type, t.Codec, orUnd(t.Tags["language"]), yes(t.Default), yes(t.Forced), t.Tags["title"]))
	}
	return lines
}
