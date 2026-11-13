package main

import (
	"archive/zip"
	"cangzhou/internal/authority"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxArchiveSize int64 = 64 << 30
const maxExpandedSize uint64 = 256 << 30

func authorizedFile(roots []string, file string) (string, error) {
	if !filepath.IsAbs(file) {
		return "", errors.New("请选择绝对路径")
	}
	resolved, e := filepath.EvalSymlinks(file)
	if e != nil {
		return "", e
	}
	c, e := authority.Inspect(roots, filepath.Dir(resolved))
	if e != nil || !c.Readable || !c.Searchable {
		return "", errors.New("文件不在已授权且可读取的目录中")
	}
	st, e := os.Stat(resolved)
	if e != nil || !st.Mode().IsRegular() {
		return "", errors.New("请选择普通文件")
	}
	return resolved, nil
}
func unzipRepository(name, dest string) error {
	z, e := zip.OpenReader(name)
	if e != nil {
		return fmt.Errorf("无法读取 ZIP：%w", e)
	}
	defer z.Close()
	if len(z.File) > 1000000 {
		return errors.New("ZIP 文件数量超过限制")
	}
	var total uint64
	for _, f := range z.File {
		n := f.Name
		if n == "" || strings.Contains(n, "\\") || strings.HasPrefix(n, "/") || path.Clean(n) == ".." || strings.HasPrefix(path.Clean(n), "../") || f.Mode()&os.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
			return errors.New("ZIP 包含不安全的路径或特殊文件")
		}
		if f.UncompressedSize64 > maxExpandedSize-total {
			return errors.New("ZIP 解压后大小超过限制")
		}
		total += f.UncompressedSize64
		target := filepath.Join(dest, filepath.FromSlash(n))
		rel, e := filepath.Rel(dest, target)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return errors.New("ZIP 路径越界")
		}
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(target, 0700); e != nil {
				return e
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		in, e := f.Open()
		if e != nil {
			out.Close()
			return e
		}
		written, copyErr := io.Copy(out, io.LimitReader(in, int64(f.UncompressedSize64)+1))
		closeErr := out.Close()
		in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if uint64(written) != f.UncompressedSize64 {
			return errors.New("ZIP 文件大小不符")
		}
	}
	return nil
}
func locateRepository(root string) (string, error) {
	entries, e := os.ReadDir(root)
	if e != nil {
		return "", e
	}
	for _, x := range entries {
		if !x.IsDir() && (x.Name() == "kopia.repository" || x.Name() == "kopia.repository.f") {
			return root, nil
		}
	}
	var dir string
	for _, x := range entries {
		if x.IsDir() {
			if dir != "" {
				return "", errors.New("ZIP 中有多个顶层目录，请只压缩一个仓库")
			}
			dir = x.Name()
		} else if x.Name() != ".DS_Store" {
			return "", errors.New("ZIP 根目录没有 Kopia 仓库")
		}
	}
	if dir != "" {
		p := filepath.Join(root, dir)
		for _, n := range []string{"kopia.repository", "kopia.repository.f"} {
			if st, e := os.Stat(filepath.Join(p, n)); e == nil && st.Mode().IsRegular() {
				return p, nil
			}
		}
	}
	return "", errors.New("未找到 Kopia 仓库文件")
}

// A local repository must not contain symlinks that let the backup engine
// traverse outside the directory selected in the authorized file browser.
func validateLocalRepository(root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("仓库包含符号链接，请先复制为不含符号链接的仓库")
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return errors.New("仓库包含特殊文件")
		}
		return nil
	})
}
