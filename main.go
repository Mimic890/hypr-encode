// hypr-encode is a terminal media file inspector powered by ffprobe.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mimic890/hypr-encode/pkg/probe"
)

func main() {
	target, err := parseTarget(os.Args[1:], os.Stdout)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err == nil {
		err = run(target)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, anRed+"✖ "+safeText(err.Error())+anReset)
		os.Exit(1)
	}
}

func parseTarget(args []string, out io.Writer) (string, error) {
	flags := flag.NewFlagSet("hypr-encode", flag.ContinueOnError)
	flags.SetOutput(out)
	flags.Usage = func() {
		fmt.Fprintln(out, "Usage: hypr-encode [file|directory]")
		fmt.Fprintln(out, "Inspect one media file or all media files in a directory.")
	}
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if flags.NArg() > 1 {
		return "", fmt.Errorf("expected at most one path, got %d", flags.NArg())
	}
	if flags.NArg() == 1 {
		return flags.Arg(0), nil
	}
	return ".", nil
}

func run(target string) error {
	fi, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		info, err := probe.ScanFile(target)
		if err != nil {
			return err
		}
		printFileInfo(info)
		return nil
	}

	paths, err := mediaPaths(target)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		fmt.Printf("\n  %sNo media files found in %s%s\n\n", anGray, shortenPath(target), anReset)
		return nil
	}

	failures := 0
	tw := termWidth()
	fmt.Println()
	for i, path := range paths {
		info, err := probe.ScanFile(path)
		if err != nil {
			failures++
			fmt.Fprintf(os.Stderr, "  %s✖ %s: %s%s\n", anRed, safeText(filepath.Base(path)), safeText(err.Error()), anReset)
			continue
		}
		printFileCard(info, tw, i+1)
		fmt.Println()
	}
	if failures > 0 {
		return fmt.Errorf("failed to inspect %d of %d media files", failures, len(paths))
	}
	return nil
}

func mediaPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !isMediaFile(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		fi, err := os.Stat(path)
		if err == nil && fi.Mode().IsRegular() {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func isMediaFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv", ".mp4", ".mov", ".avi", ".m4v", ".webm", ".wmv", ".mpg", ".mpeg",
		".ts", ".m2ts", ".mts", ".vob", ".mxf", ".flv", ".ogv", ".3gp",
		".mp3", ".flac", ".aac", ".opus", ".ogg", ".oga", ".wav", ".m4a",
		".aiff", ".aif", ".ape", ".wma", ".alac", ".ac3", ".dts",
		".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".tif", ".tiff",
		".avif", ".heic", ".heif":
		return true
	}
	return false
}
