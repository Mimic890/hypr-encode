package main

import (
	"strings"
	"testing"

	"github.com/Mimic890/hypr-encode/pkg/probe"
)

func TestSafeTextRemovesTerminalControls(t *testing.T) {
	got := safeText("name\x1b]52;c;payload\a\nnext\t\u202e")
	for _, forbidden := range []string{"\x1b", "\a", "\n", "\t", "\u202e"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("unsafe control %q remains in %q", forbidden, got)
		}
	}
}

func TestImageClassification(t *testing.T) {
	tests := []struct {
		name string
		info probe.MediaInfo
		want bool
	}{
		{"jpeg", probe.MediaInfo{FileName: "photo.jpg", Video: []probe.VideoStream{{Codec: "mjpeg", Width: 100, Height: 100}}}, true},
		{"avif", probe.MediaInfo{FileName: "photo.avif", Video: []probe.VideoStream{{Codec: "av1", Width: 100, Height: 100}}}, true},
		{"mjpeg video", probe.MediaInfo{FileName: "movie.avi", Video: []probe.VideoStream{{Codec: "mjpeg", Width: 100, Height: 100}}}, false},
		{"animated image", probe.MediaInfo{FileName: "movie.webp", Video: []probe.VideoStream{{Codec: "webp", NbFrames: 2}}}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isImageFile(&test.info); got != test.want {
				t.Fatalf("isImageFile() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestTrackCardsFitAvailableWidth(t *testing.T) {
	cards := []trackCard{{title: "#1", lines: []string{strings.Repeat("界", 50)}}}
	for _, line := range trackCardRowLines(cards, 40) {
		if width := visLen(line); width > 40 {
			t.Fatalf("line width = %d, want <= 40: %q", width, line)
		}
	}
}

func TestVisibleWidth(t *testing.T) {
	if got := visLen("A界"); got != 3 {
		t.Fatalf("visLen() = %d, want 3", got)
	}
}

func TestResolveMetaIsCaseInsensitive(t *testing.T) {
	rows := resolveMeta(map[string]string{"TiTLe": "hello\nworld"}, audioMetaKeys)
	if len(rows) != 1 || rows[0][0] != "title" || rows[0][1] != "hello?world" {
		t.Fatalf("resolveMeta() = %v", rows)
	}
}
