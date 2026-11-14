package main

import (
	"cangzhou/internal/authority"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type offlineSession struct {
	Cfg       Config
	Temp      string
	Snapshots []Snapshot
}
type offlineEntry struct {
	Name, OpenURL, PickURL string
	IsDir                  bool
}
type offlineData struct {
	CSRF, Message, Path, Pick string
	Roots                     []string
	Entries                   []offlineEntry
	Busy                      bool
	Session                   *offlineSession
}

func (a *App) offlinePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "请求方法不支持", 405)
		return
	}
	a.mu.Lock()
	d := offlineData{CSRF: a.csrf, Message: a.visibleMessageLocked("offline", ""), Roots: append([]string(nil), a.roots...), Busy: a.busy}
	if a.offline != nil {
		x := *a.offline
		x.Cfg = Config{}
		d.Session = &x
	}
	a.mu.Unlock()
	d.Pick = r.URL.Query().Get("pick")
	p := r.URL.Query().Get("path")
	if p != "" {
		c, e := authority.Inspect(d.Roots, p)
		if e != nil || !c.Readable || !c.Searchable {
			http.Error(w, "目录未授权或不可读取", 403)
			return
		}
		d.Path = c.Path
		entries, e := os.ReadDir(c.Path)
		if e != nil {
			http.Error(w, "目录读取失败", 400)
			return
		}
		for _, entry := range entries {
			if len(d.Entries) >= 2000 {
				break
			}
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			full := filepath.Join(c.Path, entry.Name())
			item := offlineEntry{Name: entry.Name(), IsDir: entry.IsDir(), PickURL: base + "/offline?path=" + url.QueryEscape(c.Path) + "&pick=" + url.QueryEscape(full)}
			if entry.IsDir() {
				item.OpenURL = base + "/offline?path=" + url.QueryEscape(full)
			}
			d.Entries = append(d.Entries, item)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = offlineRestorePage.Execute(w, d)
}
func (a *App) offlineOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "请求方法不支持", 405)
		return
	}
	_, err := a.acquire("打开离线仓库", "")
	if err != nil {
		http.Error(w, "已有任务运行", 409)
		return
	}
	succeeded := false
	defer func() {
		if !succeeded {
			a.release("")
		}
	}()
	a.mu.Lock()
	exists := a.offline != nil
	roots := append([]string(nil), a.roots...)
	a.mu.Unlock()
	if exists {
		http.Error(w, "请先关闭已打开的离线仓库", 409)
		return
	}
	temp, e := os.MkdirTemp(a.dir, "offline-")
	if e != nil {
		http.Error(w, "无法创建临时目录", 500)
		return
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(temp)
		}
	}()
	var method, local, password, archive string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, maxArchiveSize+1024*1024)
		mr, e := r.MultipartReader()
		if e != nil {
			http.Error(w, "上传格式无效", 400)
			return
		}
		csrfOK := false
		for {
			part, e := mr.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				http.Error(w, "读取上传失败", 400)
				return
			}
			switch part.FormName() {
			case "csrf":
				b, _ := io.ReadAll(io.LimitReader(part, 256))
				csrfOK = string(b) == a.csrf
			case "password":
				b, _ := io.ReadAll(io.LimitReader(part, 4097))
				if len(b) > 4096 {
					http.Error(w, "密码过长", 400)
					return
				}
				password = string(b)
			case "archive":
				if !csrfOK {
					http.Error(w, "CSRF 校验失败", 400)
					return
				}
				archive = filepath.Join(temp, "upload.zip")
				f, e := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e != nil {
					http.Error(w, "无法保存上传", 500)
					return
				}
				n, e := io.Copy(f, io.LimitReader(part, maxArchiveSize+1))
				ce := f.Close()
				if e != nil || ce != nil || n > maxArchiveSize {
					http.Error(w, "上传失败或超过 64 GiB", 400)
					return
				}
				method = "upload"
			}
			part.Close()
		}
		if !csrfOK {
			http.Error(w, "CSRF 校验失败", 400)
			return
		}
	} else {
		if e := a.post(r); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		method = r.PostForm.Get("mode")
		local = r.PostForm.Get("path")
		password = r.PostForm.Get("password")
	}
	if password == "" {
		http.Error(w, "请输入仓库密码", 400)
		return
	}
	switch method {
	case "directory":
		c, e := authority.Inspect(roots, local)
		if e != nil || !c.Readable || !c.Searchable {
			http.Error(w, "仓库目录未授权或不可读取", 403)
			return
		}
		local = c.Path
		if e = validateLocalRepository(local); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
	case "archive":
		archive, e = authorizedFile(roots, local)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
	case "upload":
		if archive == "" {
			http.Error(w, "请选择 ZIP 文件", 400)
			return
		}
	default:
		http.Error(w, "请选择仓库来源", 400)
		return
	}
	if archive != "" {
		expanded := filepath.Join(temp, "contents")
		if e = os.Mkdir(expanded, 0700); e != nil {
			http.Error(w, "无法创建解压目录", 500)
			return
		}
		if e = unzipRepository(archive, expanded); e != nil {
			http.Error(w, "解压失败："+e.Error(), 400)
			return
		}
		local, e = locateRepository(expanded)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
	}
	id := random(8)
	cfg := Config{ID: id, RepoPassword: password}
	defer func() {
		if !keep {
			if dir, e := a.jobDir(id); e == nil {
				_ = os.RemoveAll(dir)
			}
		}
	}()
	if _, e = a.prepareJob(id); e != nil {
		http.Error(w, "无法创建临时连接", 500)
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()
	if _, e = a.run(ctx, cfg, "repository", "connect", "filesystem", "--path="+local, "--readonly", "--no-persist-credentials", "--no-check-for-updates"); e != nil {
		http.Error(w, "无法打开仓库，请检查密码和仓库文件："+e.Error(), 400)
		return
	}
	out, e := a.run(ctx, cfg, "snapshot", "list", "--all", "--json")
	if e != nil {
		http.Error(w, "无法读取快照："+e.Error(), 400)
		return
	}
	var snapshots []Snapshot
	if e = json.Unmarshal(out, &snapshots); e != nil {
		http.Error(w, "快照列表格式无效", 500)
		return
	}
	a.mu.Lock()
	a.offline = &offlineSession{Cfg: cfg, Temp: temp, Snapshots: snapshots}
	a.setMessageLocked("offline", "", fmt.Sprintf("已只读打开离线仓库，找到 %d 个快照。", len(snapshots)))
	a.mu.Unlock()
	keep = true
	succeeded = true
	a.release("")
	http.Redirect(w, r, base+"/offline", 303)
}
func (a *App) offlineRestore(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_, e := a.acquire("恢复离线快照", "")
	if e != nil {
		http.Error(w, "已有任务运行", 409)
		return
	}
	a.mu.Lock()
	session := a.offline
	a.mu.Unlock()
	id := r.PostForm.Get("id")
	found := false
	if session != nil {
		for _, s := range session.Snapshots {
			if s.Root.Obj == id {
				found = true
			}
		}
	}
	if !found || !objectID.MatchString(id) {
		a.release("")
		http.Error(w, "快照无效", 400)
		return
	}
	cfg := session.Cfg
	var selected []string
	if r.PostForm.Get("selection") == "1" {
		ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
		selected, e = a.validateSelection(ctx, cfg, id, r.PostForm["selected"])
		cancel()
		if e != nil {
			a.release("")
			http.Error(w, "无法恢复所选项目："+e.Error(), http.StatusBadRequest)
			return
		}
	}
	parent, e := os.MkdirTemp(filepath.Join(a.dir, "restores"), "restore-")
	if e != nil {
		a.release("")
		http.Error(w, "无法创建恢复目录", 500)
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(a.ctx, 72*time.Hour)
		defer cancel()
		var e error
		if selected != nil {
			e = a.restoreSelected(ctx, cfg, id, filepath.Join(parent, "files"), selected)
		} else {
			_, e = a.run(ctx, cfg, "restore", "--no-overwrite-files", "--no-overwrite-directories", "--no-overwrite-symlinks", id, filepath.Join(parent, "files"))
		}
		if e == nil {
			e = writeFile(filepath.Join(parent, ".complete"), []byte("ok"))
		}
		if e != nil {
			a.release("恢复失败：" + e.Error())
		} else {
			a.release("离线快照已恢复，可在备份恢复页面下载。")
		}
	}()
	http.Redirect(w, r, base+"/recoveries", 303)
}
func (a *App) offlineClose(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		http.Error(w, "任务运行中", 409)
		return
	}
	s := a.offline
	a.offline = nil
	a.mu.Unlock()
	if s != nil {
		if e := os.RemoveAll(s.Temp); e != nil {
			a.mu.Lock()
			a.setMessageLocked("offline", "", "已关闭仓库，清理临时文件失败："+e.Error())
			a.mu.Unlock()
		}
		if dir, e := a.jobDir(s.Cfg.ID); e == nil {
			os.RemoveAll(dir)
		}
	}
	http.Redirect(w, r, base+"/offline", 303)
}
