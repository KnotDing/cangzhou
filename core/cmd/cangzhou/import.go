package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type importDocument struct {
	Version           int
	ExportedAt        time.Time
	IncludesPasswords bool
	GlobalUploadKiB   int
	DAVs              []exportDAV
	Jobs              []exportJob
}
type importPreview struct {
	CSRF                       string
	DAVs                       []exportDAV
	Jobs                       []exportJob
	ExistingDAVs, ExistingJobs map[string]bool
	Message                    string
	GlobalUploadKiB            int
}

func (a *App) importSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "请求方法不支持", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		http.Error(w, "设置文件过大或格式无效", 400)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if r.FormValue("csrf") != a.csrf {
		http.Error(w, "CSRF 校验失败", 400)
		return
	}
	f, _, e := r.FormFile("settings")
	if e != nil {
		http.Error(w, "请选择设置文件", 400)
		return
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 4<<20))
	var d importDocument
	if e = decoder.Decode(&d); e != nil || d.Version != 1 || d.GlobalUploadKiB < 0 || d.GlobalUploadKiB > maxUploadKiB || len(d.DAVs) > 1000 || len(d.Jobs) > 1000 {
		http.Error(w, "设置文件格式或版本无效", 400)
		return
	}
	var extra any
	if e = decoder.Decode(&extra); e != io.EOF {
		http.Error(w, "设置文件包含多余数据", 400)
		return
	}
	seen := map[string]bool{}
	for _, x := range d.DAVs {
		u, e := url.Parse(x.URL)
		if !validDAVID.MatchString(x.ID) || seen[x.ID] || strings.TrimSpace(x.Name) == "" || len(x.Name) > 120 || x.User == "" || e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "sftp") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || x.InsecureTLS && u.Scheme != "https" || (x.Protocol != "" && x.Protocol != "webdav" && x.Protocol != "sftp") || (x.Protocol == "sftp") != (u.Scheme == "sftp") || (x.Protocol == "sftp" && (x.Host == "" || x.Port < 1 || x.Port > 65535 || x.KnownHosts == "" || !strings.HasPrefix(x.RemotePath, "/") || u.Host != net.JoinHostPort(x.Host, strconv.Itoa(x.Port)) || u.Path != x.RemotePath)) {
			http.Error(w, "连接配置无效或编号重复", 400)
			return
		}
		seen[x.ID] = true
	}
	seen = map[string]bool{}
	for _, x := range d.Jobs {
		if !validJobID.MatchString(x.ID) || seen[x.ID] || strings.TrimSpace(x.Name) == "" || len(x.Name) > 120 || !filepath.IsAbs(x.Source) || (x.LocalPath != "" && (!filepath.IsAbs(x.LocalPath) || x.DAVID != "")) || x.IntervalHours < 0 || x.IntervalHours > 720 || x.UploadLimitKiB < 0 || x.UploadLimitKiB > maxUploadKiB {
			http.Error(w, "任务配置无效或编号重复", 400)
			return
		}
		seen[x.ID] = true
	}
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		http.Error(w, "任务运行中，请稍后导入", 409)
		return
	}
	a.pendingImport = &d
	data := importPreview{CSRF: a.csrf, GlobalUploadKiB: d.GlobalUploadKiB, ExistingDAVs: map[string]bool{}, ExistingJobs: map[string]bool{}}
	for _, x := range a.davs {
		data.ExistingDAVs[x.ID] = true
	}
	for _, x := range a.jobs {
		data.ExistingJobs[x.ID] = true
	}
	for _, x := range d.DAVs {
		x.Password = ""
		x.PrivateKey = ""
		data.DAVs = append(data.DAVs, x)
	}
	for _, x := range d.Jobs {
		x.RepoPassword = ""
		x.WebDAVPassword = ""
		data.Jobs = append(data.Jobs, x)
	}
	a.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = importPreviewPage.Execute(w, data)
}
func (a *App) applyImport(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_, e := a.acquire("导入设置", "")
	if e != nil {
		http.Error(w, "已有任务运行", 409)
		return
	}
	a.mu.Lock()
	doc := a.pendingImport
	existingD := append([]DAVConfig(nil), a.davs...)
	existingJ := append([]Config(nil), a.jobs...)
	a.mu.Unlock()
	if doc == nil {
		a.release("")
		http.Error(w, "请重新选择设置文件", 400)
		return
	}
	davMap := map[string]DAVConfig{}
	jobMap := map[string]bool{}
	for _, d := range existingD {
		davMap[d.ID] = d
	}
	for _, j := range existingJ {
		jobMap[j.ID] = true
	}
	var newDAV []DAVConfig
	var jobs []Config
	skipped := 0
	for _, d := range doc.DAVs {
		if _, ok := davMap[d.ID]; ok {
			skipped++
			continue
		}
		secret := r.PostForm.Get("dav_" + d.ID)
		if secret == "" {
			secret = d.Password
		}
		private := r.PostForm.Get("key_" + d.ID)
		if private == "" {
			private = d.PrivateKey
		}
		if secret == "" && private == "" {
			a.release("")
			http.Error(w, "请填写连接「"+d.Name+"」的密码或私钥", 400)
			return
		}
		x := DAVConfig{ID: d.ID, Name: d.Name, URL: d.URL, User: d.User, Password: secret, InsecureTLS: d.InsecureTLS, Protocol: d.Protocol, Host: d.Host, Port: d.Port, RemotePath: d.RemotePath, KnownHosts: d.KnownHosts, PrivateKey: private}
		davMap[d.ID] = x
		newDAV = append(newDAV, x)
	}
	for _, j := range doc.Jobs {
		if jobMap[j.ID] {
			skipped++
			continue
		}
		d, ok := davMap[j.DAVID]
		if j.LocalPath == "" && !ok {
			a.release("")
			http.Error(w, "任务「"+j.Name+"」缺少关联的连接", 400)
			return
		}
		pw := r.PostForm.Get("repo_" + j.ID)
		if pw == "" {
			pw = j.RepoPassword
		}
		if pw == "" {
			a.release("")
			http.Error(w, "请填写任务「"+j.Name+"」的仓库密码", 400)
			return
		}
		var target string
		if j.LocalPath != "" {
			target = j.LocalPath
		} else {
			u, _ := url.Parse(d.URL)
			var err error
			target, err = repositoryURL(u, j.TargetPath)
			if err != nil {
				a.release("")
				http.Error(w, "目标子目录无效", 400)
				return
			}
		}
		jobs = append(jobs, Config{ID: j.ID, Name: j.Name, DAVID: d.ID, TargetPath: j.TargetPath, LocalPath: j.LocalPath, URL: target, User: d.User, WebDAVPassword: d.Password, RepoPassword: pw, Source: j.Source, IntervalHours: j.IntervalHours, UploadLimitKiB: j.UploadLimitKiB, Paused: j.Paused, InsecureTLS: d.InsecureTLS, ConnectionID: random(8)})
	}
	a.wg.Add(1)
	go a.connectImported(newDAV, jobs, skipped, davMap, r.PostForm.Get("import_global_limit") == "yes", doc.GlobalUploadKiB)
	http.Redirect(w, r, base+"/export", 303)
}
func (a *App) connectImported(davs []DAVConfig, jobs []Config, skipped int, davMap map[string]DAVConfig, importGlobal bool, global int) {
	defer a.wg.Done()
	staged := map[string]*davProxy{}
	committed := false
	defer func() {
		if !committed {
			for _, p := range staged {
				p.Close()
			}
		}
	}()
	for i := range jobs {
		j := &jobs[i]
		if _, e := a.prepareJob(j.ID); e != nil {
			a.release("导入失败，原设置未更改：" + e.Error())
			return
		}
		p, e := a.repositoryConfigPath(*j)
		if e != nil {
			a.release("导入失败：" + e.Error())
			return
		}
		if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			a.release("导入失败：" + e.Error())
			return
		}
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
		proxy, e := a.connectRemote(ctx, j, davMap[j.DAVID], "connect")
		cancel()
		if proxy != nil {
			staged[j.ID] = proxy
		}
		if e != nil {
			a.release("任务「" + j.Name + "」验证失败，原设置未更改：" + e.Error())
			return
		}
		if !j.Paused && j.IntervalHours > 0 {
			j.Next = time.Now().Add(time.Duration(j.IntervalHours) * time.Hour)
		}
	}
	a.mu.Lock()
	prevJ := append([]Config(nil), a.jobs...)
	prevD := append([]DAVConfig(nil), a.davs...)
	prevLimit := a.globalUploadKiB
	if importGlobal {
		a.globalUploadKiB = global
	}
	a.jobs = append(a.jobs, jobs...)
	a.davs = append(a.davs, davs...)
	if e := a.save(); e != nil {
		a.jobs = prevJ
		a.davs = prevD
		a.globalUploadKiB = prevLimit
		a.mu.Unlock()
		a.release("导入失败，原设置未更改：" + e.Error())
		return
	}
	for id, p := range staged {
		a.proxies[id] = p
	}
	a.pendingImport = nil
	committed = true
	a.mu.Unlock()
	a.release(fmt.Sprintf("已导入 %d 个连接、%d 个任务，跳过 %d 项重复配置。", len(davs), len(jobs), skipped))
}
