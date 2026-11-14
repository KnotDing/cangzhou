package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// validateSelection only accepts regular files or directories actually present
// in the selected snapshot. Listings and paths remain bounded.
func (a *App) validateSelection(ctx context.Context, cfg Config, id string, requested []string) ([]string, error) {
	paths, ok := selectedSnapshotPaths(requested)
	if !ok {
		return nil, errors.New("请选择 1 至 100 个有效文件或目录")
	}
	listings := make(map[string]map[string]bool)
	for _, path := range paths {
		parent := snapshotBrowseParent(path)
		entries, ok := listings[parent]
		if !ok {
			objectPath := id
			if parent != "" {
				objectPath += "/" + parent
			}
			out, err := a.run(ctx, cfg, "list", "--long", "--no-error-summary", "--", objectPath)
			if err != nil && !errors.Is(err, errOutputTooLarge) {
				return nil, err
			}
			text := string(out)
			if errors.Is(err, errOutputTooLarge) {
				if i := strings.LastIndexByte(text, '\n'); i >= 0 {
					text = text[:i+1]
				} else {
					text = ""
				}
			}
			entries = make(map[string]bool)
			for _, entry := range snapshotBrowseEntries(parent, text) {
				entries[entry.Path] = true
			}
			listings[parent] = entries
		}
		if !entries[path] {
			return nil, fmt.Errorf("所选项目不存在或不在可显示的目录列表中：%s", path)
		}
	}
	return paths, nil
}

func (a *App) restoreSelected(ctx context.Context, cfg Config, id, destination string, paths []string) error {
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	for _, path := range paths {
		target := filepath.Join(destination, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if _, err := a.run(ctx, cfg, "restore", "--no-overwrite-files", "--no-overwrite-directories", "--no-overwrite-symlinks", "--", id+"/"+path, target); err != nil {
			return err
		}
	}
	return nil
}
