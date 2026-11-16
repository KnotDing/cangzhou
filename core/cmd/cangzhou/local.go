package main

import (
	"errors"
	"path/filepath"
	"strings"

	"cangzhou/internal/authority"
)

// selectLocalRepository resolves the chosen path once; subsequent checks require
// that the stored real path has not been redirected through a symlink.
func (a *App) selectLocalRepository(path, source string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("本地仓库路径必须是绝对路径")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("无法访问本地仓库目录")
	}
	return a.checkLocalRepository(resolved, source)
}

// checkLocalRepository requires an existing authorized directory and rejects
// overlap with the source. Recheck at each Kopia invocation: paths can change.
func (a *App) checkLocalRepository(path, source string) (string, error) {
	c, err := authority.Inspect(a.roots, path)
	if err != nil || !c.Readable || !c.Writable || !c.Searchable {
		return "", errors.New("本地仓库目录必须存在，且已授权并可读写、可访问")
	}
	if filepath.Clean(path) != c.Path {
		return "", errors.New("本地仓库路径已变化，请重新选择真实目录")
	}
	if source != "" {
		src, err := filepath.EvalSymlinks(source)
		if err != nil {
			return "", errors.New("无法检查备份源目录")
		}
		contains := func(parent, child string) bool {
			rel, err := filepath.Rel(parent, child)
			return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
		}
		if contains(src, c.Path) || contains(c.Path, src) {
			return "", errors.New("本地仓库与备份源目录不能相同或互相包含")
		}
	}
	return c.Path, nil
}
