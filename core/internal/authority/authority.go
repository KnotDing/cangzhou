// Package authority validates the current list of fnOS authorized paths.
// It is an application-level guard, not a substitute for kernel permissions.
package authority

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var ErrNotAuthorized = errors.New("path outside authorized roots")

// Parse uses only the paths supplied by fnOS. No default volumes are added.
func Parse(value string) []string {
	var out []string
	for _, p := range strings.Split(value, ":") {
		if p != "" && filepath.IsAbs(p) {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}

type Check struct {
	Path       string
	Root       string
	Readable   bool
	Writable   bool
	Searchable bool
}

// Inspect requires an existing directory, follows symlinks, checks containment
// by path components and reports access with the real user ID. Callers must
// run without setuid and re-check at use; the filesystem can change meanwhile.
func Inspect(roots []string, path string) (Check, error) {
	var result Check
	if !filepath.IsAbs(path) {
		return result, errors.New("path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return result, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return result, err
	}
	if !info.IsDir() {
		return result, errors.New("path must be a directory")
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		rr, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rr, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		result = Check{Path: resolved, Root: rr, Readable: syscall.Access(resolved, 4) == nil, Writable: syscall.Access(resolved, 2) == nil, Searchable: syscall.Access(resolved, 1) == nil}
		if os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() {
			return Check{}, fmt.Errorf("real and effective credentials differ")
		}
		return result, nil
	}
	return result, ErrNotAuthorized
}
