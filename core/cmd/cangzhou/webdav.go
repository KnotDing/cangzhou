package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var validDAVID = regexp.MustCompile("^[a-f0-9]{16}$")

func (a *App) findDAV(id string) (int, DAVConfig, bool) {
	for i, d := range a.davs {
		if d.ID == id {
			return i, d, true
		}
	}
	return -1, DAVConfig{}, false
}
func cleanDAV(d DAVConfig) DAVConfig { d.Password = ""; d.PrivateKey = ""; return d }
func parseDAV(r *http.Request, old DAVConfig) (DAVConfig, error) {
	if r.PostForm.Get("protocol") == "local" {
		name := strings.TrimSpace(r.PostForm.Get("name"))
		path := strings.TrimSpace(r.PostForm.Get("local_path"))
		if name == "" || len(name) > 120 || !filepath.IsAbs(path) {
			return DAVConfig{}, errors.New("请填写名称和本地目录绝对路径")
		}
		return DAVConfig{ID: old.ID, Name: name, Protocol: "local", URL: (&url.URL{Scheme: "file", Path: filepath.Clean(path)}).String()}, nil
	}
	if r.PostForm.Get("protocol") == "sftp" {
		return parseSFTP(r, old)
	}
	d := DAVConfig{ID: old.ID, Name: strings.TrimSpace(r.PostForm.Get("name")), User: strings.TrimSpace(r.PostForm.Get("user")), Password: r.PostForm.Get("password"), InsecureTLS: r.PostForm.Get("insecure_tls") == "1"}
	if d.Password == "" {
		d.Password = old.Password
	}
	u, e := url.Parse(strings.TrimSpace(r.PostForm.Get("url")))
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return d, errors.New("请输入不含凭据和查询参数的 HTTP 或 HTTPS WebDAV 地址")
	}
	if d.InsecureTLS && u.Scheme != "https" {
		return d, errors.New("跳过证书验证仅适用于 HTTPS")
	}
	if d.Name == "" || len(d.Name) > 120 || d.User == "" || d.Password == "" {
		return d, errors.New("请输入连接名称、用户名和密码")
	}
	d.URL = u.String()
	return d, nil
}
func (a *App) webdav(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "请求方法不支持", 405)
		return
	}
	a.mu.Lock()
	list := make([]DAVConfig, len(a.davs))
	for i, d := range a.davs {
		list[i] = cleanDAV(d)
	}
	var edit DAVConfig
	if id := r.URL.Query().Get("edit"); id != "" {
		_, d, ok := a.findDAV(id)
		if !ok {
			a.mu.Unlock()
			http.Error(w, "找不到存储位置", 404)
			return
		}
		edit = cleanDAV(d)
	}
	localPath := ""
	if edit.Protocol == "local" {
		if u, err := url.Parse(edit.URL); err == nil {
			localPath = u.Path
		}
	}
	data := struct {
		Roots             []string
		LocalPath         string
		DAVs              []DAVConfig
		Edit              DAVConfig
		CSRF              string
		Busy              bool
		Activity, Message string
		Elapsed           int
		New               bool
	}{a.roots, localPath, list, edit, a.csrf, a.busy, a.activity, a.visibleMessageLocked("remote", ""), elapsed(a.startedAt), r.URL.Query().Get("new") == "1" || len(list) == 0}
	a.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = davPage.Execute(w, data)
}
func (a *App) saveDAV(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_, e := a.acquire("更新存储位置", "")
	if e != nil {
		http.Redirect(w, r, base+"/webdav", 303)
		return
	}
	id := r.PostForm.Get("id")
	var old DAVConfig
	if id != "" {
		a.mu.Lock()
		_, d, ok := a.findDAV(id)
		a.mu.Unlock()
		if !ok {
			a.release("")
			http.Error(w, "找不到存储位置", 404)
			return
		}
		old = d
	}
	d, e := parseDAV(r, old)
	if e != nil {
		a.release("")
		http.Error(w, e.Error(), 400)
		return
	}
	if d.Protocol == "local" {
		u, _ := url.Parse(d.URL)
		p, err := a.selectLocalRepository(u.Path, "")
		if err != nil {
			a.release("")
			http.Error(w, err.Error(), 400)
			return
		}
		d.URL = (&url.URL{Scheme: "file", Path: p}).String()
	}
	if id == "" {
		d.ID = random(8)
		a.mu.Lock()
		a.davs = append(a.davs, d)
		e = a.save()
		if e != nil {
			a.davs = a.davs[:len(a.davs)-1]
		}
		a.mu.Unlock()
		if e != nil {
			a.release("保存存储位置失败：" + e.Error())
		} else {
			a.release("存储位置「" + d.Name + "」已保存。")
		}
		http.Redirect(w, r, base+"/webdav", 303)
		return
	}
	changed := old.URL != d.URL || old.User != d.User || old.Password != d.Password || old.InsecureTLS != d.InsecureTLS || old.Protocol != d.Protocol || old.PrivateKey != d.PrivateKey || old.KnownHosts != d.KnownHosts
	a.mu.Lock()
	var affected []Config
	for _, j := range a.jobs {
		if j.DAVID == id {
			affected = append(affected, j)
		}
	}
	a.mu.Unlock()
	if !changed || len(affected) == 0 {
		a.mu.Lock()
		i, prev, ok := a.findDAV(id)
		if !ok {
			a.mu.Unlock()
			a.release("")
			http.Error(w, "找不到存储位置", 404)
			return
		}
		a.davs[i] = d
		e = a.save()
		if e != nil {
			a.davs[i] = prev
		}
		a.mu.Unlock()
		if e != nil {
			a.release("保存存储位置失败：" + e.Error())
		} else {
			a.release("存储位置「" + d.Name + "」已更新。")
		}
		http.Redirect(w, r, base+"/webdav", 303)
		return
	}
	a.mu.Lock()
	a.setMessageLocked("remote", "", fmt.Sprintf("正在验证存储位置的 %d 个关联任务；全部成功后才会切换。", len(affected)))
	a.mu.Unlock()
	a.wg.Add(1)
	go a.syncDAV(d, affected)
	http.Redirect(w, r, base+"/webdav", 303)
}

type stagedDAVJob struct {
	job   Config
	proxy *davProxy
}

func (a *App) syncDAV(d DAVConfig, jobs []Config) {
	defer a.wg.Done()
	var staged []stagedDAVJob
	committed := false
	defer func() {
		if !committed {
			for _, x := range staged {
				if x.proxy != nil {
					x.proxy.Close()
				}
			}
		}
	}()
	for _, old := range jobs {
		cfg := old
		u, e := url.Parse(d.URL)
		if e != nil {
			a.release("更新失败，原配置未变：" + e.Error())
			return
		}
		cfg.URL, e = repositoryURL(u, old.TargetPath)
		if e != nil {
			a.release("更新失败，原配置未变：" + e.Error())
			return
		}
		cfg.LocalPath = ""
		if d.Protocol == "local" {
			cfg.LocalPath, e = localConnectionTarget(d, old.TargetPath)
			if e != nil {
				a.release("更新失败，原配置未变：" + e.Error())
				return
			}
			cfg.URL = cfg.LocalPath
		}
		cfg.User = d.User
		cfg.WebDAVPassword = d.Password
		cfg.InsecureTLS = d.InsecureTLS
		cfg.ConnectionID = random(8)
		cfg.ProxyPort = 0
		cfg.ProxyUser = ""
		cfg.ProxyPassword = ""
		cfg.RepoSizeKnown = false
		cfg.RepoSizeBytes = 0
		cfg.RepoSizeAt = time.Time{}
		cfg.RepoSizeAttemptAt = time.Time{}
		cfg.RepoSizeError = ""
		path, e := a.repositoryConfigPath(cfg)
		if e != nil {
			a.release("更新失败，原配置未变：" + e.Error())
			return
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			a.release("更新失败，原配置未变：" + e.Error())
			return
		}
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
		proxy, e := a.connectRemote(ctx, &cfg, d, "connect")
		if e == nil {
			staged = append(staged, stagedDAVJob{cfg, proxy})
		}
		timedout := ctx.Err() == context.DeadlineExceeded
		cancel()
		if e != nil {
			if timedout {
				e = errors.New("连接超时（5 分钟）")
			}
			a.release("任务「" + old.Name + "」验证失败，原配置未变：" + e.Error())
			return
		}
	}
	a.mu.Lock()
	di, _, ok := a.findDAV(d.ID)
	if !ok {
		a.mu.Unlock()
		a.release("存储位置不存在，原配置未变")
		return
	}
	previousJobs := append([]Config(nil), a.jobs...)
	previousDAVs := append([]DAVConfig(nil), a.davs...)
	var oldProxies []*davProxy
	for _, x := range staged {
		i, current, exists := a.find(x.job.ID)
		if !exists {
			a.jobs = previousJobs
			a.mu.Unlock()
			a.release("关联任务不存在，原配置未变")
			return
		}
		x.job.Paused = current.Paused
		x.job.Next = current.Next
		a.jobs[i] = x.job
		oldProxies = append(oldProxies, a.proxies[x.job.ID])
	}
	a.davs[di] = d
	if e := a.save(); e != nil {
		a.jobs = previousJobs
		a.davs = previousDAVs
		a.mu.Unlock()
		a.release("保存失败，原配置未变：" + e.Error())
		return
	}
	for _, x := range staged {
		if x.proxy != nil {
			a.proxies[x.job.ID] = x.proxy
		} else {
			delete(a.proxies, x.job.ID)
		}
		delete(a.proxyErrors, x.job.ID)
	}
	committed = true
	a.mu.Unlock()
	for _, p := range oldProxies {
		if p != nil {
			p.Close()
		}
	}
	a.release(fmt.Sprintf("存储位置「%s」及 %d 个关联任务已更新。", d.Name, len(staged)))
}
func (a *App) removeDAV(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "请确认删除", 400)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy {
		http.Redirect(w, r, base+"/webdav", 303)
		return
	}
	id := r.PostForm.Get("id")
	i, d, ok := a.findDAV(id)
	if !ok {
		http.Error(w, "找不到存储位置", 404)
		return
	}
	for _, j := range a.jobs {
		if j.DAVID == id {
			a.setMessageLocked("remote", "", "存储位置「"+d.Name+"」仍被任务「"+j.Name+"」使用，请先更换该任务的连接。")
			http.Redirect(w, r, base+"/webdav", 303)
			return
		}
	}
	previous := append([]DAVConfig(nil), a.davs...)
	a.davs = append(a.davs[:i], a.davs[i+1:]...)
	if e := a.save(); e != nil {
		a.davs = previous
		http.Error(w, "保存失败："+e.Error(), 500)
		return
	}
	a.setMessageLocked("remote", "", "已删除存储位置「"+d.Name+"」，不会删除仓库数据。")
	http.Redirect(w, r, base+"/webdav", 303)
}
func (a *App) testDAV(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	var old DAVConfig
	if id := r.PostForm.Get("id"); id != "" {
		a.mu.Lock()
		_, d, ok := a.findDAV(id)
		a.mu.Unlock()
		if !ok {
			http.Error(w, "找不到存储位置", 404)
			return
		}
		old = d
	}
	d, e := parseDAV(r, old)
	if e != nil {
		writeDAVTest(w, false, e.Error())
		return
	}
	if d.Protocol == "local" {
		u, _ := url.Parse(d.URL)
		if _, err := a.selectLocalRepository(u.Path, ""); err != nil {
			writeDAVTest(w, false, err.Error())
		} else {
			writeDAVTest(w, true, "本地目录已授权且可读写、可访问。")
		}
		return
	}
	if d.Protocol == "sftp" {
		if err := testSFTP(r.Context(), d); err != nil {
			writeDAVTest(w, false, "SFTP 连接失败："+err.Error())
		} else {
			writeDAVTest(w, true, "连接成功：SFTP 目录可访问。此测试未检查写入权限。")
		}
		return
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: d.InsecureTLS}, TLSHandshakeTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !strings.EqualFold(req.URL.Host, via[0].URL.Host) || req.URL.Scheme != via[0].URL.Scheme {
			return errors.New("拒绝跨主机、跨协议或过多重定向")
		}
		return nil
	}}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "PROPFIND", d.URL, strings.NewReader("<?xml version=\"1.0\"?><d:propfind xmlns:d=\"DAV:\"><d:prop><d:resourcetype/></d:prop></d:propfind>"))
	if e != nil {
		writeDAVTest(w, false, "地址无效")
		return
	}
	req.SetBasicAuth(d.User, d.Password)
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml")
	resp, e := client.Do(req)
	if e != nil {
		writeDAVTest(w, false, "连接失败："+e.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		writeDAVTest(w, false, fmt.Sprintf("服务器返回 HTTP %d，请检查地址和账号权限。", resp.StatusCode))
		return
	}
	decoder := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	for {
		token, err := decoder.Token()
		if err != nil {
			writeDAVTest(w, false, "服务器响应不是有效的 WebDAV 目录信息，请检查地址。")
			return
		}
		if start, ok := token.(xml.StartElement); ok {
			if start.Name.Space != "DAV:" || start.Name.Local != "multistatus" {
				writeDAVTest(w, false, "服务器响应不是 WebDAV 目录信息，请检查地址。")
				return
			}
			break
		}
	}
	writeDAVTest(w, true, "连接成功：WebDAV 目录可访问。此测试未检查写入权限。")
}
func writeDAVTest(w http.ResponseWriter, ok bool, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}{ok, msg})
}
