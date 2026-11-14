package main

import (
	"regexp"
	"sort"
	"strings"
)

type browseDirectory struct{ Name, Path string }
type browseEntry struct {
	Name, Path string
	IsDir      bool
}

// Kopia 0.23.1's long listing ends in a padded object ID and an unescaped name.
// Only unambiguous single-line directory names are made into navigation links.
var browseEntryLine = regexp.MustCompile(`^([d-])\S*\s+\d+\s+\S+\s+\S+\s+\S+\s+([a-z0-9]{20,100}) ([^\r\n]+)$`)

func snapshotBrowsePath(path string) (string, bool) {
	if len(path) > 4096 || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\\x00\r\n") {
		return "", false
	}
	if path == "" {
		return "", true
	}
	for _, r := range path {
		if r < 32 || r == 127 {
			return "", false
		}
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	return path, true
}

func snapshotBrowseParent(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return ""
}

func snapshotBrowseDirectories(path, output string) []browseDirectory {
	var directories []browseDirectory
	for _, entry := range snapshotBrowseEntries(path, output) {
		if entry.IsDir {
			directories = append(directories, browseDirectory{entry.Name, entry.Path})
		}
	}
	return directories
}

func snapshotBrowseEntries(path, output string) []browseEntry {
	var entries []browseEntry
	for _, line := range strings.Split(output, "\n") {
		m := browseEntryLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[3]
		padding := 34 - len(m[2])
		if padding > 0 {
			if len(name) < padding || name[:padding] != strings.Repeat(" ", padding) {
				continue
			}
			name = name[padding:]
		}
		isDir := m[1] == "d"
		if isDir {
			if !strings.HasSuffix(name, "/") {
				continue
			}
			name = strings.TrimSuffix(name, "/")
		}
		if strings.Contains(name, "/") {
			continue
		}
		child := name
		if path != "" {
			child = path + "/" + child
		}
		if _, ok := snapshotBrowsePath(child); ok {
			entries = append(entries, browseEntry{name, child, isDir})
		}
	}
	return entries
}

func selectedSnapshotPaths(paths []string) ([]string, bool) {
	if len(paths) == 0 || len(paths) > 100 {
		return nil, false
	}
	unique := map[string]bool{}
	for _, path := range paths {
		if _, ok := snapshotBrowsePath(path); !ok || path == "" {
			return nil, false
		}
		unique[path] = true
	}
	paths = paths[:0]
	for path := range unique {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		covered := false
		for parent := snapshotBrowseParent(path); parent != ""; parent = snapshotBrowseParent(parent) {
			if unique[parent] {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		result = append(result, path)
	}
	return result, true
}
