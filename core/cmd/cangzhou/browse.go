package main

import (
	"regexp"
	"strings"
)

type browseDirectory struct{ Name, Path string }

// Kopia 0.23.1's long listing ends in a padded object ID and an unescaped name.
// Only unambiguous single-line directory names are made into navigation links.
var browseDirectoryLine = regexp.MustCompile(`^d\S*\s+\d+\s+\S+\s+\S+\s+\S+\s+[a-z0-9]{20,100} {1,2}([^\r\n]+)/$`)

func snapshotBrowsePath(path string) (string, bool) {
	if len(path) > 4096 || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\\x00\r\n") {
		return "", false
	}
	if path == "" {
		return "", true
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
	for _, line := range strings.Split(output, "\n") {
		m := browseDirectoryLine.FindStringSubmatch(line)
		if m == nil || strings.Contains(m[1], "/") {
			continue
		}
		child := m[1]
		if path != "" {
			child = path + "/" + child
		}
		if _, ok := snapshotBrowsePath(child); ok {
			directories = append(directories, browseDirectory{m[1], child})
		}
	}
	return directories
}
