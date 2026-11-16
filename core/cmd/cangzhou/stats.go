package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

// blobBytes counts the backend-reported length of every blob, including
// metadata. This is repository-wide, not attributable to an individual source.
func blobBytes(r io.Reader) (int64, error) {
	d := json.NewDecoder(r)
	t, e := d.Token()
	if e != nil || t != json.Delim('[') {
		return 0, errors.New("仓库对象列表格式无效")
	}
	var total int64
	for d.More() {
		var b struct {
			Length *int64 `json:"length"`
		}
		if e = d.Decode(&b); e != nil {
			return 0, e
		}
		if b.Length == nil || *b.Length < 0 || total > math.MaxInt64-*b.Length {
			return 0, errors.New("仓库对象大小无效")
		}
		total += *b.Length
	}
	if t, e = d.Token(); e != nil || t != json.Delim(']') {
		return 0, errors.New("仓库对象列表不完整")
	}
	if _, e = d.Token(); e != io.EOF {
		return 0, errors.New("仓库对象列表包含多余数据")
	}
	return total, nil
}
func (a *App) repositoryBytes(ctx context.Context, cfg Config) (int64, error) {
	cmd, dir, e := a.command(ctx, cfg, "blob", "list", "--json")
	if e != nil {
		return 0, e
	}
	out, e := os.CreateTemp(dir, ".blob-list-*")
	if e != nil {
		return 0, e
	}
	defer os.Remove(out.Name())
	defer out.Close()
	errFile, e := os.CreateTemp(dir, ".blob-error-*")
	if e != nil {
		return 0, e
	}
	defer os.Remove(errFile.Name())
	defer errFile.Close()
	cmd.Stdout = out
	cmd.Stderr = errFile
	if e = cmd.Run(); e != nil {
		errText, _ := readCommandOutput(errFile.Name(), 4096)
		s := string(errText)
		for _, secret := range []string{cfg.RepoPassword, cfg.WebDAVPassword, cfg.ProxyPassword} {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "[已隐藏]")
			}
		}
		return 0, fmt.Errorf("统计仓库失败: %w: %s", e, s)
	}
	// Use a separate file descriptor: a descendant could retain the writer.
	reader, e := os.Open(out.Name())
	if e != nil {
		return 0, e
	}
	defer reader.Close()
	return blobBytes(reader)
}
func (a *App) startSizeCheck(id string, automatic bool) error {
	cfg, e := a.acquire("统计仓库大小", id)
	if e != nil {
		return e
	}
	a.mu.Lock()
	i, old, ok := a.find(id)
	if !ok {
		a.mu.Unlock()
		a.release("")
		return errors.New("找不到任务")
	}
	if automatic && (old.RepoSizeKnown || (!old.RepoSizeAttemptAt.IsZero() && time.Since(old.RepoSizeAttemptAt) < time.Hour)) {
		a.mu.Unlock()
		a.release("")
		return nil
	}
	a.jobs[i].RepoSizeAttemptAt = time.Now()
	a.jobs[i].RepoSizeError = ""
	if e = a.save(); e != nil {
		a.jobs[i] = old
		a.mu.Unlock()
		a.release("无法保存统计状态：" + e.Error())
		return e
	}
	a.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
		defer cancel()
		n, err := a.repositoryBytes(ctx, cfg)
		a.mu.Lock()
		if i, _, ok := a.find(id); ok {
			if err != nil {
				msg := err.Error()
				if len(msg) > 1000 {
					msg = msg[:1000] + "…"
				}
				a.jobs[i].RepoSizeError = msg
			} else {
				a.jobs[i].RepoSizeBytes = n
				a.jobs[i].RepoSizeKnown = true
				a.jobs[i].RepoSizeAt = time.Now()
				a.jobs[i].RepoSizeError = ""
			}
			if se := a.save(); se != nil {
				a.setMessageLocked("task", id, "保存仓库统计失败："+se.Error())
			}
		}
		a.mu.Unlock()
		a.release("")
	}()
	return nil
}
func (a *App) measure(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	id := r.PostForm.Get("job")
	if e := a.startSizeCheck(id, false); e != nil && !errors.Is(e, errBusy) {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, base+"/task?job="+id, 303)
}
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.2f %s", v, units[i])
}

// refreshSizeAfterBackup runs under the existing backup activity, so another
// operation cannot race between backup completion and the size measurement.
func (a *App) refreshSizeAfterBackup(cfg Config) error {
	a.mu.Lock()
	a.progress = nil
	a.activity = "统计仓库大小：" + cfg.Name
	if i, _, ok := a.find(cfg.ID); ok {
		a.jobs[i].RepoSizeAttemptAt = time.Now()
		if e := a.save(); e != nil {
			a.mu.Unlock()
			return fmt.Errorf("保存统计状态失败: %w", e)
		}
	}
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()
	n, err := a.repositoryBytes(ctx, cfg)
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, _, ok := a.find(cfg.ID); ok {
		if err != nil {
			msg := err.Error()
			if len(msg) > 1000 {
				msg = msg[:1000] + "…"
			}
			a.jobs[i].RepoSizeError = msg
		} else {
			a.jobs[i].RepoSizeBytes = n
			a.jobs[i].RepoSizeKnown = true
			a.jobs[i].RepoSizeAt = time.Now()
			a.jobs[i].RepoSizeError = ""
			if count := len(a.jobs[i].History); count > 0 {
				r := &a.jobs[i].History[count-1]
				if r.Status == "success" {
					r.RepoSizeBytes, r.RepoSizeKnown, r.RepoSizeAt = n, true, a.jobs[i].RepoSizeAt
				}
			}
		}
		if e := a.save(); e != nil {
			return fmt.Errorf("保存仓库统计失败: %w", e)
		}
	}
	return err
}
