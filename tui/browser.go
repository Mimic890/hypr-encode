package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mimic890/hypr-encode/pkg/probe"
)

type entry struct {
	name string
	dir  bool
}

// browser lists the subdirectories and media files of one directory.
type browser struct {
	dir     string
	entries []entry
	cursor  int
	marked  map[string]bool // absolute paths queued for conversion
}

func (b *browser) load(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	list, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var dirs, files []entry
	for _, e := range list {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		fi, err := os.Stat(filepath.Join(dir, name)) // follows symlinks
		switch {
		case err != nil:
		case fi.IsDir():
			dirs = append(dirs, entry{name, true})
		case fi.Mode().IsRegular() && probe.IsMediaFile(name):
			files = append(files, entry{name, false})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].name < dirs[j].name })
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	prev := filepath.Base(b.dir)
	cameFromChild := b.dir != "" && filepath.Dir(b.dir) == dir
	b.dir, b.cursor = dir, 0
	b.entries = append([]entry{{"..", true}}, append(dirs, files...)...)
	if b.marked == nil {
		b.marked = map[string]bool{}
	}
	for i, e := range b.entries {
		if cameFromChild && e.name == prev {
			b.cursor = i // going up keeps the cursor on the directory we left
		}
	}
	return nil
}

// current returns the absolute path under the cursor.
func (b browser) current() (path string, isDir bool) {
	if len(b.entries) == 0 {
		return "", false
	}
	e := b.entries[b.cursor]
	if e.name == ".." {
		return filepath.Dir(b.dir), true
	}
	return filepath.Join(b.dir, e.name), e.dir
}

func (b *browser) move(delta int) {
	b.cursor = min(max(b.cursor+delta, 0), len(b.entries)-1)
}

func (b *browser) toggleMark() {
	if path, isDir := b.current(); !isDir {
		if b.marked[path] {
			delete(b.marked, path)
		} else {
			b.marked[path] = true
		}
		b.move(1)
	}
}

// markedPaths returns the marked files in name order.
func (b browser) markedPaths() []string {
	paths := make([]string, 0, len(b.marked))
	for p := range b.marked {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func (b browser) lines(w, h int) []string {
	lines := []string{dimStyle.Render(shorten(b.dir, w))}
	var list []string
	for i, e := range b.entries {
		mark := "  "
		if !e.dir && b.marked[filepath.Join(b.dir, e.name)] {
			mark = okStyle.Render("● ")
		}
		name := e.name
		if e.dir {
			name = headStyle.Render(name + "/")
		}
		line := fitLine(mark+name, w)
		if i == b.cursor {
			line = cursorSty.Render(line)
		}
		list = append(list, line)
	}
	return append(lines, window(list, b.cursor, h-1)...)
}

// shorten keeps the end of a long path, which holds the useful part.
func shorten(path string, w int) string {
	if r := []rune(path); len(r) > w && w > 1 {
		return "…" + string(r[len(r)-w+1:])
	}
	return path
}
