package probe

import (
	"math"
	"strings"
	"testing"
)

func TestParseFFprobe(t *testing.T) {
	raw := []byte(`{
		"format":{"duration":"10.25","size":"9999","bit_rate":"800000","tags":{"title":"Demo"}},
		"chapters":[{}],
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc","avg_frame_rate":"24000/1001","r_frame_rate":"60/1","pix_fmt":"p010le","tags":{"NUMBER_OF_BYTES-eng":"1200"},"color_primaries":"bt2020","color_transfer":"smpte2084","side_data_list":[{"side_data_type":"Content light level metadata","max_content":1000,"max_average":400}]},
			{"index":1,"codec_type":"audio","codec_name":"aac","sample_rate":"48000","tags":{"BPS":"640000","NUMBER_OF_BYTES":"1100"}},
			{"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"NUMBER_OF_BYTES":"100"}}
		]
	}`)

	info, err := parseFFprobe("/tmp/demo.mkv", 1000, raw)
	if err != nil {
		t.Fatal(err)
	}
	if info.FileSize != 1000 || info.DurationMs != 10250 || info.BitrateBps != 800000 || info.Chapters != 1 {
		t.Fatalf("unexpected format data: %+v", info)
	}
	if len(info.Video) != 1 || math.Abs(info.Video[0].FPS-23.976) > 0.001 || info.Video[0].BitDepth != 10 {
		t.Fatalf("unexpected video data: %+v", info.Video)
	}
	if !info.Video[0].HDR.IsHDR10 || info.Video[0].HDR.MaxCLL != 1000 || info.Video[0].SizeBytes != 0 {
		t.Fatalf("unexpected HDR or video size: %+v", info.Video[0])
	}
	if len(info.Audio) != 1 || info.Audio[0].BitrateBps != 640000 || info.Audio[0].SizeBytes != 0 {
		t.Fatalf("unexpected audio data: %+v", info.Audio)
	}
	if len(info.Subtitle) != 1 || info.Subtitle[0].SizeBytes != 100 {
		t.Fatalf("unexpected subtitle data: %+v", info.Subtitle)
	}
}

func TestTagInt64PrefixIsDeterministic(t *testing.T) {
	tags := map[string]string{"BPS-eng": "2", "bps": "3", "BPS": "4"}
	for i := 0; i < 100; i++ {
		if got := tagInt64Prefix(tags, "BPS"); got != 4 {
			t.Fatalf("exact key must win, got %d", got)
		}
	}
}

func TestBitDepthFromPixFmt(t *testing.T) {
	tests := map[string]int{
		"yuv420p": 8, "yuv420p9le": 9, "p010le": 10, "gbrp12be": 12,
		"gray16le": 16, "rgb48be": 16, "rgb565le": 0, "unknown": 0,
	}
	for pixFmt, want := range tests {
		if got := BitDepthFromPixFmt(pixFmt); got != want {
			t.Errorf("BitDepthFromPixFmt(%q) = %d, want %d", pixFmt, got, want)
		}
	}
}

func TestParseFPS(t *testing.T) {
	if got := parseFPS("24"); got != 24 {
		t.Fatalf("parseFPS(plain) = %v, want 24", got)
	}
	if got := parseFPS("24000/1001"); math.Abs(got-23.976) > 0.001 {
		t.Fatalf("parseFPS() = %v", got)
	}
	if got := parseFPS("1/0"); got != 0 {
		t.Fatalf("parseFPS(zero denominator) = %v", got)
	}
}

func TestParseFFprobeRejectsInvalidJSON(t *testing.T) {
	_, err := parseFFprobe("bad.mkv", 1, []byte("not json"))
	if err == nil || !strings.Contains(err.Error(), "parse ffprobe output") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestScanFileRejectsDirectory(t *testing.T) {
	_, err := ScanFile(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("unexpected error: %v", err)
	}
}
