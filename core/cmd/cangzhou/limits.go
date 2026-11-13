package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const maxUploadKiB = 1048576

func parseUploadLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 0 || n > maxUploadKiB {
		return 0, errors.New("上传限速必须在 0 至 1048576 KiB/s 之间")
	}
	return n, nil
}
func effectiveLimit(global, task int) int {
	if global == 0 {
		return task
	}
	if task == 0 || global < task {
		return global
	}
	return task
}
func (a *App) limitedRepositoryConfig(j Config, limit int) (string, error) {
	p, e := a.repositoryConfigPath(j)
	if e != nil {
		return "", e
	}
	data, e := os.ReadFile(p)
	if e != nil {
		return "", e
	}
	var cfg map[string]any
	if e = json.Unmarshal(data, &cfg); e != nil {
		return "", e
	}
	storage, ok := cfg["storage"].(map[string]any)
	if !ok {
		return "", errors.New("仓库连接配置不支持限速")
	}
	conf, ok := storage["config"].(map[string]any)
	if !ok {
		return "", errors.New("仓库连接配置不支持限速")
	}
	conf["maxUploadSpeedBytesPerSecond"] = limit * 1024
	throttling, ok := cfg["throttlingLimits"].(map[string]any)
	if !ok {
		throttling = map[string]any{}
	}
	throttling["maxUploadSpeedBytesPerSecond"] = limit * 1024
	cfg["throttlingLimits"] = throttling
	data, e = json.Marshal(cfg)
	if e != nil {
		return "", e
	}
	dir, e := a.jobDir(j.ID)
	if e != nil {
		return "", e
	}
	f, e := os.CreateTemp(dir, ".limited-config-")
	if e != nil {
		return "", e
	}
	name := f.Name()
	if _, e = f.Write(data); e != nil {
		f.Close()
		os.Remove(name)
		return "", e
	}
	if e = f.Close(); e != nil {
		os.Remove(name)
		return "", e
	}
	return name, nil
}
func (a *App) limitsPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "请求方法不支持", 405)
		return
	}
	a.mu.Lock()
	d := struct {
		CSRF, Message string
		Global        int
	}{a.csrf, a.visibleMessageLocked("limits", ""), a.globalUploadKiB}
	a.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = limitsSettingsPage.Execute(w, d)
}
func (a *App) saveLimits(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	n, e := parseUploadLimit(r.PostForm.Get("upload_limit_kib"))
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	a.mu.Lock()
	old := a.globalUploadKiB
	a.globalUploadKiB = n
	if e = a.save(); e != nil {
		a.globalUploadKiB = old
		a.mu.Unlock()
		http.Error(w, "保存失败", 500)
		return
	}
	a.setMessageLocked("limits", "", "全局备份上传限速已保存，下次启动的备份生效。")
	a.mu.Unlock()
	http.Redirect(w, r, base+"/limits", 303)
}
