# hypr-encode

Terminal media toolkit powered by [FFmpeg](https://ffmpeg.org/).

It shows codecs, stream sizes, frame rate, HDR metadata, audio and subtitle tracks, language tags, and common media metadata without changing the inspected files.

## Install

Requirements: Go 1.21+ and `ffprobe` from [FFmpeg](https://ffmpeg.org/). Optional tools are listed by `hypr-encode doctor`.

```sh
go install github.com/Mimic890/hypr-encode@latest
```

Or build the repository locally:

```sh
go build -o hypr-encode .
```

## Usage

```text
hypr-encode <command> [flags] [args]
```

| Command | Purpose |
| --- | --- |
| `info [path...]` | Show media information (default command) |
| `doctor` | Check external tools and their versions |

`hypr-encode <path...>` is shorthand for `hypr-encode info <path...>`. To inspect a file literally named like a command, use `hypr-encode info ./info`.

Flags may appear before or after positional arguments; `--` ends flag parsing. `-h` prints a command's flags. The CLI never asks questions interactively.

### info

```text
hypr-encode info [--json] [file|directory...]
```

| Invocation | Behavior |
| --- | --- |
| `hypr-encode` | Inspect media files in the current directory |
| `hypr-encode ./Movies` | Inspect media files in one directory |
| `hypr-encode file.mkv` | Show a detailed view of one file |
| `hypr-encode a.mkv b.mp4 ./Music` | Inspect several files and directories |

Directory scans are non-recursive, skip hidden entries and subdirectories, and discover media by file extension. Every file is attempted; failures are reported after the rest are shown (see exit codes).

Each `ffprobe` process has a 30-second timeout. Paths are passed as explicit local inputs, and filenames and metadata are sanitized before terminal output.

Output covers:

- File size, duration, overall bitrate, chapters, and common metadata
- Video codec, resolution, average frame rate, frame count, bit depth, pixel format, color range, bitrate, and stream size
- HDR10, HDR10+, HLG, and Dolby Vision data when reported by `ffprobe`
- Audio codec, channels, sample rate, bitrate, stream size, and detectable Atmos or DTS:X profiles
- Subtitle format, language, forced flag, title, and stream size
- Still-image dimensions, pixel format, bit depth, and common EXIF tags

### doctor

```text
hypr-encode doctor [--json]
```

Checks `ffprobe`, `ffmpeg`, `mkvpropedit` and `exiftool`: whether each is in `PATH` and which version it reports. Exits with code 4 if any of them is missing. Commands that do not need a missing tool keep working.

## Terminal output

Colors and box frames are drawn only when stdout is a terminal. Piped output is plain text. Set [`NO_COLOR`](https://no-color.org/) to any non-empty value to disable colors in a terminal.

Data goes to stdout; errors and logs go to stderr.

## JSON output

Every command accepts `--json`. Documents are printed to stdout and always carry `schemaVersion`. The version changes only on breaking changes; new fields may be added without a bump.

`info --json`:

```json
{
  "schemaVersion": 1,
  "files": [
    {
      "filePath": "/media/demo.mkv",
      "fileName": "demo.mkv",
      "fileSize": 1048576,
      "durationMs": 10250,
      "format": "matroska,webm",
      "formatLong": "Matroska / WebM",
      "bitrateBps": 818400,
      "chapters": 2,
      "tags": { "title": "Demo" },
      "video": [ { "index": 0, "codec": "hevc", "width": 3840, "height": 2160, "fps": 23.976, "bitDepth": 10, "hdr": { "isHdr10": true } } ],
      "audio": [ { "index": 1, "codec": "eac3", "channels": 6, "language": "eng", "isAtmos": true } ],
      "subtitle": [ { "index": 2, "codec": "subrip", "language": "rus", "forced": true } ]
    }
  ],
  "errors": [ { "path": "/media/broken.mp4", "error": "ffprobe failed on \"broken.mp4\": ..." } ]
}
```

The stream objects are abbreviated above; the full field list is in [`testdata/info.golden`](testdata/info.golden). `files`, `errors`, `video`, `audio` and `subtitle` are always arrays, never `null`.

`doctor --json`:

```json
{
  "schemaVersion": 1,
  "tools": [
    { "name": "ffprobe", "found": true, "path": "/usr/bin/ffprobe", "version": "7.1" },
    { "name": "mkvpropedit", "found": false, "error": "mkvpropedit not found in PATH; install MKVToolNix" }
  ]
}
```

On failure with `--json`, stderr receives one JSON line:

```json
{"schemaVersion":1,"error":{"code":"tool_missing","message":"ffprobe not found in PATH; install FFmpeg"}}
```

## Exit codes

| Code | `error.code` | Meaning |
| --- | --- | --- |
| 0 | | Success |
| 1 | `error` | The operation failed (for batches: every item failed) |
| 2 | `usage` | Invalid arguments or flags |
| 3 | `partial` | Some items of a batch failed, others succeeded |
| 4 | `tool_missing` | A required external tool is not installed |
| 130 | `interrupted` | Stopped by Ctrl+C or SIGTERM |

## Development

```sh
gofmt -w .
go vet ./...
go test ./...
go build ./...
```

Integration tests generate tiny media files with `ffmpeg -f lavfi` and are skipped when the needed tool is missing. Regenerate golden files with `go test . -update`.

## License

MIT
