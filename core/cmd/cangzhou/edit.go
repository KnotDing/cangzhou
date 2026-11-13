package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) edit(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "请求方法不支持", 405)
		return
	}
	a.mu.Lock()
	_, j, ok := a.find(r.URL.Query().Get("job"))
	busy := a.busy
	a.mu.Unlock()
	if !ok {
		http.Error(w, "找不到该任务", 404)
		return
	}
	if busy {
		http.Redirect(w, r, base+"/task?job="+url.QueryEscape(j.ID), 303)
		return
	}
	a.mu.Lock()
	davs := make([]DAVConfig, len(a.davs))
	for i, d := range a.davs {
		davs[i] = cleanDAV(d)
	}
	a.mu.Unlock()
	data := struct {
		DAVs  []DAVConfig
		Job   Config
		CSRF  string
		Roots []string
	}{davs, sanitized(j), a.csrf, a.roots}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = editPage.Execute(w, data)
}
func (a *App) reconnect(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	id := r.PostForm.Get("job")
	mode := r.PostForm.Get("mode")
	if mode != "create" && mode != "connect" {
		http.Error(w, "请选择新建或连接已有仓库", 400)
		return
	}
	previous, e := a.acquire("更新连接", id)
	if e != nil {
		if errors.Is(e, errBusy) {
			http.Redirect(w, r, base+"/task?job="+url.QueryEscape(id), 303)
		} else {
			http.Error(w, e.Error(), 400)
		}
		return
	}
	davID := r.PostForm.Get("dav_id")
	a.mu.Lock()
	_, dav, ok := a.findDAV(davID)
	a.mu.Unlock()
	if !ok {
		a.release("")
		http.Error(w, "请选择已保存的 WebDAV 连接", 400)
		return
	}
	u, e := url.Parse(dav.URL)
	if e != nil {
		a.release("")
		http.Error(w, "WebDAV 地址无效", 400)
		return
	}
	targetPath := strings.Trim(r.PostForm.Get("target_path"), "/")
	target, e := repositoryURL(u, targetPath)
	if e != nil {
		a.release("")
		http.Error(w, e.Error(), 400)
		return
	}
	cfg := previous
	cfg.DAVID = davID
	cfg.TargetPath = targetPath
	cfg.ConnectionID = random(8)
	cfg.URL = target
	cfg.User = dav.User
	cfg.WebDAVPassword = dav.Password
	if p := r.PostForm.Get("repo_password"); p != "" {
		cfg.RepoPassword = p
	}
	if len(cfg.RepoPassword) < 12 {
		a.release("")
		http.Error(w, "仓库密码至少 12 位", 400)
		return
	}
	cfg.InsecureTLS = dav.InsecureTLS
	cfg.ProxyUser = ""
	cfg.ProxyPassword = ""
	cfg.ProxyPort = 0
	cfg.RepoSizeKnown = false
	cfg.RepoSizeBytes = 0
	cfg.RepoSizeAt = time.Time{}
	cfg.RepoSizeAttemptAt = time.Time{}
	cfg.RepoSizeError = ""
	a.mu.Lock()
	a.setMessageLocked("task", id, "正在验证新目标，完成前原连接保持不变。")
	a.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		path, e := a.repositoryConfigPath(cfg)
		if e != nil {
			a.release("更新失败：" + e.Error())
			return
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			a.release("更新失败：" + e.Error())
			return
		}
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
		defer cancel()
		proxy, e := a.connectRemote(ctx, &cfg, dav, mode)
		if e != nil {
			if proxy != nil {
				proxy.Close()
			}
			if ctx.Err() == context.DeadlineExceeded {
				e = errors.New("连接超时（5 分钟）")
			}
			a.release("更新失败，原连接仍保留：" + e.Error())
			return
		}
		a.mu.Lock()
		i, old, ok := a.find(id)
		if !ok {
			a.mu.Unlock()
			if proxy != nil {
				proxy.Close()
			}
			a.release("任务不存在")
			return
		}
		// Pause/resume is allowed while connection verification runs.
		// Preserve changes made since the worker captured the task.
		cfg.Paused = old.Paused
		cfg.Next = old.Next
		a.jobs[i] = cfg
		e = a.save()
		if e != nil {
			a.jobs[i] = old
			a.mu.Unlock()
			if proxy != nil {
				proxy.Close()
			}
			a.release("保存新连接失败，原连接仍保留：" + e.Error())
			return
		}
		oldProxy := a.proxies[id]
		if proxy != nil {
			a.proxies[id] = proxy
		} else {
			delete(a.proxies, id)
		}
		delete(a.proxyErrors, id)
		a.mu.Unlock()
		if oldProxy != nil {
			oldProxy.Close()
		}
		a.release("任务「" + cfg.Name + "」目标连接已更新。")
	}()
	http.Redirect(w, r, base+"/task?job="+url.QueryEscape(id), 303)
}
