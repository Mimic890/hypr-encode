# hypr-encode

Fast terminal media inspection powered by [ffprobe](https://ffmpeg.org/ffprobe.html).

It shows codecs, stream sizes, frame rate, HDR metadata, audio and subtitle tracks, language tags, and common media metadata without changing the inspected files.

## Install

Requirements: Go 1.21+ and `ffprobe` from [FFmpeg](https://ffmpeg.org/).

```sh
go install github.com/Mimic890/hypr-encode@latest
```

Or build the repository locally:

```sh
go build -o hypr-encode .
```

## Usage

```text
hypr-encode [file|directory]
```

| Invocation | Behavior |
| --- | --- |
| `hypr-encode` | Inspect media files in the current directory |
| `hypr-encode ./Movies` | Inspect media files in one directory |
| `hypr-encode file.mkv` | Show a detailed view of one file |

Directory scans are non-recursive, skip hidden entries and subdirectories, and discover media by file extension. Every file is attempted; any probe failure makes the command exit non-zero after the remaining files are shown.

Each `ffprobe` process has a 30-second timeout. Paths are passed as explicit local inputs, and filenames and metadata are sanitized before terminal output.

## Output

- File size, duration, overall bitrate, chapters, and common metadata
- Video codec, resolution, average frame rate, frame count, bit depth, pixel format, color range, bitrate, and stream size
- HDR10, HDR10+, HLG, and Dolby Vision data when reported by `ffprobe`
- Audio codec, channels, sample rate, bitrate, stream size, and detectable Atmos or DTS:X profiles
- Subtitle format, language, forced flag, title, and stream size
- Still-image dimensions, pixel format, bit depth, and common EXIF tags

Colors are enabled only for terminal output. Set [`NO_COLOR`](https://no-color.org/) to any non-empty value to disable them explicitly.

## Development

```sh
gofmt -w .
go test ./...
go vet ./...
go build ./...
```

## License

MIT
