# hypr-encode

Terminal media file inspector powered by [ffprobe](https://ffmpeg.org/ffprobe.html).

Displays detailed, colour-coded information about video, audio, and image files — codecs, HDR format, language tracks, subtitles, metadata tags, and more.

## Usage

```
hypr-encode [path]
```

| Invocation               | Behaviour                                   |
|--------------------------|---------------------------------------------|
| `hypr-encode`            | Scan current directory                      |
| `hypr-encode ./Movies`   | Scan a directory (one card per media file)  |
| `hypr-encode file.mkv`   | Detailed view for a single file             |

## What it shows

**Single file** — full breakdown:
- Filename, file size, duration, overall bitrate, chapter count
- Metadata tags (title, artist, album, encoder, EXIF for images…)
- Video track: codec, resolution, fps, frame count, bit depth, pixel format, colour range, bitrate, track size
- HDR info: HDR10 (MaxCLL / MaxFALL / peak nits), HDR10+, HLG, Dolby Vision (profile / level / compat layer)
- Audio tracks: codec, channels, sample rate, bitrate, Atmos / DTS:X badges
- Subtitle tracks: format, language, forced flag, size
- Language tags are colour-coded by language

**Directory** — compact card for every media file:
- Multiple track cards flow into columns that adapt to terminal width

## Requirements

- Go 1.21+
- `ffprobe` in `$PATH` (part of the [ffmpeg](https://ffmpeg.org/) package)

## Install

```sh
git clone https://github.com/Mimic890/hypr-encode.git
cd hypr-encode
go build -o hyprenc .
```

Move the binary somewhere on your `$PATH`, e.g. `~/.local/bin/`.

## Colours

Uses 24-bit ANSI colour codes. Set `NO_COLOR` to any non-empty value to disable all colours ([no-color.org](https://no-color.org/)):

```sh
NO_COLOR=1 hyprenc file.mkv
```

## License

MIT
