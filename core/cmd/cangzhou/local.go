package main

import (
	"errors"
	"net/url"
	"os"
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

// localConnectionTarget validates a relative path without creating it.
func localConnectionTarget(d DAVConfig, relative string) (string, error) {
	u, err := url.Parse(d.URL)
	if err != nil || u.Scheme != "file" || u.Host != "" || !filepath.IsAbs(u.Path) || filepath.Clean(u.Path) != u.Path || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("本地存储位置无效")
	}
	target, err := repositoryURL(u, relative)
	if err != nil {
		return "", err
	}
	result, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	return result.Path, nil
}

// prepareLocalTarget checks every existing component before creating a child;
// existing repositories are never created. The usual filesystem race remains.
func (a *App) prepareLocalTarget(d DAVConfig, cfg Config, mode string) error {
	base, err := localConnectionTarget(d, "")
	if err != nil {
		return err
	}
	if _, err = a.checkLocalRepository(base, ""); err != nil {
		return err
	}
	target, err := localConnectionTarget(d, cfg.TargetPath)
	if err != nil || target != cfg.LocalPath {
		return errors.New("本地仓库路径无效")
	}
	src, err := filepath.EvalSymlinks(cfg.Source)
	if err != nil {
		return errors.New("无法检查备份源目录")
	}
	overlap := func(parent, child string) bool {
		rel, e := filepath.Rel(parent, child)
		return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	if overlap(src, target) || overlap(target, src) {
		return errors.New("本地仓库与备份源目录不能相同或互相包含")
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return err
	}
	current := base
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			info, e := os.Lstat(current)
			if errors.Is(e, os.ErrNotExist) && mode == "create" {
				if _, e = a.checkLocalRepository(filepath.Dir(current), ""); e != nil {
					return e
				}
				e = os.Mkdir(current, 0700)
				if e == nil {
					info, e = os.Lstat(current)
				}
			}
			if e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return errors.New("仓库子目录不能包含符号链接或非目录文件")
			}
			if _, e = a.checkLocalRepository(current, ""); e != nil {
				return e
			}
		}
	}
	_, err = a.checkLocalRepository(target, cfg.Source)
	return err
}
